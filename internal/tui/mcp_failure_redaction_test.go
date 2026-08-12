package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
)

// THE FAILURE REASON IS WRITTEN BY THE SERVER THAT FAILED.
//
// It is rendered into the panel and persisted into the transcript, so every case
// below is a hostile or careless server echoing back something Zero sent it. The
// assertions are made against the text after sanitizeTerminalReason, because
// that is what the reader sees, and because for the split-secret case it is
// precisely the sanitizer that reassembles the credential.

// failedServerReason builds the state and returns the reason as it will be
// displayed. Asserting on the pre-sanitized field would miss the rejoin.
func failedServerReason(t *testing.T, cfg config.MCPConfig, name string, err error, tokenStore *mcp.TokenStore) string {
	t.Helper()
	state := BuildMCPViewState(MCPStateOptions{
		Config:     cfg,
		TokenStore: tokenStore,
		Skipped:    []mcp.SkippedServer{{Name: name, Err: err}},
	})
	for _, server := range state.Servers {
		if server.Name == name {
			if server.State != "failed" {
				t.Fatalf("server %q state = %q, want failed", name, server.State)
			}
			return sanitizeTerminalReason(server.Error)
		}
	}
	t.Fatalf("server %q missing from the view state", name)
	return ""
}

// A configured credential split by a control byte must not be reassembled.
//
// Redaction matches the configured value literally, so a secret with a byte
// pushed into the middle of it matches nothing. The sanitizer then removes that
// byte WITHOUT leaving a gap and the two halves become adjacent again, which is
// how an unredacted credential reaches the screen and the transcript.
//
// Every splitter here is one the sanitizer drops, so each one rejoins.
func TestFailedServerReasonRedactsASecretSplitByControlBytes(t *testing.T) {
	const secret = "wk-live-4f9c2b7ae1d8"
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "http", URL: "https://docs.example/mcp", Headers: map[string]string{
			"Authorization": secret,
		}},
	}}

	for _, testCase := range []struct {
		name     string
		splitter string
	}{
		{name: "CSI colour sequence", splitter: "\x1b[31m"},
		{name: "OSC sequence terminated by BEL", splitter: "\x1b]0;title\x07"},
		{name: "bare escape byte", splitter: "\x1b"},
		{name: "backspace", splitter: "\x08"},
		{name: "NUL", splitter: "\x00"},
		{name: "DEL", splitter: "\x7f"},
		{name: "C1 control", splitter: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			split := "wk-live-" + testCase.splitter + "4f9c2b7ae1d8"
			reason := failedServerReason(t, cfg, "docs",
				errors.New("handshake rejected, sent "+split), nil)

			if strings.Contains(reason, secret) {
				t.Errorf("the credential was reassembled for display: %q", reason)
			}
			if !strings.Contains(reason, "handshake rejected") {
				t.Errorf("redaction ate the diagnostic text: %q", reason)
			}
		})
	}
}

// The counterpart: a splitter the sanitizer turns into a space does not rejoin,
// so the reason keeps both halves and neither of them is the credential. This
// pins that the fix did not simply delete every control byte and call that
// redaction.
func TestFailedServerReasonKeepsWhitespaceSplitTextReadable(t *testing.T) {
	const secret = "wk-live-4f9c2b7ae1d8"
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "http", URL: "https://docs.example/mcp", Headers: map[string]string{
			"Authorization": secret,
		}},
	}}

	reason := failedServerReason(t, cfg, "docs",
		errors.New("handshake rejected, sent wk-live-\n4f9c2b7ae1d8"), nil)

	if strings.Contains(reason, secret) {
		t.Errorf("a newline-separated value was joined into the credential: %q", reason)
	}
	if !strings.Contains(reason, "handshake rejected") {
		t.Errorf("redaction ate the diagnostic text: %q", reason)
	}
}

// A secret handed to a stdio child as a command-line argument must be redacted
// out of that child's own complaint about it. connectStdio appends the captured
// stderr to the initialization error, and a child that rejects its arguments
// habitually prints the invocation back.
func TestFailedServerReasonRedactsSensitiveStdioArgumentValues(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		args   []string
		secret string
	}{
		{
			name:   "value in the following argument",
			args:   []string{"serve", "--api-key", "arg-secret-abcdefgh"},
			secret: "arg-secret-abcdefgh",
		},
		{
			name:   "value joined with equals",
			args:   []string{"serve", "--api-key=arg-secret-abcdefgh"},
			secret: "arg-secret-abcdefgh",
		},
		{
			// A value carrying "=" must survive the cut intact, or the redaction
			// set holds a truncated string that matches nothing.
			name:   "base64 value with padding",
			args:   []string{"serve", "--token=YWJjZGVmZ2hpamts=="},
			secret: "YWJjZGVmZ2hpamts==",
		},
		{
			name:   "flag and value packed into one argument",
			args:   []string{"serve", "--api-key arg-secret-abcdefgh"},
			secret: "arg-secret-abcdefgh",
		},
		{
			name:   "password flag",
			args:   []string{"--password", "hunter2-abcdefgh"},
			secret: "hunter2-abcdefgh",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"docs": {Type: "stdio", Command: "docs-mcp", Args: testCase.args},
			}}
			reason := failedServerReason(t, cfg, "docs",
				errors.New("initialize failed: usage: docs-mcp "+
					strings.Join(testCase.args, " ")+": unknown flag"), nil)

			if strings.Contains(reason, testCase.secret) {
				t.Errorf("the argument secret reached the panel: %q", reason)
			}
			if !strings.Contains(reason, "initialize failed") {
				t.Errorf("redaction ate the diagnostic text: %q", reason)
			}
		})
	}
}

// A sensitive flag in the last position has no value to redact and must not walk
// off the end of the arguments.
func TestSensitiveArgValuesToleratesATrailingFlag(t *testing.T) {
	if got := sensitiveMCPArgValues([]string{"serve", "--api-key"}); len(got) != 0 {
		t.Errorf("a trailing sensitive flag produced values %q", got)
	}
	// A blank between the flag and the secret must be skipped rather than
	// consumed as the value, or the real secret in the next position is missed.
	got := sensitiveMCPArgValues([]string{"--api-key", "   ", "real-secret-abcdefgh"})
	if len(got) != 1 || got[0] != "real-secret-abcdefgh" {
		t.Errorf("blank argument consumed as the value: %q", got)
	}
	// A flag following a flag is not that flag's value.
	if got := sensitiveMCPArgValues([]string{"--api-key", "--verbose"}); len(got) != 0 {
		t.Errorf("a following flag was collected as a secret: %q", got)
	}
}

// A stored OAuth bearer must be redacted out of a failed server's error.
//
// No pattern can recognize an opaque token by shape, so this can only work by
// value, and the value has to come from the token store. The token is saved
// IDENTITY-BOUND here because that is what the login path does: a redaction that
// looked the token up by server name would find nothing for exactly the servers
// that hold a real bearer, and a test that seeded the store by name would pass
// anyway.
func TestFailedOAuthServerReasonRedactsTheStoredBearerToken(t *testing.T) {
	const access = "opaque-access-token-abcdefgh"
	const refresh = "opaque-refresh-token-abcdefgh"

	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"linear": {Type: "http", URL: "https://linear.example/mcp", Auth: "oauth"},
	}}
	store := newTestMCPTokenStore(t)
	servers, err := mcp.NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("NormalizeConfig() returned %d servers, want 1", len(servers))
	}
	if err := store.SaveForServer(servers[0], mcp.StoredToken{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
	}); err != nil {
		t.Fatalf("SaveForServer() error = %v", err)
	}

	reason := failedServerReason(t, cfg, "linear",
		errors.New(`jsonrpc error: {"error":"invalid_grant","presented":"`+access+
			`","refresh":"`+refresh+`"}`), store)

	if strings.Contains(reason, access) {
		t.Errorf("the stored access token reached the panel: %q", reason)
	}
	if strings.Contains(reason, refresh) {
		t.Errorf("the stored refresh token reached the panel: %q", reason)
	}
	if !strings.Contains(reason, "invalid_grant") {
		t.Errorf("redaction ate the diagnostic text: %q", reason)
	}
}

// The token store is nil whenever its initialization soft-failed at startup, so
// the panel has to render rather than crash. That is a production state, not
// only a test one.
func TestFailedServerReasonToleratesAMissingTokenStore(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"linear": {Type: "http", URL: "https://linear.example/mcp", Auth: "oauth"},
	}}
	reason := failedServerReason(t, cfg, "linear", errors.New("connection refused"), nil)
	if !strings.Contains(reason, "connection refused") {
		t.Errorf("reason = %q, want the underlying failure", reason)
	}
}

// newTestMCPTokenStore builds a store backed by a temp file. Env is set
// explicitly because NewTokenStore otherwise reads ZERO_OAUTH_STORAGE from the
// real process environment, and a developer who has it set to the keyring would
// run this test against their own OS keychain.
func newTestMCPTokenStore(t *testing.T) *mcp.TokenStore {
	t.Helper()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	store, err := mcp.NewTokenStore(mcp.TokenStoreOptions{
		FilePath: filepath.Join(t.TempDir(), "tokens.json"),
		Env:      map[string]string{},
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	return store
}
