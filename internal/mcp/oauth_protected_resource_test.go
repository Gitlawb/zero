package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
