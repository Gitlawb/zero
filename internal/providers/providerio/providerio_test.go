package providerio

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// A stalled-but-open upstream must not block forever: the helper aborts after
// the idle timeout, cancels the request context, and returns ErrStreamIdle.
func TestScanSSEDataWithContextAbortsOnIdle(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	// Send one event, then never send anything else and never close.
	go func() {
		_, _ = io.WriteString(pw, "data: first\n\n")
	}()

	cancelled := false
	cancel := func() { cancelled = true }

	var got []string
	done := make(chan error, 1)
	go func() {
		done <- ScanSSEDataWithContext(context.Background(), cancel, pr, 60*time.Millisecond, func(data string) bool {
			got = append(got, data)
			return true
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrStreamIdle) {
			t.Fatalf("err = %v, want ErrStreamIdle", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ScanSSEDataWithContext hung on a stalled stream")
	}

	if len(got) != 1 || got[0] != "first" {
		t.Fatalf("got payloads %#v, want [first]", got)
	}
	if !cancelled {
		t.Fatal("idle abort did not cancel the request context")
	}
}

// ctx cancellation must unblock a hung read and surface ctx.Err().
func TestScanSSEDataWithContextHonorsContextCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ScanSSEDataWithContext(ctx, cancel, pr, time.Hour, func(string) bool { return true })
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ScanSSEDataWithContext did not honor context cancellation")
	}
}

// ctx cancellation must unblock a hung read EVEN WHEN the idle watchdog is
// disabled (idleTimeout <= 0). Regression: the helper used to skip the
// goroutine + select loop when idle was off, so a context cancel could not
// interrupt a parked read and the call hung forever.
func TestScanSSEDataWithContextHonorsCancelWhenIdleDisabled(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// idleTimeout == 0 disables the watchdog; only ctx cancel can return.
		done <- ScanSSEDataWithContext(ctx, cancel, pr, 0, func(string) bool { return true })
	}()

	// Cancel shortly after the call has parked in a blocking read with no data.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ScanSSEDataWithContext hung with idle watchdog disabled; ctx cancel ignored")
	}
}

// Normal completion (EOF) must return nil after delivering all data payloads,
// matching ScanSSEData's multi-line accumulation semantics.
func TestScanSSEDataWithContextDeliversThenEOF(t *testing.T) {
	body := "data: line-a\ndata: line-b\n\ndata: [DONE]\n\n"
	var got []string
	err := ScanSSEDataWithContext(context.Background(), func() {}, strings.NewReader(body), time.Hour, func(data string) bool {
		got = append(got, data)
		return true
	})
	if err != nil {
		t.Fatalf("err = %v, want nil on EOF", err)
	}
	if len(got) != 1 || got[0] != "line-a\nline-b" {
		t.Fatalf("got %#v, want one accumulated payload", got)
	}
}

// UpstreamUnreachable rewrites a transport/gateway connectivity failure into a
// clear message naming the host, and leaves genuine model errors (and bare
// markers with no host) untouched.
func TestUpstreamUnreachable(t *testing.T) {
	cases := []struct {
		name       string
		message    string
		wantMatch  bool
		wantHost   string
		wantReason string
	}{
		{
			name:       "ollama daemon 502 cloud proxy",
			message:    `Post "https://ollama.com:443/v1/chat/completions?ts=1781690613": net/http: TLS handshake timeout`,
			wantMatch:  true,
			wantHost:   "ollama.com:443",
			wantReason: "TLS handshake timeout",
		},
		{
			name:       "direct connection tls timeout",
			message:    `Post "https://ollama.com/v1/chat/completions": net/http: TLS handshake timeout`,
			wantMatch:  true,
			wantHost:   "ollama.com",
			wantReason: "TLS handshake timeout",
		},
		{
			name:       "url form preferred over dial target",
			message:    `Get "https://api.example.com/v1/models": dial tcp 203.0.113.7:443: i/o timeout`,
			wantMatch:  true,
			wantHost:   "api.example.com",
			wantReason: "i/o timeout",
		},
		{
			name:       "dns lookup failure keeps url host",
			message:    `Post "https://ollama.com/api/chat": dial tcp: lookup ollama.com on 8.8.8.8:53: no such host`,
			wantMatch:  true,
			wantHost:   "ollama.com",
			wantReason: "no such host",
		},
		{
			name:       "local daemon not running",
			message:    `dial tcp 127.0.0.1:11434: connect: connection refused`,
			wantMatch:  true,
			wantHost:   "127.0.0.1:11434",
			wantReason: "connection refused",
		},
		{
			name:      "genuine model error untouched",
			message:   `{"error":{"message":"model not found"}}`,
			wantMatch: false,
		},
		{
			name:      "marker without host untouched",
			message:   `context deadline exceeded`,
			wantMatch: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := UpstreamUnreachable(testCase.message)
			if ok != testCase.wantMatch {
				t.Fatalf("match = %v, want %v (got %q)", ok, testCase.wantMatch, got)
			}
			if !testCase.wantMatch {
				if got != testCase.message {
					t.Fatalf("non-match must return input unchanged, got %q", got)
				}
				return
			}
			if !strings.HasPrefix(got, "upstream unreachable: ") {
				t.Errorf("missing prefix in %q", got)
			}
			if !strings.Contains(got, testCase.wantHost) {
				t.Errorf("missing host %q in %q", testCase.wantHost, got)
			}
			if !strings.Contains(got, testCase.wantReason) {
				t.Errorf("missing reason %q in %q", testCase.wantReason, got)
			}
		})
	}
}
