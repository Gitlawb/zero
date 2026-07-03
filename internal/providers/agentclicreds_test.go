package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agentcli"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/providers/providerio"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// notFoundKeychain is a Deps.Keychain stub that always reports "not found" —
// injected into every claude-related test so ExtractCredentials falls through
// to the (fake) CredFiles path instead of ever touching the real macOS
// keychain on a developer machine.
func notFoundKeychain(string) ([]byte, error) {
	return nil, errors.New("keychain: not found")
}

func TestNewCLIAuthedProviderAnthropicUsesBearerAndBetaHeader(t *testing.T) {
	isolateCLIAuthTokenStore(t)
	transport := &captureTransport{responseBody: "data: [DONE]\n\n"}
	future := time.Now().Add(time.Hour).UnixMilli()
	deps := agentcli.Deps{
		Keychain: notFoundKeychain,
		ReadFile: func(path string) ([]byte, error) {
			if strings.HasSuffix(path, ".claude/.credentials.json") {
				return []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok-claude","refreshToken":"r","expiresAt":%d}}`, future)), nil
			}
			return nil, os.ErrNotExist
		},
	}

	provider, err := New(config.ProviderProfile{
		Name:      "claude",
		CatalogID: "anthropic",
		AuthCLI:   "claude",
		Model:     "claude-cli-test-model",
	}, Options{
		HTTPClient:   &http.Client{Transport: transport},
		AgentCLIDeps: deps,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}

	if transport.request == nil {
		t.Fatal("HTTP client was not used")
	}
	if got := transport.request.Header.Get("Authorization"); got != "Bearer tok-claude" {
		t.Fatalf("Authorization = %q, want Bearer tok-claude", got)
	}
	if got := transport.request.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want empty — the bearer must replace it entirely", got)
	}
	// The claude CLI path must send BOTH the oauth beta and the claude-code
	// beta — a Claude Code subscription token is only served to a request that
	// identifies as Claude Code.
	if got := transport.request.Header.Get("anthropic-beta"); !strings.Contains(got, "oauth-2025-04-20") || !strings.Contains(got, "claude-code-20250219") {
		t.Fatalf("anthropic-beta = %q, want both the oauth and claude-code flags", got)
	}
	// The first system block must be the exact Claude Code identity string.
	var sent struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal([]byte(transport.requestBody), &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if len(sent.System) == 0 || sent.System[0].Text != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("system[0] = %+v, want the Claude Code identity block first", sent.System)
	}
}

// isolateCLIAuthTokenStore points the resolver's token cache at a throwaway
// path so provider-level tests never read (or, after a refresh, write) the
// developer's real oauth token store.
func isolateCLIAuthTokenStore(t *testing.T) {
	t.Helper()
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", filepath.Join(t.TempDir(), "oauth-tokens.json"))
}

// TestNewCLIAuthedProviderAnthropicExpiredCredsError locks in the behavior for
// an expired harness login: zero attempts ONE auto-refresh against the OAuth
// token endpoint, and when that fails the stream's error event names the
// harness, the fix command, and the refresh failure — the completion API is
// never called without a valid bearer.
func TestNewCLIAuthedProviderAnthropicExpiredCredsError(t *testing.T) {
	isolateCLIAuthTokenStore(t)
	transport := &captureTransport{responseBody: "data: [DONE]\n\n"}
	past := time.Now().Add(-time.Hour).UnixMilli()
	deps := agentcli.Deps{
		Keychain: notFoundKeychain,
		ReadFile: func(path string) ([]byte, error) {
			if strings.HasSuffix(path, ".claude/.credentials.json") {
				return []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok-claude","refreshToken":"r","expiresAt":%d}}`, past)), nil
			}
			return nil, os.ErrNotExist
		},
	}

	provider, err := New(config.ProviderProfile{
		Name:      "claude",
		CatalogID: "anthropic",
		AuthCLI:   "claude",
		Model:     "claude-cli-test-model",
	}, Options{
		HTTPClient:   &http.Client{Transport: transport},
		AgentCLIDeps: deps,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	var gotErr string
	for event := range stream {
		if event.Type == zeroruntime.StreamEventError {
			gotErr = event.Error
		}
	}
	if !strings.Contains(gotErr, "login has expired") || !strings.Contains(gotErr, "claude") {
		t.Fatalf("stream error = %q, want an actionable claude-login-expired message", gotErr)
	}
	if !strings.Contains(gotErr, "auto-refresh failed") {
		t.Fatalf("stream error = %q, want it to show the refresh was attempted", gotErr)
	}
	// The one permitted request is the refresh attempt against the OAuth token
	// endpoint (which this transport answers with garbage, failing the refresh);
	// the completion API itself must never be called without a valid bearer.
	if transport.request == nil {
		t.Fatal("expected an auto-refresh attempt for expired CLI credentials")
	}
	if got := transport.request.URL.String(); !strings.Contains(got, "platform.claude.com/v1/oauth/token") {
		t.Fatalf("request went to %q, want only the OAuth token endpoint", got)
	}
}

// TestNewCLIAuthedProviderAnthropicMissingCredsError covers the "never logged
// in" case (no credentials file at all), which must fail the same way as an
// expired token rather than falling back to an unauthenticated request.
func TestNewCLIAuthedProviderAnthropicMissingCredsError(t *testing.T) {
	isolateCLIAuthTokenStore(t)
	transport := &captureTransport{responseBody: "data: [DONE]\n\n"}
	deps := agentcli.Deps{
		Keychain: notFoundKeychain,
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}

	provider, err := New(config.ProviderProfile{
		Name:      "claude",
		CatalogID: "anthropic",
		AuthCLI:   "claude",
		Model:     "claude-cli-test-model",
	}, Options{
		HTTPClient:   &http.Client{Transport: transport},
		AgentCLIDeps: deps,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	var gotErr string
	for event := range stream {
		if event.Type == zeroruntime.StreamEventError {
			gotErr = event.Error
		}
	}
	if !strings.Contains(gotErr, "login has expired") {
		t.Fatalf("stream error = %q, want the same actionable message for a never-logged-in harness", gotErr)
	}
}

func TestNewCLIAuthedProviderCodexUsesBearerAndAccountHeader(t *testing.T) {
	transport := &captureTransport{responseBody: "data: [DONE]\n\n"}
	deps := agentcli.Deps{
		ReadFile: func(path string) ([]byte, error) {
			if strings.HasSuffix(path, ".codex/auth.json") {
				return []byte(`{"tokens":{"access_token":"tok-codex","refresh_token":"r","account_id":"acct-codex-1"}}`), nil
			}
			return nil, os.ErrNotExist
		},
	}

	provider, err := New(config.ProviderProfile{
		Name:      "codex",
		CatalogID: "chatgpt",
		AuthCLI:   "codex",
		Model:     "gpt-5.5",
	}, Options{
		HTTPClient:   &http.Client{Transport: transport},
		UserAgent:    "zero-agentcli-test",
		AgentCLIDeps: deps,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}

	if transport.request == nil {
		t.Fatal("HTTP client was not used")
	}
	if !strings.HasSuffix(transport.request.URL.Path, "/responses") {
		t.Fatalf("request URL path = %q, want .../responses", transport.request.URL.Path)
	}
	if got := transport.request.Header.Get("Authorization"); got != "Bearer tok-codex" {
		t.Fatalf("Authorization = %q, want Bearer tok-codex", got)
	}
	if got := transport.request.Header.Get("chatgpt-account-id"); got != "acct-codex-1" {
		t.Fatalf("chatgpt-account-id = %q, want acct-codex-1", got)
	}
	if got := transport.request.Header.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q, want codex_cli_rs", got)
	}
}

// TestNewCLIAuthedProviderCodexNoAccountIDOmitsHeader mirrors the zero-native
// OAuth path's convention (openai.CodexProvider): a missing account id just
// omits the header rather than failing the whole request — only a missing/
// expired bearer is a hard error (see cliBearerResolver vs cliAccountResolver).
func TestNewCLIAuthedProviderCodexNoAccountIDOmitsHeader(t *testing.T) {
	transport := &captureTransport{responseBody: "data: [DONE]\n\n"}
	deps := agentcli.Deps{
		ReadFile: func(path string) ([]byte, error) {
			if strings.HasSuffix(path, ".codex/auth.json") {
				return []byte(`{"tokens":{"access_token":"tok-codex","refresh_token":"r"}}`), nil
			}
			return nil, os.ErrNotExist
		},
	}

	provider, err := New(config.ProviderProfile{
		Name:      "codex",
		CatalogID: "chatgpt",
		AuthCLI:   "codex",
		Model:     "gpt-5.5",
	}, Options{HTTPClient: &http.Client{Transport: transport}, AgentCLIDeps: deps})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}
	if got := transport.request.Header.Get("Authorization"); got != "Bearer tok-codex" {
		t.Fatalf("Authorization = %q, want Bearer tok-codex", got)
	}
	if got := transport.request.Header.Get("chatgpt-account-id"); got != "" {
		t.Fatalf("chatgpt-account-id = %q, want empty when the harness has no stored account id", got)
	}
}

func TestNewCLIAuthedProviderUnknownHarnessErrors(t *testing.T) {
	_, err := New(config.ProviderProfile{
		Name: "x", CatalogID: "anthropic", AuthCLI: "not-a-real-harness", Model: "m",
	}, Options{})
	if err == nil {
		t.Fatal("expected an error for an unknown AuthCLI harness")
	}
}

// TestNewCLIAuthedProviderHarnessWithoutReusableCredsErrors covers a detected
// harness with no CatalogID (e.g. gemini/qwen) named as AuthCLI — this should
// never happen from the wizard (those harnesses render as disabled rows), but
// the factory must still fail clearly rather than silently building a
// keyless, unauthenticated provider.
func TestNewCLIAuthedProviderHarnessWithoutReusableCredsErrors(t *testing.T) {
	if _, ok := agentcli.Lookup("gemini"); !ok {
		t.Fatal("test assumption broken: gemini is no longer in the agentcli catalog")
	}
	_, err := New(config.ProviderProfile{
		Name: "g", CatalogID: "google", AuthCLI: "gemini", Model: "m",
	}, Options{})
	if err == nil {
		t.Fatal("expected an error: gemini has no reusable provider credentials")
	}
}

// fakeCLIAuthTokenStore is an in-memory cliAuthTokenStore.
type fakeCLIAuthTokenStore struct {
	tokens map[string]oauth.Token
	saves  int
}

func (s *fakeCLIAuthTokenStore) Load(key string) (oauth.Token, bool, error) {
	token, ok := s.tokens[key]
	return token, ok, nil
}

func (s *fakeCLIAuthTokenStore) Save(key string, token oauth.Token) error {
	if s.tokens == nil {
		s.tokens = map[string]oauth.Token{}
	}
	s.tokens[key] = token
	s.saves++
	return nil
}

// claudeResolverFixture wires a claudeRefreshingBearerResolver with fully fake
// deps: an extracted credential blob, a store state, and a scripted refresh.
func claudeResolverFixture(t *testing.T, fileBlob string, store *fakeCLIAuthTokenStore, refresh claudeRefreshFunc) (providerio.TokenResolver, *int) {
	t.Helper()
	harness, ok := agentcli.Lookup("claude")
	if !ok {
		t.Fatal("Lookup(claude) failed")
	}
	reads := 0
	deps := agentcli.Deps{
		Home:     "/home/u",
		Keychain: func(string) ([]byte, error) { return nil, errors.New("not found") },
		ReadFile: func(string) ([]byte, error) {
			reads++
			if fileBlob == "" {
				return nil, errors.New("missing")
			}
			return []byte(fileBlob), nil
		},
	}
	return claudeRefreshingBearerResolver(harness, deps, store, refresh), &reads
}

func claudeBlob(access, refreshToken string, expires time.Time) string {
	return `{"claudeAiOauth":{"accessToken":"` + access + `","refreshToken":"` + refreshToken +
		`","expiresAt":` + strconv.FormatInt(expires.UnixMilli(), 10) + `}}`
}

func TestClaudeRefreshingBearerResolver(t *testing.T) {
	future := time.Now().Add(2 * time.Hour)
	past := time.Now().Add(-time.Hour)
	noRefresh := claudeRefreshFunc(func(context.Context, string) (oauth.Token, error) {
		return oauth.Token{}, errors.New("refresh must not be called")
	})

	t.Run("cached store token short-circuits extraction and refresh", func(t *testing.T) {
		store := &fakeCLIAuthTokenStore{tokens: map[string]oauth.Token{
			cliAuthTokenStoreKey: {AccessToken: "at-cached", ExpiresAt: future},
		}}
		resolver, reads := claudeResolverFixture(t, claudeBlob("at-file", "rt-file", future), store, noRefresh)
		_, value, ok, err := resolver(context.Background(), false)
		if err != nil || !ok || value != "Bearer at-cached" {
			t.Fatalf("resolver = %q ok=%v err=%v", value, ok, err)
		}
		if *reads != 0 {
			t.Fatalf("extraction ran %d times, want 0 when the cache is fresh", *reads)
		}
	})

	t.Run("fresh extracted token used without refresh", func(t *testing.T) {
		resolver, _ := claudeResolverFixture(t, claudeBlob("at-file", "rt-file", future), &fakeCLIAuthTokenStore{}, noRefresh)
		_, value, ok, err := resolver(context.Background(), false)
		if err != nil || !ok || value != "Bearer at-file" {
			t.Fatalf("resolver = %q ok=%v err=%v", value, ok, err)
		}
	})

	t.Run("expired extracted token refreshes and persists", func(t *testing.T) {
		store := &fakeCLIAuthTokenStore{}
		var gotRefreshToken string
		refresh := claudeRefreshFunc(func(_ context.Context, rt string) (oauth.Token, error) {
			gotRefreshToken = rt
			return oauth.Token{AccessToken: "at-new", RefreshToken: "rt-new", ExpiresAt: future}, nil
		})
		resolver, _ := claudeResolverFixture(t, claudeBlob("at-old", "rt-old", past), store, refresh)
		_, value, ok, err := resolver(context.Background(), false)
		if err != nil || !ok || value != "Bearer at-new" {
			t.Fatalf("resolver = %q ok=%v err=%v", value, ok, err)
		}
		if gotRefreshToken != "rt-old" {
			t.Fatalf("refresh used %q, want the extracted refresh token", gotRefreshToken)
		}
		saved := store.tokens[cliAuthTokenStoreKey]
		if saved.AccessToken != "at-new" || saved.RefreshToken != "rt-new" {
			t.Fatalf("persisted token = %+v", saved)
		}
	})

	t.Run("refresh failure surfaces the actionable login error", func(t *testing.T) {
		refresh := claudeRefreshFunc(func(context.Context, string) (oauth.Token, error) {
			return oauth.Token{}, errors.New("token endpoint returned 401 Unauthorized")
		})
		resolver, _ := claudeResolverFixture(t, claudeBlob("at-old", "rt-old", past), &fakeCLIAuthTokenStore{}, refresh)
		_, _, _, err := resolver(context.Background(), false)
		if err == nil {
			t.Fatal("want error when refresh fails")
		}
		if !strings.Contains(err.Error(), "run \"claude\"") || !strings.Contains(err.Error(), "auto-refresh failed") {
			t.Fatalf("error = %q, want the actionable hint plus the refresh failure", err)
		}
	})

	t.Run("no refresh token anywhere is the plain expired error", func(t *testing.T) {
		resolver, _ := claudeResolverFixture(t, claudeBlob("at-old", "", past), &fakeCLIAuthTokenStore{}, noRefresh)
		_, _, _, err := resolver(context.Background(), false)
		if err == nil || !strings.Contains(err.Error(), "login has expired") {
			t.Fatalf("error = %v, want the expired-login error", err)
		}
	})

	t.Run("forceRefresh bypasses fresh cache and extraction", func(t *testing.T) {
		store := &fakeCLIAuthTokenStore{tokens: map[string]oauth.Token{
			cliAuthTokenStoreKey: {AccessToken: "at-cached", RefreshToken: "rt-cached", ExpiresAt: future},
		}}
		refreshed := false
		refresh := claudeRefreshFunc(func(context.Context, string) (oauth.Token, error) {
			refreshed = true
			return oauth.Token{AccessToken: "at-forced", ExpiresAt: future}, nil
		})
		resolver, _ := claudeResolverFixture(t, claudeBlob("at-file", "rt-file", future), store, refresh)
		_, value, ok, err := resolver(context.Background(), true)
		if err != nil || !ok || value != "Bearer at-forced" {
			t.Fatalf("resolver = %q ok=%v err=%v", value, ok, err)
		}
		if !refreshed {
			t.Fatal("forceRefresh must invoke the refresh flow")
		}
	})

	t.Run("newer-expiry source wins the refresh token choice", func(t *testing.T) {
		cached := oauth.Token{RefreshToken: "rt-cached", ExpiresAt: past.Add(-time.Hour)}
		extracted := agentcli.Credentials{RefreshToken: "rt-file", ExpiresAt: past}
		if got := newestClaudeRefreshToken(cached, extracted); got != "rt-file" {
			t.Fatalf("newestClaudeRefreshToken = %q, want the later-expiry source", got)
		}
		if got := newestClaudeRefreshToken(oauth.Token{RefreshToken: "rt-cached", ExpiresAt: future}, extracted); got != "rt-cached" {
			t.Fatalf("newestClaudeRefreshToken = %q, want the cached chain when it is newer", got)
		}
		if got := newestClaudeRefreshToken(oauth.Token{}, extracted); got != "rt-file" {
			t.Fatalf("newestClaudeRefreshToken = %q, want the only source with a token", got)
		}
	})
}

// TestClaudeRefreshingBearerResolverSerializesConcurrentRefresh proves the
// resolver's mutex prevents the race CodeRabbit flagged: concurrent
// StreamCompletion calls (e.g. parallel tool calls) sharing one resolver must
// not both see a stale token and both call refresh with the SAME (commonly
// single-use/rotated) refresh token. Without the lock, every one of the
// concurrent callers would race into the refresh branch; with it, only the
// first acquires an expired token, refreshes and saves it, and every other
// caller — blocked until that finishes — re-checks the now-fresh cached token
// and short-circuits instead of refreshing again.
func TestClaudeRefreshingBearerResolverSerializesConcurrentRefresh(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(2 * time.Hour)
	store := &fakeCLIAuthTokenStore{tokens: map[string]oauth.Token{
		cliAuthTokenStoreKey: {AccessToken: "at-stale", RefreshToken: "rt-stale", ExpiresAt: past},
	}}

	var refreshCalls int32
	var concurrentInFlight int32
	refresh := claudeRefreshFunc(func(context.Context, string) (oauth.Token, error) {
		if atomic.AddInt32(&concurrentInFlight, 1) > 1 {
			t.Error("refresh invoked concurrently: resolver is not serializing access")
		}
		defer atomic.AddInt32(&concurrentInFlight, -1)
		atomic.AddInt32(&refreshCalls, 1)
		time.Sleep(20 * time.Millisecond) // widen the race window
		return oauth.Token{AccessToken: "at-fresh", RefreshToken: "rt-fresh", ExpiresAt: future}, nil
	})
	// Extracted file credentials are also expired, so every caller that misses
	// the (initially stale) cache falls through to the refresh branch too.
	resolver, _ := claudeResolverFixture(t, claudeBlob("at-file-stale", "rt-file-stale", past), store, refresh)

	const goroutines = 10
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, _, err := resolver(context.Background(), false)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: resolver returned error: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh called %d times, want exactly 1 (later callers should reuse the winner's saved token)", got)
	}
}
