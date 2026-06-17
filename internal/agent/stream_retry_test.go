package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// callErrorProvider returns a CALL-level error (StreamCompletion's error return,
// i.e. a connection failure before any stream opens) for the first errCalls
// calls, then streams ok. This is distinct from mockProvider, which only ever
// surfaces errors as in-stream events.
type callErrorProvider struct {
	errCalls int
	err      string
	ok       []zeroruntime.StreamEvent
	calls    int
}

func (p *callErrorProvider) StreamCompletion(_ context.Context, _ zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.calls++
	if p.calls <= p.errCalls {
		return nil, errors.New(p.err)
	}
	ch := make(chan zeroruntime.StreamEvent, len(p.ok))
	for _, event := range p.ok {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestRunAnnotatesUnreachableProviderAfterRetries(t *testing.T) {
	withInstantBackoff(t)
	timeout := zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: `provider stream error: Post "https://ollama.com/v1/chat/completions": net/http: TLS handshake timeout`}
	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{{timeout}, {timeout}, {timeout}}}

	_, err := Run(context.Background(), "build", provider, Options{Registry: tools.NewRegistry()})
	if err == nil {
		t.Fatal("expected an error once every retry failed")
	}
	if !strings.Contains(err.Error(), "/provider") || !strings.Contains(err.Error(), "network/connectivity") {
		t.Fatalf("error should be an actionable network message, got %q", err.Error())
	}
	if len(provider.requests) != 3 { // initial + maxNetworkRetries(2)
		t.Fatalf("expected 3 attempts, got %d", len(provider.requests))
	}
}

func TestIsTransientNetworkError(t *testing.T) {
	transient := []string{
		`provider stream error: Post "https://ollama.com/v1/chat/completions": net/http: TLS handshake timeout`,
		"dial tcp 1.2.3.4:443: i/o timeout",
		"read tcp: connection reset by peer",
		"connection refused",
		"unexpected EOF",
		"network is unreachable",
	}
	for _, reason := range transient {
		if !isTransientNetworkError(reason) {
			t.Errorf("isTransientNetworkError(%q) = false, want true", reason)
		}
	}
	terminal := []string{
		"context canceled",
		"context deadline exceeded",
		"provider error: status code 401: invalid api key",
		"HTTP 429: rate limit exceeded",
		"context length exceeded: 200000 tokens",
		"invalid request: model not found",
	}
	for _, reason := range terminal {
		if isTransientNetworkError(reason) {
			t.Errorf("isTransientNetworkError(%q) = true, want false", reason)
		}
	}
}

func TestDefaultNetworkRetryBackoff(t *testing.T) {
	for attempt, want := range map[int]time.Duration{0: time.Second, 1: 2 * time.Second, 2: 4 * time.Second, 5: 8 * time.Second} {
		if got := defaultNetworkRetryBackoff(attempt); got != want {
			t.Errorf("defaultNetworkRetryBackoff(%d) = %s, want %s", attempt, got, want)
		}
	}
}

// withInstantBackoff zeros the retry backoff for the duration of a test.
func withInstantBackoff(t *testing.T) {
	t.Helper()
	prev := networkRetryBackoff
	networkRetryBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { networkRetryBackoff = prev })
}

func TestRunRetriesTransientNetworkFailure(t *testing.T) {
	withInstantBackoff(t)
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{{Type: zeroruntime.StreamEventError, Error: "provider stream error: net/http: TLS handshake timeout"}},
			{{Type: zeroruntime.StreamEventText, Content: "built it"}, {Type: zeroruntime.StreamEventDone}},
		},
	}
	var retries []int
	result, err := Run(context.Background(), "build", provider, Options{
		Registry:       tools.NewRegistry(),
		OnNetworkRetry: func(attempt int, _ string) { retries = append(retries, attempt) },
	})
	if err != nil {
		t.Fatalf("expected the transient failure to be retried to success, got %v", err)
	}
	if result.FinalAnswer != "built it" {
		t.Fatalf("final answer = %q, want %q", result.FinalAnswer, "built it")
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected 2 provider requests (initial + 1 retry), got %d", len(provider.requests))
	}
	if len(retries) != 1 || retries[0] != 1 {
		t.Fatalf("expected one OnNetworkRetry(attempt=1), got %#v", retries)
	}
}

func TestRunDoesNotRetryTerminalError(t *testing.T) {
	withInstantBackoff(t)
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{{Type: zeroruntime.StreamEventError, Error: "provider error: status code 401: invalid api key"}},
		},
	}
	_, err := Run(context.Background(), "build", provider, Options{Registry: tools.NewRegistry()})
	if err == nil {
		t.Fatal("expected a terminal (auth) error to surface, not be retried")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("a terminal error must not be retried, got %d requests", len(provider.requests))
	}
}

func TestRunDoesNotRetryAfterPartialOutput(t *testing.T) {
	withInstantBackoff(t)
	// Text was already streamed before the error → retrying would duplicate it.
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventText, Content: "partial answer"},
				{Type: zeroruntime.StreamEventError, Error: "net/http: TLS handshake timeout"},
			},
		},
	}
	_, err := Run(context.Background(), "build", provider, Options{Registry: tools.NewRegistry()})
	if err == nil {
		t.Fatal("expected the mid-stream error to surface (no retry after partial output)")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("must not retry once output was produced, got %d requests", len(provider.requests))
	}
}

func TestRunDoesNotRetryAfterReasoningOrDroppedOutput(t *testing.T) {
	withInstantBackoff(t)
	// A stream can surface reasoning blocks or dropped tool-call signals without
	// any text/tool calls. Retrying would duplicate that surfaced output, so the
	// no-output guard must treat either as "produced output" and NOT retry.
	cases := map[string]zeroruntime.StreamEvent{
		"reasoning": {Type: zeroruntime.StreamEventReasoning, ReasoningBlocks: []zeroruntime.ReasoningBlock{{Provider: "anthropic", Type: "thinking", Text: "planning"}}},
		"dropped":   {Type: zeroruntime.StreamEventToolCallDropped},
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			provider := &mockProvider{
				turns: [][]zeroruntime.StreamEvent{
					{output, {Type: zeroruntime.StreamEventError, Error: "net/http: TLS handshake timeout"}},
				},
			}
			_, err := Run(context.Background(), "build", provider, Options{Registry: tools.NewRegistry()})
			if err == nil {
				t.Fatal("expected the error to surface (no retry after non-text output)")
			}
			if len(provider.requests) != 1 {
				t.Fatalf("must not retry once %s output was produced, got %d requests", name, len(provider.requests))
			}
		})
	}
}

func TestRunRetriesInitialCallTransientFailure(t *testing.T) {
	withInstantBackoff(t)
	// The FIRST StreamCompletion CALL fails transiently (connection error before
	// any stream), then succeeds. The call-level failure must be routed through
	// the same retry flow, not returned immediately.
	provider := &callErrorProvider{
		errCalls: 1,
		err:      `Post "https://api.example.com/v1/chat": net/http: TLS handshake timeout`,
		ok:       []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventText, Content: "done"}, {Type: zeroruntime.StreamEventDone}},
	}
	var retries []int
	result, err := Run(context.Background(), "build", provider, Options{
		Registry:       tools.NewRegistry(),
		OnNetworkRetry: func(attempt int, _ string) { retries = append(retries, attempt) },
	})
	if err != nil {
		t.Fatalf("initial call-level transient failure should be retried to success, got %v", err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("final answer = %q, want %q", result.FinalAnswer, "done")
	}
	if provider.calls != 2 {
		t.Fatalf("expected 2 calls (initial + 1 retry), got %d", provider.calls)
	}
	if len(retries) != 1 || retries[0] != 1 {
		t.Fatalf("expected one OnNetworkRetry(attempt=1), got %#v", retries)
	}
}

func TestRunAnnotatesInitialCallAfterRetries(t *testing.T) {
	withInstantBackoff(t)
	// Every initial CALL fails transiently → the surfaced error is the actionable
	// connectivity message, and the call is attempted initial + maxNetworkRetries.
	provider := &callErrorProvider{errCalls: 9, err: "net/http: TLS handshake timeout"}
	_, err := Run(context.Background(), "build", provider, Options{Registry: tools.NewRegistry()})
	if err == nil {
		t.Fatal("expected an error once every initial-call retry failed")
	}
	if !strings.Contains(err.Error(), "/provider") || !strings.Contains(err.Error(), "network/connectivity") {
		t.Fatalf("error should be the actionable network message, got %q", err.Error())
	}
	if provider.calls != 3 { // initial + maxNetworkRetries(2)
		t.Fatalf("expected 3 calls, got %d", provider.calls)
	}
}
