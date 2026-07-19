package providerio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Gitlawb/zero/internal/trace"
)

// Transient-failure retry, shared by every provider.
//
// SendWithRetry centralizes one retry policy so all providers behave consistently
// (previously only the OpenAI provider retried; Anthropic and Gemini surfaced the
// first failure).
//
// What is retried: 429 (rate limit) and 503 (service unavailable) — the statuses
// where the server explicitly did NOT accept the request — plus PROVABLY
// pre-send transport failures (DNS resolution, a refused/unreachable dial, a TLS
// handshake timeout) where no request bytes ever left this host. In every one of
// those cases we KNOW the server did not receive and process the request, so a
// replay cannot duplicate work. Other 5xx (500/502/504) and every ambiguous or
// post-send transport error (connection reset, broken pipe, EOF, a generic i/o
// timeout, context deadline) are NOT retried: a completion POST is
// non-idempotent and may already have reached and been processed by the server,
// so replaying it could duplicate (billable) work. Only the INITIAL request is
// ever in scope; once the response body starts streaming it is never re-issued.
// Pre-send classification is by syscall errno (portable across POSIX and
// Windows), so a refused/unreachable dial retries the same on every platform.

const defaultMaxRetryAttempts = 6

// maxBackoff caps a single backoff wait so a hostile or buggy Retry-After can't
// stall the agent for minutes.
const maxBackoff = 30 * time.Second

// retryBackoffBase is the first wait when the server supplied no Retry-After.
// Rate-limit windows are measured in seconds, not milliseconds: retrying a 429
// after 400ms almost always burns the attempt while still limited, so the
// schedule is 2s, 4s, 8s, 16s, then maxBackoff. A var so tests can shrink it.
var retryBackoffBase = 2 * time.Second

// SendWithRetry issues an HTTP request, retrying ONLY the safe-to-replay server
// responses (429 and 503, see ShouldRetryStatus) up to maxAttempts — backing off
// between tries and honoring a server Retry-After header and context
// cancellation. Other 5xx and transport/network errors are returned immediately,
// never replayed (see the package note). The request is rebuilt from body each
// attempt; setHeader (if non-nil) sets headers on every attempt.
//
// It returns the final *http.Response (which the caller inspects for a non-2xx
// status, exactly as before) or an error for a network failure / context
// cancellation. Retries exhausted on a retryable status return that response,
// not an error, so the caller's existing HTTP-error path still runs.
func SendWithRetry(
	ctx context.Context,
	client *http.Client,
	method string,
	url string,
	body []byte,
	setHeader func(*http.Request),
	maxAttempts int,
) (*http.Response, error) {
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxRetryAttempts
	}
	for attempt := 1; ; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if setHeader != nil {
			setHeader(request)
		}

		connectSpan := trace.FromContext(ctx).Span(trace.SpanProviderConnect)
		response, err := client.Do(request)
		connectSpan.End()
		if err != nil {
			// Context cancellation always surfaces as cancellation, never a retry.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// A transport failure on a POST does NOT usually mean the server didn't
			// receive it — the request may have arrived and be generating a
			// (billable, non-idempotent) completion while only the response or
			// connection failed. Replaying that could duplicate work, so it is
			// surfaced immediately. The one safe exception is a PROVABLY pre-send
			// failure (see isPreSendTransportError): the request never left this
			// host, so a replay cannot duplicate anything — exactly like a 429/503
			// where we KNOW the request was not accepted.
			if isPreSendTransportError(err) && attempt < maxAttempts {
				if r := trace.FromContext(ctx); r != nil {
					r.Counter(trace.CounterRetryCount, 1)
				}
				if Backoff(ctx, attempt, 0) {
					continue
				}
				return nil, ctx.Err()
			}
			return nil, err
		}

		if ShouldRetryStatus(response.StatusCode) && attempt < maxAttempts {
			if r := trace.FromContext(ctx); r != nil {
				r.Counter(trace.CounterRetryCount, 1)
			}
			wait := RetryAfter(response)
			_ = response.Body.Close()
			if Backoff(ctx, attempt, wait) {
				continue
			}
			// Backoff aborted: the only reason it returns false is ctx cancellation.
			return nil, ctx.Err()
		}

		// Success, a non-retryable status, or retries exhausted on a retryable
		// status. If the context was cancelled meanwhile, surface that instead of
		// a misclassified upstream status.
		if ctx.Err() != nil {
			_ = response.Body.Close()
			return nil, ctx.Err()
		}
		return response, nil
	}
}

// isPreSendTransportError reports whether a transport error PROVES no request
// bytes reached the server, so replaying the request cannot duplicate a
// billable completion. Only narrow, unambiguous pre-connect failures qualify:
// DNS resolution failure, a refused or unreachable dial, and a TLS handshake
// timeout (the handshake completes before any HTTP request bytes are written).
//
// Classification is by syscall errno wherever possible, via errors.Is, which is
// portable across platforms: Go maps ECONNREFUSED / ENETUNREACH / EHOSTUNREACH
// / ECONNRESET to the host's real values, including the WSA* codes on Windows,
// whose human-readable dial wording differs entirely from POSIX. String markers
// are kept only as a fallback for errors that arrive already flattened to text
// (no errno in the chain).
//
// Ambiguous or post-send failures are deliberately excluded and checked FIRST,
// so an error that is both is never treated as pre-send: a reset or broken pipe
// can follow a request that was already sent, and a bare "i/o timeout" covers
// read timeouts after send as well as dial timeouts, so none of these prove the
// request was not received and a non-idempotent POST must not be replayed on
// them.
func isPreSendTransportError(err error) bool {
	if err == nil {
		return false
	}
	// DNS resolution precedes any connection, so a lookup failure — including a
	// DNS timeout — proves nothing was sent. errors.As unwraps url.Error/OpError.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	// Post-send / ambiguous failures, excluded first. EOF and reset are matched
	// by identity so wording (or a hostname containing "eof") can't fool them.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "i/o timeout") {
		return false
	}
	// Provably pre-send: the connect never completed, so no request bytes left
	// this host. Errno match is authoritative and platform-independent; the
	// string markers only catch a POSIX error already flattened to text.
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}
	switch {
	case strings.Contains(msg, "connection refused"):
		return true
	case strings.Contains(msg, "network is unreachable"), strings.Contains(msg, "no route to host"):
		return true
	case strings.Contains(msg, "tls handshake timeout"):
		return true
	}
	return false
}

// ShouldRetryStatus reports whether an HTTP status is safe to retry for a
// non-idempotent completion POST: 429 (Too Many Requests), 503 (Service
// Unavailable), and 529 (Anthropic's "overloaded"). All mean the server
// explicitly did NOT accept the request — it was rate-limited or the service
// was unavailable — so replaying it cannot duplicate work. Other 5xx
// (500/502/504) are deliberately NOT retried: they do not guarantee the
// request had no effect (e.g. a 504 gateway timeout may follow an upstream
// that already produced a billable completion), so replaying them risks
// duplicate work.
func ShouldRetryStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable || code == 529
}

// Backoff waits before retry attempt N (1-based), returning false if the context
// is cancelled during the wait. A server-supplied (positive) Retry-After wins;
// otherwise the wait doubles from retryBackoffBase per attempt. Either way the
// wait is capped at maxBackoff.
func Backoff(ctx context.Context, attempt int, retryAfter time.Duration) bool {
	timer := time.NewTimer(backoffWait(attempt, retryAfter))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// backoffWait computes the wait before retry attempt N (1-based): Retry-After
// when supplied, else exponential from retryBackoffBase, both capped at
// maxBackoff. The exponent is clamped so a large attempt count cannot overflow.
func backoffWait(attempt int, retryAfter time.Duration) time.Duration {
	wait := retryAfter
	if wait <= 0 {
		exponent := attempt - 1
		if exponent > 5 {
			exponent = 5
		}
		if exponent < 0 {
			exponent = 0
		}
		wait = retryBackoffBase * time.Duration(1<<exponent)
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	return wait
}

// RetryAfter parses a response's Retry-After header (delay-seconds or an HTTP
// date) into a positive duration, or 0 when absent/unparseable. The result is
// capped at maxBackoff by Backoff.
func RetryAfter(response *http.Response) time.Duration {
	if response == nil {
		return 0
	}
	value := strings.TrimSpace(response.Header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}
