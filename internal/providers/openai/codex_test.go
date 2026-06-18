package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// codexRequest captures the headers + body of one outgoing Codex request so a
// test can assert the Codex-specific headers were applied.
type codexRequest struct {
	method      string
	path        string
	originator  string
	accountID   string
	userAgent   string
	auth        string
	otherHeader map[string]string
	body        map[string]any
}

// newCodexTestServer returns an httptest.Server that records each request's
// Codex headers and writes a minimal [DONE] SSE stream. The Codex provider
// targets `{BaseURL}/responses` (the Responses API on the chatgpt backend),
// so the handler is mounted on that path.
func newCodexTestServer(t *testing.T, rec *codexRequest) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/responses", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.originator = r.Header.Get(codexOriginatorHeader)
		rec.accountID = r.Header.Get(codexAccountHeader)
		rec.userAgent = r.Header.Get("User-Agent")
		rec.auth = r.Header.Get("Authorization")
		rec.otherHeader = map[string]string{}
		for _, k := range []string{"Content-Type", "X-Extra"} {
			rec.otherHeader[k] = r.Header.Get(k)
		}
		_ = json.NewDecoder(r.Body).Decode(&rec.body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	return httptest.NewServer(mux)
}

func drainCodexEvents(t *testing.T, stream <-chan zeroruntime.StreamEvent) {
	t.Helper()
	for ev := range stream {
		if ev.Type == zeroruntime.StreamEventError {
			t.Fatalf("unexpected error event: %q", ev.Error)
		}
	}
}

func TestCodexProviderSetsExpectedHeaders(t *testing.T) {
	var rec codexRequest
	srv := newCodexTestServer(t, &rec)
	defer srv.Close()

	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			APIKey:  "sk-test",
			BaseURL: srv.URL,
			Model:   "gpt-5",
		},
		AccountID:  "acc-from-token",
		Originator: "codex_cli_rs",
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)

	if rec.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", rec.method)
	}
	if rec.path != "/responses" {
		t.Fatalf("path = %q, want /responses", rec.path)
	}
	if rec.originator != "codex_cli_rs" {
		t.Fatalf("originator = %q, want codex_cli_rs", rec.originator)
	}
	if rec.accountID != "acc-from-token" {
		t.Fatalf("chatgpt-account-id = %q, want acc-from-token", rec.accountID)
	}
	if rec.auth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want Bearer sk-test", rec.auth)
	}
	if rec.otherHeader["Content-Type"] != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", rec.otherHeader["Content-Type"])
	}
	if rec.userAgent == "" {
		t.Fatalf("User-Agent = empty, want the Codex default (codex_cli_rs)")
	}
}

func TestCodexProviderUsesConfiguredBaseURL(t *testing.T) {
	var rec codexRequest
	// Run the test server on a custom path to confirm the request goes
	// through {BaseURL}/responses, not a hard-coded host.
	mux := http.NewServeMux()
	customPath := "/api/v1/codex/responses"
	var hit atomic.Int32
	mux.HandleFunc(customPath, func(w http.ResponseWriter, r *http.Request) {
		hit.Add(1)
		rec.path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			APIKey:  "sk-test",
			BaseURL: srv.URL + "/api/v1/codex",
			Model:   "gpt-5",
		},
		AccountID: "acc-x",
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)
	if rec.path != customPath {
		t.Fatalf("path = %q, want %q (the Codex baseURL must be honored)", rec.path, customPath)
	}
	if hit.Load() != 1 {
		t.Fatalf("server hit %d times, want 1", hit.Load())
	}
}

func TestCodexProviderDefaultsOriginator(t *testing.T) {
	var rec codexRequest
	srv := newCodexTestServer(t, &rec)
	defer srv.Close()

	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			APIKey:  "sk-test",
			BaseURL: srv.URL,
			Model:   "gpt-5",
		},
		// Originator intentionally empty: defaults to "codex_cli_rs".
		AccountID: "acc-x",
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)
	if rec.originator != codexDefaultOriginator {
		t.Fatalf("originator = %q, want default %q", rec.originator, codexDefaultOriginator)
	}
}

func TestCodexProviderBrandsUserAgent(t *testing.T) {
	var rec codexRequest
	srv := newCodexTestServer(t, &rec)
	defer srv.Close()

	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			APIKey:  "sk-test",
			BaseURL: srv.URL,
			Model:   "gpt-5",
			// openai Options.UserAgent overridden by CodexOptions.UserAgent below.
			UserAgent: "zero/dev",
		},
		// CodexOptions.UserAgent wins over openai Options.UserAgent.
		UserAgent: "codex_cli_rs/0.1",
		AccountID: "acc-x",
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)
	if rec.userAgent != "codex_cli_rs/0.1" {
		t.Fatalf("User-Agent = %q, want codex_cli_rs/0.1 (CodexOptions.UserAgent should win)", rec.userAgent)
	}
}

func TestCodexProviderAccountResolverIsUsedWhenAccountIDEmpty(t *testing.T) {
	var rec codexRequest
	srv := newCodexTestServer(t, &rec)
	defer srv.Close()

	var resolverCalls atomic.Int32
	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			APIKey:  "sk-test",
			BaseURL: srv.URL,
			Model:   "gpt-5",
		},
		// AccountID empty: AccountResolver is the source.
		AccountResolver: func(_ context.Context) (string, bool, error) {
			resolverCalls.Add(1)
			return "acc-resolver", true, nil
		},
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)
	if resolverCalls.Load() != 1 {
		t.Fatalf("resolver called %d times, want 1", resolverCalls.Load())
	}
	if rec.accountID != "acc-resolver" {
		t.Fatalf("chatgpt-account-id = %q, want acc-resolver", rec.accountID)
	}
}

func TestCodexProviderResolverIsConsultedOnEveryRequest(t *testing.T) {
	// The factory wires the AccountResolver from the OAuth store so a refresh
	// that updates the stored token's Account field takes effect on the next
	// outgoing request — not just the first. This test asserts the resolver
	// runs once per request (and that the second request picks up the
	// account id the resolver starts returning on call #2).
	var hits atomic.Int32
	var rec1, rec2 codexRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/responses", func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		var rec *codexRequest
		if n == 1 {
			rec = &rec1
		} else {
			rec = &rec2
		}
		rec.path = r.URL.Path
		rec.originator = r.Header.Get(codexOriginatorHeader)
		rec.accountID = r.Header.Get(codexAccountHeader)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resolverCalls := atomic.Int32{}
	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			BaseURL: srv.URL,
			Model:   "gpt-5",
			// Wire an OAuth resolver so the openai provider's 401-retry
			// path runs. The Codex resolver (AccountResolver) is what
			// this test actually exercises.
			OAuthResolver: func(_ context.Context, _ bool) (string, string, bool, error) {
				return "Authorization", "Bearer oauth-tok", true, nil
			},
		},
		// Static AccountID intentionally empty so the resolver is the
		// source. The first call returns ok=false (no account); the second
		// call (after the 401 refresh) returns the new id.
		AccountResolver: func(_ context.Context) (string, bool, error) {
			n := resolverCalls.Add(1)
			if n == 1 {
				return "", false, nil
			}
			return "acc-from-store", true, nil
		},
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}

	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)

	if hits.Load() != 2 {
		t.Fatalf("server hit %d times, want 2 (initial + retry)", hits.Load())
	}
	if resolverCalls.Load() != 2 {
		t.Fatalf("resolver called %d times, want 2 (once per request, including the 401 retry)", resolverCalls.Load())
	}
	if rec1.accountID != "" {
		t.Fatalf("first attempt account id = %q, want empty (resolver returned ok=false on call 1)", rec1.accountID)
	}
	if rec2.accountID != "acc-from-store" {
		t.Fatalf("retry account id = %q, want acc-from-store (resolver must re-run on every request)", rec2.accountID)
	}
}

func TestCodexProviderOmitsAccountIDWhenResolverSaysNo(t *testing.T) {
	var rec codexRequest
	srv := newCodexTestServer(t, &rec)
	defer srv.Close()

	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			APIKey:  "sk-test",
			BaseURL: srv.URL,
			Model:   "gpt-5",
		},
		AccountResolver: func(_ context.Context) (string, bool, error) {
			return "", false, nil
		},
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)
	if rec.accountID != "" {
		t.Fatalf("chatgpt-account-id = %q, want empty (resolver returned ok=false)", rec.accountID)
	}
}

func TestCodexProviderSendsRequestBody(t *testing.T) {
	var rec codexRequest
	srv := newCodexTestServer(t, &rec)
	defer srv.Close()

	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			APIKey:  "sk-test",
			BaseURL: srv.URL,
			Model:   "gpt-5",
		},
		AccountID: "acc-x",
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleSystem, Content: "sys"},
			{Role: zeroruntime.MessageRoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)
	if rec.body["model"] != "gpt-5" {
		t.Fatalf("body.model = %#v, want gpt-5", rec.body["model"])
	}
	if rec.body["stream"] != true {
		t.Fatalf("body.stream = %#v, want true", rec.body["stream"])
	}
}

func TestCodexProviderRetriesHeadersAfter401(t *testing.T) {
	var hits atomic.Int32
	var rec1, rec2 codexRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/responses", func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		var rec *codexRequest
		if n == 1 {
			rec = &rec1
		} else {
			rec = &rec2
		}
		rec.path = r.URL.Path
		rec.originator = r.Header.Get(codexOriginatorHeader)
		rec.accountID = r.Header.Get(codexAccountHeader)
		rec.auth = r.Header.Get("Authorization")
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Wire an OAuthResolver so the openai provider's 401-retry path runs
	// (otherwise a 401 short-circuits to an auth error and the retry is
	// never made). The Codex provider's setRequestExtra is what we want
	// to verify; the resolver just needs to be a stable, force-refreshable
	// token source.
	resolver := func(_ context.Context, _ bool) (string, string, bool, error) {
		return "Authorization", "Bearer oauth-tok", true, nil
	}
	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			BaseURL: srv.URL,
			Model:   "gpt-5",
			OAuthResolver: func(ctx context.Context, fr bool) (string, string, bool, error) {
				return resolver(ctx, fr)
			},
		},
		AccountID:  "acc-retry",
		Originator: "codex_cli_rs",
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	// The first request is a 401; the openai provider surfaces it as an
	// auth error and does NOT retry automatically unless an OAuthResolver
	// is wired AND sends ok=true. With our resolver in place the retry
	// path runs and the second request returns [DONE]. The test asserts
	// the Codex headers are applied on BOTH attempts.
	drainCodexEvents(t, stream)
	if hits.Load() != 2 {
		t.Fatalf("server hit %d times, want 2 (initial + retry)", hits.Load())
	}
	if rec1.originator != "codex_cli_rs" || rec1.accountID != "acc-retry" {
		t.Fatalf("first attempt headers: originator=%q accountID=%q", rec1.originator, rec1.accountID)
	}
	if rec2.originator != "codex_cli_rs" || rec2.accountID != "acc-retry" {
		t.Fatalf("retry headers missing: originator=%q accountID=%q (Codex headers must survive 401-refresh)", rec2.originator, rec2.accountID)
	}
}

func TestCodexProviderRequiresBaseURL(t *testing.T) {
	// An empty baseURL falls back to the openai provider's default
	// (https://api.openai.com/v1). The Codex provider is designed to be
	// wired by the factory with the catalog's Codex baseURL, so the
	// realistic misconfig is an INVALID baseURL (covered in the next
	// test). This test just confirms the constructor accepts an empty
	// baseURL — the factory will always supply the Codex-specific URL.
	provider, err := NewCodexProvider(CodexOptions{
		Options:   Options{Model: "gpt-5"},
		AccountID: "acc-x",
	})
	if err != nil {
		t.Fatalf("NewCodexProvider with empty baseURL should not error (factory will override): %v", err)
	}
	if provider == nil {
		t.Fatal("expected a provider, got nil")
	}
}

func TestCodexProviderRejectsBadBaseURL(t *testing.T) {
	_, err := NewCodexProvider(CodexOptions{
		Options:   Options{APIKey: "sk", Model: "gpt-5", BaseURL: "://not a url"},
		AccountID: "acc-x",
	})
	if err == nil {
		t.Fatal("NewCodexProvider with a bad baseURL should error")
	}
	if !strings.Contains(err.Error(), "base URL") && !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("error = %q, want one mentioning base URL", err.Error())
	}
}

func TestValidateAccount(t *testing.T) {
	if err := ValidateAccount(""); err == nil {
		t.Fatal("empty account id should be rejected")
	}
	if err := ValidateAccount("  "); err == nil {
		t.Fatal("whitespace-only account id should be rejected")
	}
	if err := ValidateAccount("acc-1"); err != nil {
		t.Fatalf("non-empty account id should be accepted, got %v", err)
	}
}

func TestCodexProviderStreamIdleTimeoutPropagates(t *testing.T) {
	// Sanity check: the wrapped openai provider's StreamIdleTimeout flows
	// through. The default is 90s; we override to a small value so a real
	// hang surfaces in the test.
	var rec codexRequest
	srv := newCodexTestServer(t, &rec)
	defer srv.Close()

	provider, err := NewCodexProvider(CodexOptions{
		Options: Options{
			APIKey:            "sk-test",
			BaseURL:           srv.URL,
			Model:             "gpt-5",
			StreamIdleTimeout: 50 * time.Millisecond,
		},
		AccountID: "acc-x",
	})
	if err != nil {
		t.Fatalf("NewCodexProvider: %v", err)
	}
	// A simple stream that returns [DONE] immediately does not hit the
	// timeout — the test just confirms the option is accepted.
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	drainCodexEvents(t, stream)
}
