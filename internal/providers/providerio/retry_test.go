//go:build !plan9

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

// wrapDialErrno builds the error shape a dial failure has (a *net.OpError
// wrapping an *os.SyscallError wrapping the errno) so errors.Is reaches the
// errno exactly as in production. These fixtures use the POSIX syscall.Exxx
// constants, which do NOT reproduce a real Windows dial: that carries distinct
// windows.WSA* errnos (Go's syscall.ECONNREFUSED is a value the net package
// never produces on Windows). They still classify on Windows only because
// dialPreSendErrnos there also lists the POSIX constants for exactly this
// fixture reason; the real Windows dial branch is covered separately by
// TestIsPreSendTransportErrorRealRefusedDial, which makes an actual refused dial.
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
// safe to replay and must be retried, bounded by preSendMaxAttempts (its own
// short schedule), unlike an ambiguous post-send failure. Real dial failures
// arrive as an Op=="dial" *net.OpError, so the errno cases are the production
// shape on every platform (Windows included); the string case exercises the
// wording fallback for a dial error already flattened past its errno.
func TestSendWithRetryReplaysProvablyPreSendErrors(t *testing.T) {
	shrinkBackoff(t)
	cases := map[string]error{
		"errno refused (dial)":      wrapDialErrno("dial", syscall.ECONNREFUSED),
		"errno network unreachable": wrapDialErrno("dial", syscall.ENETUNREACH),
		"errno host unreachable":    wrapDialErrno("dial", syscall.EHOSTUNREACH),
		"dial string fallback":      &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")},
		"tls handshake timeout":     errors.New("net/http: TLS handshake timeout"),
		"dns timeout":               &net.DNSError{Err: "server misbehaving", Name: "nope.invalid", IsTimeout: true},
	}
	for name, transportErr := range cases {
		t.Run(name, func(t *testing.T) {
			var calls int32
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				atomic.AddInt32(&calls, 1)
				return nil, transportErr
			})}
			// maxAttempts=6 (the default) proves the pre-send path is bounded by
			// preSendMaxAttempts, not the caller's 429-tuned maxAttempts.
			resp, err := SendWithRetry(context.Background(), client, http.MethodPost, "http://example.invalid", []byte("{}"), nil, 6)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("expected the transport error to surface after retries are exhausted")
			}
			if got := atomic.LoadInt32(&calls); got != preSendMaxAttempts {
				t.Fatalf("pre-send error tried %d times, want %d (preSendMaxAttempts)", got, preSendMaxAttempts)
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
		// The pre-send errnos are also raised on an ESTABLISHED connection: a route
		// dropping mid-generation surfaces on the pending read/write, which is
		// post-send. Scoping to Op=="dial" must keep these from replaying the POST.
		"host unreachable on read is post-send": wrapDialErrno("read", syscall.EHOSTUNREACH),
		"net unreachable on write is post-send": wrapDialErrno("write", syscall.ENETUNREACH),
		// NXDOMAIN is authoritative and deterministic, so retrying it would only
		// stall the agent before failing anyway.
		"dns nxdomain": &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true},
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

// A REAL refused dial (nothing listening on the target) must classify as
// pre-send on every platform. This is the regression the errno-constant fixtures
// miss: on Windows the kernel raises WSAECONNREFUSED, which errors.Is does NOT
// match against syscall.ECONNREFUSED, so before dialPreSendErrnos carried the WSA
// codes a refused dial returned false here and Windows never retried it. Using a
// real Dial exercises the platform's true error shape.
func TestIsPreSendTransportErrorRealRefusedDial(t *testing.T) {
	// Reserve an ephemeral port, then close it, so a dial there is refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if cerr := ln.Close(); cerr != nil {
		t.Fatalf("close listener: %v", cerr)
	}
	conn, dialErr := (&net.Dialer{Timeout: 2 * time.Second}).Dial("tcp", addr)
	if dialErr == nil {
		_ = conn.Close()
		t.Skip("expected a refused dial but the port accepted a connection")
	}
	if !isPreSendTransportError(dialErr) {
		t.Fatalf("real refused dial not classified pre-send: %v", dialErr)
	}
}

func TestIsPreSendTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// DNS: a lookup that could recover is pre-send; NXDOMAIN is permanent.
		{"dns timeout", &net.DNSError{Err: "server misbehaving", Name: "x.invalid", IsTimeout: true}, true},
		{"dns temporary", &net.DNSError{Err: "server misbehaving", Name: "x.invalid", IsTemporary: true}, true},
		{"dns nxdomain is permanent", &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}, false},
		{"tls handshake timeout", errors.New("net/http: TLS handshake timeout"), true},
		// Errno-wrapped DIAL failures: how a real refused/unreachable dial arrives
		// on EVERY platform, including Windows where the wording differs entirely.
		{"errno refused (dial)", wrapDialErrno("dial", syscall.ECONNREFUSED), true},
		{"errno network unreachable (dial)", wrapDialErrno("dial", syscall.ENETUNREACH), true},
		{"errno host unreachable (dial)", wrapDialErrno("dial", syscall.EHOSTUNREACH), true},
		{"dial operror string fallback", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}, true},
		// The SAME errnos raised on a post-send read/write are NOT pre-send — the
		// kernel reports them on an established connection when a route drops.
		{"host unreachable on read is post-send", wrapDialErrno("read", syscall.EHOSTUNREACH), false},
		{"net unreachable on write is post-send", wrapDialErrno("write", syscall.ENETUNREACH), false},
		{"errno reset is post-send", wrapDialErrno("read", syscall.ECONNRESET), false},
		// A refused/unreachable error already flattened past its *net.OpError can't
		// be proven pre-send, so it is NOT retried (conservative direction).
		{"flattened refused, no opError", errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), false},
		{"connection reset", errors.New("read tcp: connection reset by peer"), false},
		{"broken pipe", errors.New("write tcp: broken pipe"), false},
		{"unexpected eof", io.ErrUnexpectedEOF, false},
		{"eof", io.EOF, false},
		{"generic io timeout", errors.New("dial tcp: i/o timeout"), false},
		{"context deadline", context.DeadlineExceeded, false},
		// Exclusion is checked before inclusion, even for a dial OpError: an "i/o
		// timeout" in the message wins over the refused wording.
		{"exclusion wins over inclusion", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused / i/o timeout")}, false},
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

// shrinkBackoff makes both retry schedules (429 and pre-send) negligible for the
// duration of a test.
func shrinkBackoff(t *testing.T) {
	t.Helper()
	savedRetry, savedPreSend := retryBackoffBase, preSendBackoffBase
	retryBackoffBase = time.Millisecond
	preSendBackoffBase = time.Millisecond
	t.Cleanup(func() {
		retryBackoffBase = savedRetry
		preSendBackoffBase = savedPreSend
	})
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

// The pre-send schedule is sub-second and doubles, far shorter than the 429
// schedule: a permanent dial failure fails in ~1.5s across preSendMaxAttempts
// (500ms + 1s) instead of stalling the agent ~60s on 2/4/8/16/30s. This is the
// second half of the fix for a mistyped host / dead local daemon hanging a turn.
func TestPreSendBackoffWaitSchedule(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 500 * time.Millisecond},
		{2, 1 * time.Second},
		{3, 2 * time.Second},
		// The exponent clamps at 5 (500ms<<5 = 16s) so a large attempt count can't
		// overflow; in practice preSendMaxAttempts caps retries at attempt 2, so
		// only the 500ms/1s rungs are ever reached.
		{50, 16 * time.Second},
	}
	for _, c := range cases {
		if got := preSendBackoffWait(c.attempt); got != c.want {
			t.Errorf("preSendBackoffWait(%d) = %v, want %v", c.attempt, got, c.want)
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

// A redirect is NOT followed on the completion path. If it were, client.Do could
// transmit the original POST to the first host, follow the 307/308, and then a
// dial failure to the redirect target would arrive as an Op=="dial" error that
// isPreSendTransportError treats as pre-send, replaying a completion the first
// host already received. With redirects off, the 3xx surfaces as the response and
// the POST is sent exactly once.
func TestSendWithRetryDoesNotFollowRedirects(t *testing.T) {
	var calls int32
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": {"https://redirect-target.invalid/v1"}},
			Body:       http.NoBody,
			Request:    r,
		}, nil
	})}

	resp, err := SendWithRetry(context.Background(), client, http.MethodPost, "https://origin.invalid/v1", []byte("{}"), nil, 3)
	if err != nil {
		t.Fatalf("a 307 must surface as a response, not an error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("want the 307 surfaced unfollowed, got status %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("redirect was followed and/or the POST replayed: transport called %d times, want 1", got)
	}
}

// The pre-send retry budget is independent of the 429/503 status retries: two
// rate-limit responses must not consume the pre-send allowance, so the first
// refused dial after them still gets its own preSendMaxAttempts tries.
func TestSendWithRetryPreSendBudgetSurvivesStatusRetries(t *testing.T) {
	shrinkBackoff(t)
	var calls int32
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Body: http.NoBody, Request: r}, nil
		}
		return nil, wrapDialErrno("dial", syscall.ECONNREFUSED)
	})}

	resp, err := SendWithRetry(context.Background(), client, http.MethodPost, "https://x.invalid/v1", []byte("{}"), nil, 6)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected the exhausted pre-send failure to surface as an error")
	}
	// Two status responses (calls 1-2), then the pre-send failure gets its own
	// budget: preSendMaxAttempts=3 means it retries twice more before returning.
	if got := atomic.LoadInt32(&calls); got != 2+preSendMaxAttempts {
		t.Fatalf("pre-send budget eaten by status retries: transport called %d times, want %d (2 status + %d pre-send)", got, 2+preSendMaxAttempts, preSendMaxAttempts)
	}
}
