package agent

import (
	"context"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// maxNetworkRetries is how many times a transient network failure that produced
// NO output is transparently retried before the turn gives up. Three total
// attempts (initial + 2 retries) covers a brief blip without stalling for long.
const maxNetworkRetries = 2

// transientNetworkSignals are substrings of error messages that indicate a
// connection-level failure worth retrying — the request never reached the model
// or got no response, so re-sending the SAME request is safe and may succeed.
// Matched case-insensitively against the provider's error string.
var transientNetworkSignals = []string{
	"tls handshake timeout",
	"i/o timeout",
	"dial tcp",
	"connection reset",
	"connection refused",
	"broken pipe",
	"network is unreachable",
	"no route to host",
	"unexpected eof",
	"server misbehaving",                   // transient resolver failure
	"temporary failure in name resolution", // transient DNS
}

// isTransientNetworkError reports whether a provider error string looks like a
// retryable connection-level failure. A user cancellation, a context deadline,
// or any HTTP/content error (auth, rate limit, context-length) returns false so
// only genuine network blips are retried.
func isTransientNetworkError(reason string) bool {
	lower := strings.ToLower(reason)
	// Never retry a cancellation or a deadline — those are intentional/terminal.
	if strings.Contains(lower, "context canceled") || strings.Contains(lower, "context deadline exceeded") {
		return false
	}
	// Never retry an explicit HTTP status error (auth, rate limit, bad request,
	// context length, etc.) — re-sending won't help.
	if strings.Contains(lower, "status code") || strings.Contains(lower, "status:") ||
		strings.Contains(lower, "http 4") || strings.Contains(lower, "http 5") {
		return false
	}
	for _, signal := range transientNetworkSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

// streamProducedNoOutput reports whether a collected stream surfaced nothing the
// user would see: no text, no tool calls, no reasoning blocks, and no dropped
// tool-call signals. The no-output network retry only fires when this holds — a
// stream that already produced ANY of these must not be re-sent, since
// re-collecting it would duplicate already-surfaced output and contradict the
// "no output" retry contract.
func streamProducedNoOutput(c zeroruntime.CollectedStream) bool {
	return c.Text == "" && len(c.ToolCalls) == 0 &&
		len(c.ReasoningBlocks) == 0 && c.DroppedToolCalls == 0
}

// networkRetryBackoff is the wait before the (0-based) attempt-th retry. It is a
// var so tests can zero it; production uses defaultNetworkRetryBackoff.
var networkRetryBackoff = defaultNetworkRetryBackoff

// defaultNetworkRetryBackoff waits exponentially — 1s, 2s, 4s — capped at 8s.
func defaultNetworkRetryBackoff(attempt int) time.Duration {
	d := time.Second << attempt
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

// annotateUnreachableProvider turns a bare network error (e.g. "TLS handshake
// timeout") into an actionable message once every retry has failed: it's a
// connectivity problem reaching the provider, not the model.
func annotateUnreachableProvider(reason string) string {
	return strings.TrimSpace(reason) +
		"\n\nCouldn't reach the provider after retrying — this is a network/connectivity problem, not the model. " +
		"Check your internet or VPN, or switch to a reachable provider with /provider."
}

// sleepWithContext waits for d, returning false if the context is cancelled
// first (so a backoff never outlives a cancelled run).
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
