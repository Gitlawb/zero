package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewPKCE(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	if p.Method != MethodS256 {
		t.Fatalf("method = %q, want S256", p.Method)
	}
	if len(p.Verifier) != 43 { // base64url(32 bytes) = 43 chars
		t.Fatalf("verifier length = %d, want 43", len(p.Verifier))
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.Challenge != want {
		t.Fatalf("challenge = %q, want base64url(sha256(verifier))", p.Challenge)
	}
}

func TestNewStateUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s, err := NewState()
		if err != nil {
			t.Fatalf("NewState: %v", err)
		}
		if s == "" || seen[s] {
			t.Fatalf("state not unique/non-empty: %q", s)
		}
		seen[s] = true
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	cfg := Config{
		ClientID:              "client-123",
		Scopes:                []string{"read", "write"},
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		ExtraAuthParams:       map[string]string{"login_hint": "a@b.c"},
	}
	pkce := PKCE{Verifier: "v", Challenge: "chal", Method: MethodS256}
	raw, err := BuildAuthorizationURL(cfg, pkce, "state-xyz", "http://127.0.0.1:9/callback", map[string]string{"prompt": "login"})
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             "client-123",
		"redirect_uri":          "http://127.0.0.1:9/callback",
		"state":                 "state-xyz",
		"code_challenge":        "chal",
		"code_challenge_method": "S256",
		"scope":                 "read write",
		"login_hint":            "a@b.c",
		"prompt":                "login",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %q = %q, want %q", k, got, want)
		}
	}
}

func TestBuildAuthorizationURLIgnoresReservedExtras(t *testing.T) {
	cfg := Config{
		ClientID:              "c",
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		// A hostile config tries to weaken the flow via extra params.
		ExtraAuthParams: map[string]string{"state": "attacker", "code_challenge_method": "plain", "login_hint": "keep-me"},
	}
	pkce := PKCE{Challenge: "chal", Method: MethodS256}
	raw, err := BuildAuthorizationURL(cfg, pkce, "real-state", "http://127.0.0.1:9/cb", map[string]string{"redirect_uri": "http://evil/cb"})
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	parsed, _ := url.Parse(raw)
	q := parsed.Query()
	if q.Get("state") != "real-state" {
		t.Fatalf("state overridden: %q", q.Get("state"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("PKCE method downgraded: %q", q.Get("code_challenge_method"))
	}
	if q.Get("redirect_uri") != "http://127.0.0.1:9/cb" {
		t.Fatalf("redirect_uri overridden: %q", q.Get("redirect_uri"))
	}
	if q.Get("login_hint") != "keep-me" {
		t.Fatalf("non-reserved extra param should still apply: %q", q.Get("login_hint"))
	}
}

func TestBuildAuthorizationURLRejectsPlain(t *testing.T) {
	_, err := BuildAuthorizationURL(Config{AuthorizationEndpoint: "https://a/x"}, PKCE{Method: "plain"}, "s", "r", nil)
	if !errors.Is(err, ErrPKCEDowngrade) {
		t.Fatalf("err = %v, want ErrPKCEDowngrade", err)
	}
}

func TestValidateTokenEndpoint(t *testing.T) {
	cases := map[string]bool{
		"https://auth.example.com/token": true,
		"http://127.0.0.1:1455/token":    true, // loopback exempt
		"http://localhost:1455/token":    true,
		"http://[::1]:1455/token":        true,
		"http://auth.example.com/token":  false, // non-https, non-loopback
		"ftp://auth.example.com/token":   false,
	}
	for endpoint, ok := range cases {
		err := validateTokenEndpoint(endpoint)
		if ok && err != nil {
			t.Errorf("validateTokenEndpoint(%q) = %v, want nil", endpoint, err)
		}
		if !ok && err == nil {
			t.Errorf("validateTokenEndpoint(%q) = nil, want error", endpoint)
		}
	}
}

func TestExchangeCodeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" || r.FormValue("code_verifier") != "verifier" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600,"scope":"read write"}`))
	}))
	defer server.Close()

	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := Config{ClientID: "c", TokenEndpoint: server.URL}
	tok, err := ExchangeCode(context.Background(), server.Client(), cfg, "code", "verifier", "http://127.0.0.1/cb", func() time.Time { return now })
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" || tok.TokenType != "Bearer" {
		t.Fatalf("token = %+v", tok)
	}
	if !tok.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expiresAt = %v, want now+1h", tok.ExpiresAt)
	}
	if strings.Join(tok.Scopes, " ") != "read write" {
		t.Fatalf("scopes = %v", tok.Scopes)
	}
}

func TestExchangeCodeErrorRedactsBody(t *testing.T) {
	// The error body contains a stray access_token-looking field; it must NOT
	// surface in the returned error (only error/error_description do).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"bad code","access_token":"SECRET-LEAK"}`))
	}))
	defer server.Close()
	cfg := Config{ClientID: "c", TokenEndpoint: server.URL}
	_, err := ExchangeCode(context.Background(), server.Client(), cfg, "code", "v", "http://127.0.0.1/cb", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid_grant") || !strings.Contains(err.Error(), "bad code") {
		t.Fatalf("error should carry error/description, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "SECRET-LEAK") {
		t.Fatalf("error leaked raw body token material: %q", err.Error())
	}
}

func TestPostTokenRefusesInsecureEndpoint(t *testing.T) {
	_, err := PostToken(context.Background(), http.DefaultClient, "http://auth.example.com/token", url.Values{}, Token{}, nil)
	if !errors.Is(err, ErrInsecureTokenEndpoint) {
		t.Fatalf("err = %v, want ErrInsecureTokenEndpoint", err)
	}
}

func TestRefreshNoToken(t *testing.T) {
	_, err := Refresh(context.Background(), http.DefaultClient, Config{TokenEndpoint: "https://a/token"}, Token{}, nil)
	if !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("err = %v, want ErrNoRefreshToken", err)
	}
}

func TestRefreshPreservesRefreshTokenWhenOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"new-at","expires_in":3600}`)) // no refresh_token in response
	}))
	defer server.Close()
	cfg := Config{ClientID: "c", TokenEndpoint: server.URL}
	tok, err := Refresh(context.Background(), server.Client(), cfg, Token{RefreshToken: "keep-me"}, nil)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "new-at" || tok.RefreshToken != "keep-me" {
		t.Fatalf("refresh should preserve old refresh token: %+v", tok)
	}
}

func TestRefreshPreservesTokenTypeWhenOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"new-at","expires_in":3600}`)) // no token_type in response
	}))
	defer server.Close()
	cfg := Config{ClientID: "c", TokenEndpoint: server.URL}
	tok, err := Refresh(context.Background(), server.Client(), cfg, Token{RefreshToken: "keep-me", TokenType: "Bearer"}, nil)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.TokenType != "Bearer" {
		t.Fatalf("refresh should carry the existing token_type forward, got %q", tok.TokenType)
	}
}
