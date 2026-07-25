package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/redaction"
)

func TestDiscoverParsesMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 r.Host,
			"authorization_endpoint": "https://issuer.example/authorize",
			"token_endpoint":         "https://issuer.example/token",
			"registration_endpoint":  "https://issuer.example/register",
			"scopes_supported":       []string{"read", "write"},
		})
	}))
	defer server.Close()

	meta, err := discoverAuthorizationServer(context.Background(), http.DefaultClient, server.URL)
	if err != nil {
		t.Fatalf("discoverAuthorizationServer() error = %v", err)
	}
	if meta.AuthorizationEndpoint != "https://issuer.example/authorize" {
		t.Fatalf("authorization endpoint = %q", meta.AuthorizationEndpoint)
	}
	if meta.TokenEndpoint != "https://issuer.example/token" {
		t.Fatalf("token endpoint = %q", meta.TokenEndpoint)
	}
	if meta.RegistrationEndpoint != "https://issuer.example/register" {
		t.Fatalf("registration endpoint = %q", meta.RegistrationEndpoint)
	}
	if len(meta.ScopesSupported) != 2 {
		t.Fatalf("scopes = %#v", meta.ScopesSupported)
	}
}

func TestResolveEndpointsFallsBackToConfig(t *testing.T) {
	// Server with no metadata document: discovery must fail and the resolver must
	// fall back to the explicitly configured authorization endpoint. Only one
	// endpoint is configured so the skip-discovery fast path does not fire;
	// the token endpoint comes from discovery (which fails here), so the
	// resolver must return an error for the missing token endpoint.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := OAuthConfig{
		AuthorizationEndpoint: "https://issuer.example/authorize",
	}
	_, err := resolveAuthorizationServer(context.Background(), http.DefaultClient, server.URL, cfg)
	if err == nil {
		t.Fatal("expected error for missing token endpoint when discovery fails, got nil")
	}
	if !strings.Contains(err.Error(), "token endpoint") {
		t.Fatalf("error = %q, want it to mention token endpoint", err)
	}
}

// Discovered MCP metadata that serves an insecure endpoint must be rejected
// before use, so discovery cannot downgrade the MCP login to an
// attacker-controlled origin (issue #511).
func TestResolveAuthorizationServerRejectsInsecureDiscovered(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"` + server.URL + `","token_endpoint":"` + server.URL + `/token","authorization_endpoint":"http://evil.example/authorize"}`))
	})
	_, err := resolveAuthorizationServer(context.Background(), server.Client(), server.URL, OAuthConfig{})
	if err == nil || !strings.Contains(err.Error(), "authorization endpoint") {
		t.Fatalf("err = %v, want insecure authorization endpoint rejection", err)
	}
}

// roundTripperFunc adapts a function to http.RoundTripper so a test can
// observe every outbound request.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResolveEndpointsSkipsDiscoveryWhenBothConfigured(t *testing.T) {
	// When both endpoints are configured, discovery must be skipped entirely.
	// The client fails any outbound request, so this test distinguishes the
	// fast path from a discovery failure rescued by the config fallback.
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected HTTP request to %s: discovery should be skipped", r.URL)
		return nil, errors.New("discovery must be skipped")
	})}
	cfg := OAuthConfig{
		AuthorizationEndpoint: "  https://issuer.example/authorize  ",
		TokenEndpoint:         "  https://issuer.example/token  ",
		RegistrationEndpoint:  "  https://issuer.example/register  ",
	}
	meta, err := resolveAuthorizationServer(context.Background(), client, "https://issuer.example", cfg)
	if err != nil {
		t.Fatalf("resolveAuthorizationServer() error = %v", err)
	}
	if meta.AuthorizationEndpoint != "https://issuer.example/authorize" {
		t.Fatalf("authorization endpoint = %q, want trimmed config value", meta.AuthorizationEndpoint)
	}
	if meta.TokenEndpoint != "https://issuer.example/token" {
		t.Fatalf("token endpoint = %q, want trimmed config value", meta.TokenEndpoint)
	}
	if meta.RegistrationEndpoint != "https://issuer.example/register" {
		t.Fatalf("registration endpoint = %q, want trimmed config value", meta.RegistrationEndpoint)
	}
}

func TestResolveEndpointsConfigOverridesDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": "https://discovered.example/authorize",
			"token_endpoint":         "https://discovered.example/token",
		})
	}))
	defer server.Close()

	cfg := OAuthConfig{TokenEndpoint: "https://configured.example/token"}
	meta, err := resolveAuthorizationServer(context.Background(), http.DefaultClient, server.URL, cfg)
	if err != nil {
		t.Fatalf("resolveAuthorizationServer() error = %v", err)
	}
	if meta.AuthorizationEndpoint != "https://discovered.example/authorize" {
		t.Fatalf("authorization endpoint = %q, want discovered value", meta.AuthorizationEndpoint)
	}
	if meta.TokenEndpoint != "https://configured.example/token" {
		t.Fatalf("token endpoint = %q, want configured override", meta.TokenEndpoint)
	}
}

func TestPKCEChallengeIsS256OfVerifier(t *testing.T) {
	pkce, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE() error = %v", err)
	}
	if pkce.Method != "S256" {
		t.Fatalf("method = %q, want S256", pkce.Method)
	}
	sum := sha256.Sum256([]byte(pkce.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pkce.Challenge != want {
		t.Fatalf("challenge = %q, want %q", pkce.Challenge, want)
	}
	// Verifiers must carry high entropy and stay within the RFC 7636 length bounds.
	if len(pkce.Verifier) < 43 || len(pkce.Verifier) > 128 {
		t.Fatalf("verifier length = %d, want 43..128", len(pkce.Verifier))
	}
}

func TestAuthorizationURLIncludesPKCEAndState(t *testing.T) {
	flow := &authorizationFlow{
		metadata: authServerMetadata{AuthorizationEndpoint: "https://issuer.example/authorize"},
		config:   OAuthConfig{ClientID: "client-123", Scopes: []string{"read", "write"}},
		pkce:     pkceParams{Challenge: "challenge-value", Method: "S256"},
		state:    "state-token",
	}
	authURL, err := flow.authorizationURL("http://127.0.0.1:54321/callback")
	if err != nil {
		t.Fatalf("authorizationURL() error = %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	query := parsed.Query()
	if query.Get("response_type") != "code" {
		t.Fatalf("response_type = %q", query.Get("response_type"))
	}
	if query.Get("client_id") != "client-123" {
		t.Fatalf("client_id = %q", query.Get("client_id"))
	}
	if query.Get("code_challenge") != "challenge-value" {
		t.Fatalf("code_challenge = %q", query.Get("code_challenge"))
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q", query.Get("code_challenge_method"))
	}
	if query.Get("state") != "state-token" {
		t.Fatalf("state = %q", query.Get("state"))
	}
	if query.Get("redirect_uri") != "http://127.0.0.1:54321/callback" {
		t.Fatalf("redirect_uri = %q", query.Get("redirect_uri"))
	}
	if query.Get("scope") != "read write" {
		t.Fatalf("scope = %q", query.Get("scope"))
	}
}

func TestCallbackRejectsStateMismatch(t *testing.T) {
	flow := &authorizationFlow{state: "expected-state"}
	_, err := flow.parseCallback(url.Values{
		"code":  []string{"the-code"},
		"state": []string{"attacker-state"},
	})
	if err == nil {
		t.Fatal("parseCallback() error = nil, want state mismatch rejection")
	}
	if !strings.Contains(err.Error(), "state") {
		t.Fatalf("error = %q, want state mismatch", err.Error())
	}
}

func TestCallbackAcceptsMatchingState(t *testing.T) {
	flow := &authorizationFlow{state: "expected-state"}
	code, err := flow.parseCallback(url.Values{
		"code":  []string{"the-code"},
		"state": []string{"expected-state"},
	})
	if err != nil {
		t.Fatalf("parseCallback() error = %v", err)
	}
	if code != "the-code" {
		t.Fatalf("code = %q, want the-code", code)
	}
}

func TestCallbackSurfacesProviderError(t *testing.T) {
	flow := &authorizationFlow{state: "expected-state"}
	_, err := flow.parseCallback(url.Values{
		"error":             []string{"access_denied"},
		"error_description": []string{"user said no"},
		"state":             []string{"expected-state"},
	})
	if err == nil {
		t.Fatal("parseCallback() error = nil, want provider error")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("error = %q, want provider error", err.Error())
	}
}

func TestExchangeCodeReturnsTokens(t *testing.T) {
	var gotVerifier, gotCode, gotGrant, gotRedirect string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotVerifier = r.Form.Get("code_verifier")
		gotCode = r.Form.Get("code")
		gotGrant = r.Form.Get("grant_type")
		gotRedirect = r.Form.Get("redirect_uri")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-xyz",
			"refresh_token": "refresh-xyz",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	flow := &authorizationFlow{
		httpClient: http.DefaultClient,
		metadata:   authServerMetadata{TokenEndpoint: server.URL},
		config:     OAuthConfig{ClientID: "client-123"},
		pkce:       pkceParams{Verifier: "verifier-value"},
		now:        time.Now,
	}
	token, err := flow.exchangeCode(context.Background(), "auth-code", "http://127.0.0.1:54321/callback")
	if err != nil {
		t.Fatalf("exchangeCode() error = %v", err)
	}
	if token.AccessToken != "access-xyz" || token.RefreshToken != "refresh-xyz" {
		t.Fatalf("token = %#v", token)
	}
	if token.ExpiresAt.IsZero() {
		t.Fatal("expiry not set from expires_in")
	}
	if gotVerifier != "verifier-value" || gotCode != "auth-code" {
		t.Fatalf("verifier=%q code=%q", gotVerifier, gotCode)
	}
	if gotGrant != "authorization_code" {
		t.Fatalf("grant_type = %q", gotGrant)
	}
	if gotRedirect != "http://127.0.0.1:54321/callback" {
		t.Fatalf("redirect_uri = %q", gotRedirect)
	}
}

func TestRefreshTokenExchange(t *testing.T) {
	var gotGrant, gotRefresh string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrant = r.Form.Get("grant_type")
		gotRefresh = r.Form.Get("refresh_token")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-new",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	cfg := OAuthConfig{ClientID: "client-123", TokenEndpoint: server.URL}
	token, err := refreshAccessToken(context.Background(), http.DefaultClient, cfg, StoredToken{RefreshToken: "refresh-old"}, time.Now)
	if err != nil {
		t.Fatalf("refreshAccessToken() error = %v", err)
	}
	if token.AccessToken != "access-new" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
	// A refresh response without a new refresh_token must preserve the old one.
	if token.RefreshToken != "refresh-old" {
		t.Fatalf("refresh token = %q, want carried over", token.RefreshToken)
	}
	if gotGrant != "refresh_token" || gotRefresh != "refresh-old" {
		t.Fatalf("grant=%q refresh=%q", gotGrant, gotRefresh)
	}
}

func TestRefreshTokenFailureSurfacesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
	}))
	defer server.Close()

	cfg := OAuthConfig{ClientID: "client-123", TokenEndpoint: server.URL}
	_, err := refreshAccessToken(context.Background(), http.DefaultClient, cfg, StoredToken{RefreshToken: "refresh-old"}, time.Now)
	if err == nil {
		t.Fatal("refreshAccessToken() error = nil, want failure")
	}
}

func TestDynamicClientRegistration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "registered-client",
			"client_secret": "registered-secret",
		})
	}))
	defer server.Close()

	clientID, clientSecret, err := registerClient(context.Background(), http.DefaultClient, server.URL, "http://127.0.0.1:54321/callback", []string{"read"})
	if err != nil {
		t.Fatalf("registerClient() error = %v", err)
	}
	if clientID != "registered-client" || clientSecret != "registered-secret" {
		t.Fatalf("client = %q / %q", clientID, clientSecret)
	}
}

func TestTokensNeverAppearInRedactedOutput(t *testing.T) {
	token := StoredToken{
		AccessToken:  "sk-secret-access-token-value",
		RefreshToken: "refresh-secret-value",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	redacted := redaction.RedactValue(token, redaction.Options{})
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted: %v", err)
	}
	output := string(encoded)
	if strings.Contains(output, "sk-secret-access-token-value") {
		t.Fatalf("access token leaked in redacted output: %s", output)
	}
	if strings.Contains(output, "refresh-secret-value") {
		t.Fatalf("refresh token leaked in redacted output: %s", output)
	}
}

func TestFullFlowStoresTokens(t *testing.T) {
	// End-to-end: discovery + authorization URL + simulated callback + exchange,
	// driven by a fake browser that immediately hits the loopback redirect.
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint": "PLACEHOLDER",
				"token_endpoint":         "PLACEHOLDER",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-final",
			"refresh_token": "refresh-final",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	cfg := OAuthConfig{
		ClientID:              "client-123",
		AuthorizationEndpoint: "https://issuer.invalid/authorize",
		TokenEndpoint:         tokenServer.URL,
		Scopes:                []string{"read"},
	}

	// The browser opener receives the authorization URL and replies on the
	// loopback redirect with a code + the issued state.
	opener := func(authURL string) error {
		parsed, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		state := parsed.Query().Get("state")
		redirect := parsed.Query().Get("redirect_uri")
		go func() {
			cbURL := redirect + "?code=auth-code&state=" + url.QueryEscape(state)
			for i := 0; i < 20; i++ {
				resp, err := http.Get(cbURL)
				if err == nil {
					resp.Body.Close()
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		}()
		return nil
	}

	result, err := Login(context.Background(), LoginOptions{
		ServerName:  "demo",
		ServerURL:   "https://issuer.invalid",
		Config:      cfg,
		HTTPClient:  http.DefaultClient,
		OpenBrowser: opener,
		Timeout:     5 * time.Second,
		Now:         time.Now,
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.AccessToken != "access-final" || result.RefreshToken != "refresh-final" {
		t.Fatalf("login token = %#v", result)
	}
}
