package providerio

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/trace"
)

// Transient-failure retry, shared by every provider.
//
// SendWithRetry centralizes one retry policy so all providers behave consistently
// (previously only the OpenAI provider retried; Anthropic and Gemini surfaced the
// first failure).
//
// What is retried: ONLY 429 (rate limit) and 503 (service unavailable) — the
// statuses where the server explicitly did NOT accept the request, so replaying
// it cannot duplicate work. Other 5xx (500/502/504) and transport/network errors
// are NOT retried: a completion POST is non-idempotent and may have reached and
// been processed by the server, so replaying it could duplicate
// (billable) work. Only the INITIAL request is ever in scope; once the response
// body starts streaming it is never re-issued.

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
			// A transport failure on a POST does NOT mean the server didn't receive
			// it — the request may have arrived and be generating a (billable,
			// non-idempotent) completion while only the response/connection failed.
			// Replaying it could duplicate that work, so surface the error instead
			// of retrying. Only 429/503 responses (below), where we KNOW the request
			// was not accepted, are safe to retry.
			if ctx.Err() != nil {
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
