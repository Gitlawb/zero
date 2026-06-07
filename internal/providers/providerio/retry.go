package providerio

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Transient-failure retry, shared by every provider.
//
// A streaming request can fail before any data arrives — a dropped connection, a
// hosted gateway returning an intermittent 5xx, or a 429 rate limit. Those are
// safe to retry because nothing has been streamed yet. SendWithRetry centralizes
// that policy so all providers behave consistently (previously only the OpenAI
// provider retried; Anthropic and Gemini surfaced the first failure).
//
// Only the INITIAL request is retried. Once the response body starts streaming
// it is never re-issued (a partially consumed stream can't be safely replayed).

const defaultMaxRetryAttempts = 3

// maxBackoff caps a single backoff wait so a hostile or buggy Retry-After can't
// stall the agent for minutes.
const maxBackoff = 30 * time.Second

// SendWithRetry issues an HTTP request with transient-failure retries: network
// errors and retryable statuses (429 and 5xx) are retried up to maxAttempts,
// backing off between tries and honoring a server Retry-After header and context
// cancellation. The request is rebuilt from body each attempt so it is
// replay-safe; setHeader (if non-nil) sets headers on every attempt.
//
// It returns the final *http.Response (which the caller inspects for a non-2xx
// status, exactly as before) or an error for a network failure / context
// cancellation. Retries exhausted on a 5xx return that 5xx response, not an
// error, so the caller's existing HTTP-error path still runs.
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

		response, err := client.Do(request)
		if err != nil {
			if attempt < maxAttempts && ctx.Err() == nil && Backoff(ctx, attempt, 0) {
				continue
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}

		if ShouldRetryStatus(response.StatusCode) && attempt < maxAttempts {
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

// ShouldRetryStatus reports whether an HTTP status is a transient failure worth
// retrying: 429 (rate limit) and any 5xx.
func ShouldRetryStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= http.StatusInternalServerError
}

// Backoff waits before retry attempt N (1-based), returning false if the context
// is cancelled during the wait. The wait is attempt*400ms unless the server
// supplied a (positive) Retry-After, and is capped at maxBackoff.
func Backoff(ctx context.Context, attempt int, retryAfter time.Duration) bool {
	wait := time.Duration(attempt) * 400 * time.Millisecond
	if retryAfter > 0 {
		wait = retryAfter
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
