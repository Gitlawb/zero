package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// flakyProvider fails the first failBefore connect attempts with failErr, then
// succeeds with a single-text stream.
type flakyProvider struct {
	calls      int32
	failBefore int32
	failErr    error
}

func (p *flakyProvider) StreamCompletion(_ context.Context, _ zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	n := atomic.AddInt32(&p.calls, 1)
	if n <= p.failBefore {
		return nil, p.failErr
	}
	ch := make(chan zeroruntime.StreamEvent, 1)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestStreamWithReconnectRecoversFromTransientDisconnect(t *testing.T) {
	p := &flakyProvider{failBefore: 1, failErr: errors.New("unexpected EOF")}
	stream, err := streamWithReconnect(context.Background(), p, zeroruntime.CompletionRequest{}, nil)
	if err != nil {
		t.Fatalf("expected reconnect to recover, got %v", err)
	}
	if stream == nil {
		t.Fatal("expected a live stream after reconnect")
	}
	if got := atomic.LoadInt32(&p.calls); got != 2 {
		t.Fatalf("expected 2 connect attempts (1 fail + 1 success), got %d", got)
	}
}

func TestStreamWithReconnectGivesUpAfterMax(t *testing.T) {
	// Always fails with a disconnect error → exhausts retries and returns it.
	p := &flakyProvider{failBefore: 99, failErr: errors.New("connection reset by peer")}
	_, err := streamWithReconnect(context.Background(), p, zeroruntime.CompletionRequest{}, nil)
	if err == nil {
		t.Fatal("expected an error after exhausting reconnects")
	}
	// 1 initial + maxStreamReconnects retries.
	if got := atomic.LoadInt32(&p.calls); got != int32(1+maxStreamReconnects) {
		t.Fatalf("expected %d attempts, got %d", 1+maxStreamReconnects, got)
	}
}

func TestStreamWithReconnectDoesNotRetryNonDisconnect(t *testing.T) {
	// A context-limit error is the compactor's job, not the reconnect's — return
	// immediately without retrying.
	p := &flakyProvider{failBefore: 99, failErr: errors.New("context length exceeded")}
	_, err := streamWithReconnect(context.Background(), p, zeroruntime.CompletionRequest{}, nil)
	if err == nil {
		t.Fatal("expected the original error")
	}
	if got := atomic.LoadInt32(&p.calls); got != 1 {
		t.Fatalf("context-limit error must not be retried, got %d attempts", got)
	}
}

func TestStreamWithReconnectStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &flakyProvider{failBefore: 99, failErr: errors.New("i/o timeout")}
	_, err := streamWithReconnect(ctx, p, zeroruntime.CompletionRequest{}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	// Cancelled ctx → no retry beyond the first attempt.
	if got := atomic.LoadInt32(&p.calls); got != 1 {
		t.Fatalf("cancelled context must not retry, got %d attempts", got)
	}
}

func TestShouldReconnectClassification(t *testing.T) {
	ctx := context.Background()
	disconnects := []string{
		"unexpected EOF", "connection reset by peer", "broken pipe",
		"i/o timeout", "503 Service Unavailable", "server closed the connection",
	}
	for _, m := range disconnects {
		if !shouldReconnect(ctx, errors.New(m)) {
			t.Errorf("expected reconnect for %q", m)
		}
	}
	notDisconnects := []string{
		"context length exceeded", "invalid api key", "model not found",
		"400 bad request: unsupported parameter",
	}
	for _, m := range notDisconnects {
		if shouldReconnect(ctx, errors.New(m)) {
			t.Errorf("did NOT expect reconnect for %q", m)
		}
	}
}

func TestBackoffGrows(t *testing.T) {
	if backoffFor(1) != streamReconnectBase {
		t.Fatalf("attempt 1 backoff = %v, want %v", backoffFor(1), streamReconnectBase)
	}
	if backoffFor(2) != 2*streamReconnectBase {
		t.Fatalf("attempt 2 backoff = %v, want %v", backoffFor(2), 2*streamReconnectBase)
	}
}

func TestReconnectNoticeRoutesThroughReasoning(t *testing.T) {
	var got string
	notify := reconnectNoticeFor(Options{OnReasoning: func(s string) { got += s }})
	if notify == nil {
		t.Fatal("expected a notifier when OnReasoning is set")
	}
	notify(1, 2)
	if got == "" || !contains(got, "reconnecting 1/2") {
		t.Fatalf("notice = %q, want a reconnecting message", got)
	}
	if reconnectNoticeFor(Options{}) != nil {
		t.Fatal("expected nil notifier when OnReasoning is unset")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

var _ = time.Second
