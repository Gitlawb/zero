package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseResourceMetadataURL(t *testing.T) {
	got, found, err := parseResourceMetadataURL([]string{
		`Basic realm="resource_metadata=not-a-parameter"`,
		`Bearer realm="OAuth", scope="read", Resource_Metadata="https://mcp.example/resource-metadata"`,
	})
	if err != nil || !found {
		t.Fatalf("parseResourceMetadataURL() found=%v err=%v", found, err)
	}
	if got != "https://mcp.example/resource-metadata" {
		t.Fatalf("resource metadata URL = %q", got)
	}

	got, found, err = parseResourceMetadataURL([]string{
		`Basic realm="unterminated`,
		`Bearer resource_metadata="https://mcp.example/fallback-metadata"`,
	})
	if err != nil || !found || got != "https://mcp.example/fallback-metadata" {
		t.Fatalf("unrelated malformed challenge blocked discovery: got=%q found=%v err=%v", got, found, err)
	}

	for name, headers := range map[string][]string{
		"unquoted":     {`Bearer resource_metadata=https://mcp.example/metadata`},
		"unterminated": {`Bearer resource_metadata="https://mcp.example/metadata`},
		"conflicting": {
			`Bearer resource_metadata="https://mcp.example/one"`,
			`DPoP resource_metadata="https://mcp.example/two"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseResourceMetadataURL(headers); err == nil {
				t.Fatal("expected malformed resource_metadata rejection")
			}
		})
	}
}

func TestProtectedResourceDiscoveryRejectsInvalidMetadata(t *testing.T) {
	for _, test := range []struct {
		name      string
		challenge func(base string) string
		metadata  func(base string) string
		wantError string
	}{
		{
			name:      "malformed challenge",
			challenge: func(string) string { return `Bearer resource_metadata="unterminated` },
			wantError: "WWW-Authenticate",
		},
		{
			name:      "insecure metadata URL",
			challenge: func(string) string { return `Bearer resource_metadata="http://metadata.example/resource"` },
			wantError: "metadata URL",
		},
		{
			name:      "invalid JSON",
			challenge: func(base string) string { return `Bearer resource_metadata="` + base + `/metadata"` },
			metadata:  func(string) string { return `{` },
			wantError: "decode protected resource metadata",
		},
		{
			name:      "resource mismatch",
			challenge: func(base string) string { return `Bearer resource_metadata="` + base + `/metadata"` },
			metadata: func(base string) string {
				return `{"resource":"` + base + `/other","authorization_servers":["https://auth.example"]}`
			},
			wantError: "does not match",
		},
		{
			name:      "authorization servers missing",
			challenge: func(base string) string { return `Bearer resource_metadata="` + base + `/metadata"` },
			metadata: func(base string) string {
				return `{"resource":"` + base + `/mcp"}`
			},
			wantError: "authorization_servers",
		},
		{
			name:      "insecure authorization server",
			challenge: func(base string) string { return `Bearer resource_metadata="` + base + `/metadata"` },
			metadata: func(base string) string {
				return `{"resource":"` + base + `/mcp","authorization_servers":["http://auth.example"]}`
			},
			wantError: "authorization server",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				base := "http://" + r.Host
				switch r.URL.Path {
				case "/mcp":
					w.Header().Set("WWW-Authenticate", test.challenge(base))
					w.WriteHeader(http.StatusUnauthorized)
				case "/metadata":
					if test.metadata == nil {
						http.NotFound(w, r)
						return
					}
					_, _ = w.Write([]byte(test.metadata(base)))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			_, _, err := discoverProtectedResourceAuthorizationServer(context.Background(), server.Client(), server.URL+"/mcp")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestProtectedResourceProbeSendsNoCredentials(t *testing.T) {
	var authorization, cookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		cookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, found, err := discoverProtectedResourceAuthorizationServer(context.Background(), server.Client(), server.URL+"/mcp")
	if err != nil || found {
		t.Fatalf("discovery found=%v err=%v", found, err)
	}
	if authorization != "" || cookie != "" {
		t.Fatalf("probe sent credentials: authorization=%q cookie=%q", authorization, cookie)
	}
}

func TestProtectedResourceProbeDoesNotFollowRedirects(t *testing.T) {
	var redirectedHits atomic.Int64
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedHits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer redirected.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirected.URL+"/mcp", http.StatusFound)
	}))
	defer source.Close()

	_, found, err := discoverProtectedResourceAuthorizationServer(context.Background(), source.Client(), source.URL+"/mcp")
	if err != nil || found {
		t.Fatalf("redirected probe found=%v err=%v", found, err)
	}
	if redirectedHits.Load() != 0 {
		t.Fatalf("probe followed redirect %d time(s)", redirectedHits.Load())
	}
}

func TestProtectedResourceMetadataDoesNotFollowRedirects(t *testing.T) {
	var redirectedHits atomic.Int64
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "https://attacker.example/mcp",
			"authorization_servers": []string{"https://attacker.example"},
		})
	}))
	defer redirected.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/metadata"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/metadata":
			http.Redirect(w, r, redirected.URL+"/metadata", http.StatusFound)
		}
	}))
	defer source.Close()

	_, _, err := discoverProtectedResourceAuthorizationServer(context.Background(), source.Client(), source.URL+"/mcp")
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("error = %v, want redirect refusal", err)
	}
	if redirectedHits.Load() != 0 {
		t.Fatalf("metadata fetch followed redirect %d time(s)", redirectedHits.Load())
	}
}

func TestResolveAuthorizationServerRejectsProtectedIssuerMismatch(t *testing.T) {
	authorizationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://unexpected.example",
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
		})
	}))
	defer authorizationServer.Close()
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/metadata"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              base + "/mcp",
				"authorization_servers": []string{authorizationServer.URL},
			})
		}
	}))
	defer resourceServer.Close()

	_, err := resolveAuthorizationServer(context.Background(), resourceServer.Client(), resourceServer.URL+"/mcp", OAuthConfig{})
	if err == nil || !strings.Contains(err.Error(), "issuer does not match") {
		t.Fatalf("error = %v, want issuer mismatch", err)
	}
}

func TestResolveAuthorizationServerRejectsMissingProtectedIssuer(t *testing.T) {
	authorizationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
		})
	}))
	defer authorizationServer.Close()
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/metadata"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              base + "/mcp",
				"authorization_servers": []string{authorizationServer.URL},
			})
		}
	}))
	defer resourceServer.Close()

	_, err := resolveAuthorizationServer(context.Background(), resourceServer.Client(), resourceServer.URL+"/mcp", OAuthConfig{})
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("error = %v, want missing issuer rejection", err)
	}
}

func TestAuthorizationServerMetadataDoesNotFollowRedirects(t *testing.T) {
	var redirectedHits atomic.Int64
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedHits.Add(1)
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 base,
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
		})
	}))
	defer redirected.Close()
	authorizationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirected.URL+"/.well-known/oauth-authorization-server", http.StatusFound)
	}))
	defer authorizationServer.Close()
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/metadata"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              base + "/mcp",
				"authorization_servers": []string{authorizationServer.URL},
			})
		}
	}))
	defer resourceServer.Close()

	_, err := resolveAuthorizationServer(context.Background(), resourceServer.Client(), resourceServer.URL+"/mcp", OAuthConfig{})
	if err == nil {
		t.Fatal("authorization metadata redirect should fail")
	}
	if redirectedHits.Load() != 0 {
		t.Fatalf("authorization metadata fetch followed redirect %d time(s)", redirectedHits.Load())
	}
}
