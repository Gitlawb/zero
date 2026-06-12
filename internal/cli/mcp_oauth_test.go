package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
)

// syncBuffer is a goroutine-safe writer used when a background goroutine reads
// CLI output while the command is still writing it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunMCPOAuthStatusReportsPresenceWithoutToken(t *testing.T) {
	store, err := mcp.NewTokenStore(mcp.TokenStoreOptions{
		FilePath: filepath.Join(t.TempDir(), "mcp-oauth-tokens.json"),
	})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := store.Save("remote", mcp.StoredToken{
		AccessToken:  "super-secret-access",
		RefreshToken: "super-secret-refresh",
		ExpiresAt:    expiry,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	deps := appDeps{newMCPTokenStore: func() (*mcp.TokenStore, error) { return store, nil }}

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"mcp", "oauth", "status", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "super-secret-access") || strings.Contains(out, "super-secret-refresh") {
		t.Fatalf("status leaked token material: %s", out)
	}
	var payload struct {
		Tokens []mcp.TokenStatus `json:"tokens"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode status JSON: %v\n%s", err, out)
	}
	if len(payload.Tokens) != 1 {
		t.Fatalf("tokens = %#v, want one", payload.Tokens)
	}
	if !payload.Tokens[0].HasToken || !payload.Tokens[0].HasRefreshToken {
		t.Fatalf("status = %#v, want present token", payload.Tokens[0])
	}
}

func TestRunMCPOAuthLogout(t *testing.T) {
	store, err := mcp.NewTokenStore(mcp.TokenStoreOptions{
		FilePath: filepath.Join(t.TempDir(), "mcp-oauth-tokens.json"),
	})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	if err := store.Save("remote", mcp.StoredToken{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	deps := appDeps{newMCPTokenStore: func() (*mcp.TokenStore, error) { return store, nil }}

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"mcp", "oauth", "logout", "remote", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"removed": true`) {
		t.Fatalf("logout output = %s", stdout.String())
	}
	if _, ok, _ := store.Load("remote"); ok {
		t.Fatal("token still present after logout")
	}
}

func TestRunMCPOAuthLoginStoresTokens(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-final",
			"refresh_token": "refresh-final",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	store, err := mcp.NewTokenStore(mcp.TokenStoreOptions{
		FilePath: filepath.Join(t.TempDir(), "mcp-oauth-tokens.json"),
	})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}

	cwd := t.TempDir()
	deps := appDeps{
		getwd:            func() (string, error) { return cwd, nil },
		newMCPTokenStore: func() (*mcp.TokenStore, error) { return store, nil },
		resolveMCPConfig: func(workspaceRoot string) (config.MCPConfig, error) {
			return config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"remote": {
					Type: "http",
					URL:  "https://remote.invalid/mcp",
					Auth: "oauth",
					OAuth: &config.MCPOAuthConfig{
						ClientID:              "client-123",
						AuthorizationEndpoint: "https://remote.invalid/authorize",
						TokenEndpoint:         tokenServer.URL,
						Scopes:                []string{"read"},
					},
				},
			}}, nil
		},
		now: time.Now,
	}

	// Drive the loopback redirect as soon as the authorization URL is printed.
	stdout := &syncBuffer{}
	stderr := &syncBuffer{}
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			callbackURL := extractCallbackURL(stdout.String())
			if callbackURL != "" {
				_, _ = http.Get(callbackURL)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	// Run the command (which blocks waiting for the callback) off-goroutine and
	// bound it: if the callback is never driven, fail fast instead of hanging
	// until the package test timeout.
	done := make(chan int, 1)
	go func() {
		done <- runWithDeps([]string{"mcp", "oauth", "login", "remote"}, stdout, stderr, deps)
	}()
	var exitCode int
	select {
	case exitCode = <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("login did not complete within 10s; stderr=%s stdout=%s", stderr.String(), stdout.String())
	}
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s stdout=%s", exitCode, stderr.String(), stdout.String())
	}
	token, ok, err := store.Load("remote")
	if err != nil || !ok {
		t.Fatalf("Load() ok=%v err=%v", ok, err)
	}
	if token.AccessToken != "access-final" || token.RefreshToken != "refresh-final" {
		t.Fatalf("stored token = %#v", token)
	}
	// The login output must never echo the issued tokens.
	if strings.Contains(stdout.String(), "access-final") || strings.Contains(stdout.String(), "refresh-final") {
		t.Fatalf("login stdout leaked token: %s", stdout.String())
	}
}

func TestRunMCPOAuthLoginRejectsNonOAuthServer(t *testing.T) {
	cwd := t.TempDir()
	store, err := mcp.NewTokenStore(mcp.TokenStoreOptions{
		FilePath: filepath.Join(t.TempDir(), "mcp-oauth-tokens.json"),
	})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	deps := appDeps{
		getwd:            func() (string, error) { return cwd, nil },
		newMCPTokenStore: func() (*mcp.TokenStore, error) { return store, nil },
		resolveMCPConfig: func(workspaceRoot string) (config.MCPConfig, error) {
			return config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"plain": {Type: "http", URL: "https://plain.invalid/mcp"},
			}}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"mcp", "oauth", "login", "plain"}, &stdout, &stderr, deps)
	if exitCode == exitSuccess {
		t.Fatal("login on non-oauth server should fail")
	}
	if !strings.Contains(stderr.String(), "oauth") {
		t.Fatalf("stderr = %q, want oauth guidance", stderr.String())
	}
}

func TestRunMCPOAuthUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"mcp", "oauth", "bogus"}, &stdout, &stderr, appDeps{})
	if exitCode != exitUsage {
		t.Fatalf("exitCode = %d, want usage error", exitCode)
	}
}

// extractCallbackURL pulls the printed authorization URL and rewrites it into a
// loopback callback hit carrying the code and state.
func extractCallbackURL(output string) string {
	const marker = "https://remote.invalid/authorize"
	index := strings.Index(output, marker)
	if index < 0 {
		return ""
	}
	rest := output[index:]
	if end := strings.IndexAny(rest, " \n"); end >= 0 {
		rest = rest[:end]
	}
	parsed, err := url.Parse(rest)
	if err != nil {
		return ""
	}
	state := parsed.Query().Get("state")
	if state == "" {
		return ""
	}
	redirect := parsed.Query().Get("redirect_uri")
	if redirect == "" {
		return ""
	}
	return redirect + "?code=auth-code&state=" + url.QueryEscape(state)
}
