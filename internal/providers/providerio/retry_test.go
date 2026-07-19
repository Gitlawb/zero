package providerio

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// wrapDialErrno builds the error shape a real dial failure has — a *net.OpError
// wrapping an *os.SyscallError wrapping the errno — so errors.Is reaches the
// errno exactly as it does in production. This is the portable way to exercise
// the Windows path: on Windows the same syscall.Exxx constants carry the WSA*
// values and the .Error() text is the Windows wording, but the errno match is
// identical, so the assertion holds on every platform.
func wrapDialErrno(op string, errno syscall.Errno) error {
	return &net.OpError{Op: op, Net: "tcp", Err: os.NewSyscallError("connectex", errno)}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendWithRetryDoesNotReplayTransportErrors(t *testing.T) {
	var calls int32
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("connection reset by peer")
	})}

	resp, err := SendWithRetry(context.Background(), client, http.MethodPost, "http://example.invalid", []byte("{}"), nil, 3)
	if resp != nil {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("close response body: %v", cerr)
		}
	}
	if err == nil {
		t.Fatal("expected a transport error to surface")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("transport error replayed %d times — a non-idempotent POST must not be retried, want 1", got)
	}
}

// A PROVABLY pre-send transport failure (no request bytes left this host) is
// safe to replay and must be retried up to maxAttempts, unlike an ambiguous
// post-send failure. Covers the string-marker path (refused dial) and the
// typed path (net.DNSError via errors.As).
func TestSendWithRetryReplaysProvablyPreSendErrors(t *testing.T) {
	shrinkBackoff(t)
	cases := map[string]error{
		"connection refused":      errors.New("dial tcp 127.0.0.1:1: connect: connection refused"),
		"network unreachable":     errors.New("dial tcp: connect: network is unreachable"),
		"tls handshake timeout":   errors.New("net/http: TLS handshake timeout"),
		"dns not found":           &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true},
		"errno refused (windows)": wrapDialErrno("dial", syscall.ECONNREFUSED),
	}
	for name, transportErr := range cases {
		t.Run(name, func(t *testing.T) {
			var calls int32
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				atomic.AddInt32(&calls, 1)
				return nil, transportErr
			})}
			resp, err := SendWithRetry(context.Background(), client, http.MethodPost, "http://example.invalid", []byte("{}"), nil, 3)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("expected the transport error to surface after retries are exhausted")
			}
			if got := atomic.LoadInt32(&calls); got != 3 {
				t.Fatalf("pre-send error retried to %d attempts, want 3 (maxAttempts)", got)
			}
		})
	}
}

// An ambiguous transport failure that could have followed a sent request must
// NOT be replayed: a non-idempotent completion POST could duplicate billable
// work. This is the safety line the fix must not cross.
func TestSendWithRetryDoesNotReplayAmbiguousTransportErrors(t *testing.T) {
	for name, transportErr := range map[string]error{
		"generic i/o timeout": errors.New("dial tcp 1.2.3.4:443: i/o timeout"),
		"broken pipe":         errors.New("write tcp: broken pipe"),
		"unexpected eof":      io.ErrUnexpectedEOF,
	} {
		t.Run(name, func(t *testing.T) {
			var calls int32
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				atomic.AddInt32(&calls, 1)
				return nil, transportErr
			})}
			resp, err := SendWithRetry(context.Background(), client, http.MethodPost, "http://example.invalid", []byte("{}"), nil, 3)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("expected the transport error to surface")
			}
			if got := atomic.LoadInt32(&calls); got != 1 {
				t.Fatalf("ambiguous transport error replayed %d times, want 1 (no retry)", got)
			}
		})
	}
}

func TestIsPreSendTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dns not found", &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}, true},
		{"dns timeout", &net.DNSError{Err: "server misbehaving", Name: "x.invalid", IsTimeout: true}, true},
		{"connection refused", errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), true},
		{"network unreachable", errors.New("dial tcp: connect: network is unreachable"), true},
		{"no route to host", errors.New("dial tcp: connect: no route to host"), true},
		{"tls handshake timeout", errors.New("net/http: TLS handshake timeout"), true},
		// Errno-wrapped dials: how a real refused/unreachable dial arrives on
		// EVERY platform, including Windows where the wording differs entirely.
		{"errno refused (portable/windows)", wrapDialErrno("dial", syscall.ECONNREFUSED), true},
		{"errno network unreachable", wrapDialErrno("dial", syscall.ENETUNREACH), true},
		{"errno host unreachable", wrapDialErrno("dial", syscall.EHOSTUNREACH), true},
		{"errno reset is post-send", wrapDialErrno("read", syscall.ECONNRESET), false},
		{"connection reset", errors.New("read tcp: connection reset by peer"), false},
		{"broken pipe", errors.New("write tcp: broken pipe"), false},
		{"unexpected eof", io.ErrUnexpectedEOF, false},
		{"eof", io.EOF, false},
		{"generic io timeout", errors.New("dial tcp: i/o timeout"), false},
		{"context deadline", context.DeadlineExceeded, false},
		{"exclusion wins over inclusion", errors.New("connect: connection refused, then i/o timeout"), false},
		{"host named eof still refused", errors.New("dial tcp eof.example:443: connect: connection refused"), true},
		{"unrelated", errors.New("some other error"), false},
	}
	for _, c := range cases {
		if got := isPreSendTransportError(c.err); got != c.want {
			t.Errorf("%s: isPreSendTransportError(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}

func TestShouldRetryStatus(t *testing.T) {
	cases := map[int]bool{
		http.StatusOK:                  false,
		http.StatusBadRequest:          false,
		http.StatusNotFound:            false,
		http.StatusUnauthorized:        false,
		http.StatusTooManyRequests:     true,  // 429: rate-limited, not accepted
		http.StatusServiceUnavailable:  true,  // 503: unavailable, not accepted
		http.StatusInternalServerError: false, // 500: ambiguous — may have had an effect
		http.StatusBadGateway:          false, // 502: ambiguous
		http.StatusGatewayTimeout:      false, // 504: upstream may have processed it
	}
	for code, want := range cases {
		if got := ShouldRetryStatus(code); got != want {
			t.Errorf("ShouldRetryStatus(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestRetryAfterParsesHeader(t *testing.T) {
	mk := func(value string) *http.Response {
		resp := &http.Response{Header: http.Header{}}
		if value != "" {
			resp.Header.Set("Retry-After", value)
		}
		return resp
	}
	if got := RetryAfter(mk("3")); got != 3*time.Second {
		t.Errorf("RetryAfter(\"3\") = %v, want 3s", got)
	}
	if got := RetryAfter(mk("")); got != 0 {
		t.Errorf("RetryAfter(absent) = %v, want 0", got)
	}
	if got := RetryAfter(mk("0")); got != 0 {
		t.Errorf("RetryAfter(\"0\") = %v, want 0", got)
	}
	if got := RetryAfter(mk("not-a-number")); got != 0 {
		t.Errorf("RetryAfter(garbage) = %v, want 0", got)
	}
	if got := RetryAfter(nil); got != 0 {
		t.Errorf("RetryAfter(nil) = %v, want 0", got)
	}
}

func TestBackoffReturnsFalseOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Backoff(ctx, 5, 0) {
		t.Fatal("Backoff should return false when the context is already cancelled")
	}
}

func TestBackoffWaitsThenReturnsTrue(t *testing.T) {
	// retryAfter overrides the attempt-based wait, keeping the test fast.
	if !Backoff(context.Background(), 1, time.Millisecond) {
		t.Fatal("Backoff should return true after waiting out a short delay")
	}
}

// shrinkBackoff makes retry waits negligible for the duration of a test.
func shrinkBackoff(t *testing.T) {
	t.Helper()
	saved := retryBackoffBase
	retryBackoffBase = time.Millisecond
	t.Cleanup(func() { retryBackoffBase = saved })
}

func TestBackoffWaitSchedule(t *testing.T) {
	// Without Retry-After the wait doubles per attempt from 2s and caps at 30s;
	// a supplied Retry-After wins but is capped too.
	cases := []struct {
		attempt    int
		retryAfter time.Duration
		want       time.Duration
	}{
		{1, 0, 2 * time.Second},
		{2, 0, 4 * time.Second},
		{3, 0, 8 * time.Second},
		{4, 0, 16 * time.Second},
		{5, 0, 30 * time.Second},  // 32s capped
		{50, 0, 30 * time.Second}, // clamped exponent, no overflow
		{1, 7 * time.Second, 7 * time.Second},
		{1, 5 * time.Minute, 30 * time.Second}, // hostile Retry-After capped
	}
	for _, c := range cases {
		if got := backoffWait(c.attempt, c.retryAfter); got != c.want {
			t.Errorf("backoffWait(%d, %v) = %v, want %v", c.attempt, c.retryAfter, got, c.want)
		}
	}
}

func TestSendWithRetryRetriesThenSucceeds(t *testing.T) {
	shrinkBackoff(t)
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // 503: retryable (not accepted)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := SendWithRetry(context.Background(), server.Client(), http.MethodPost, server.URL, []byte("{}"), nil, 3)
	if err != nil {
		t.Fatalf("SendWithRetry returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after retry", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("server hit %d times, want 2 (one failure + one success)", got)
	}
}

func TestSendWithRetryReturnsNonRetryableImmediately(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest) // 400 is not retryable
	}))
	defer server.Close()

	resp, err := SendWithRetry(context.Background(), server.Client(), http.MethodPost, server.URL, []byte("{}"), nil, 3)
	if err != nil {
		t.Fatalf("SendWithRetry returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("server hit %d times, want 1 (no retry on 400)", got)
	}
}

func TestSendWithRetryReturnsLastResponseAfterMaxAttempts(t *testing.T) {
	shrinkBackoff(t)
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable) // always retryable
	}))
	defer server.Close()

	resp, err := SendWithRetry(context.Background(), server.Client(), http.MethodPost, server.URL, []byte("{}"), nil, 2)
	if err != nil {
		t.Fatalf("SendWithRetry returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (exhausted retries surface the response)", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("server hit %d times, want 2 (maxAttempts)", got)
	}
}
