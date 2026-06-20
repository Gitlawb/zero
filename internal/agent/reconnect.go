package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Mid-stream reconnect: a long autonomous task (a big refactor, a swarm member,
// a headless/cron run) should survive a single transient upstream hiccup
// instead of dying and re-burning every token on a restart. When the initial
// StreamCompletion connect fails with a disconnect-shaped error — before any
// content has been forwarded — re-issue the same request with backoff a few
// times. We retry ONLY the connect (not a partially-consumed stream), so no
// already-forwarded OnText is ever duplicated.
const (
	maxStreamReconnects = 2
	streamReconnectBase = 500 * time.Millisecond
)

// reconnectNotifier is called before each retry with the 1-based attempt number
// and the max, so the caller can surface a "Reconnecting N/max" notice. Nil is
// fine.
type reconnectNotifier func(attempt, max int)

// reconnectNoticeFor builds a notifier that surfaces reconnect attempts through
// OnReasoning — a non-content channel that is never folded into the answer
// text, so the user sees "Reconnecting…" without corrupting streamed output.
// Returns nil when there is no reasoning sink (the reconnect still happens
// silently).
func reconnectNoticeFor(options Options) reconnectNotifier {
	if options.OnReasoning == nil {
		return nil
	}
	return func(attempt, max int) {
		options.OnReasoning(fmt.Sprintf("\n[connection lost — reconnecting %d/%d…]\n", attempt, max))
	}
}

// streamWithReconnect issues request via provider.StreamCompletion and, on a
// transient disconnect error, retries the connect up to maxStreamReconnects
// times with exponential backoff. It returns the live stream on success, or the
// last error. A context-cancellation, a non-disconnect error, or a context
// already past its deadline is returned immediately (no retry) — those have
// their own handling (compaction for context-limit, image-rejection, etc.).
func streamWithReconnect(ctx context.Context, provider Provider, request zeroruntime.CompletionRequest, notify reconnectNotifier) (<-chan zeroruntime.StreamEvent, error) {
	stream, err := provider.StreamCompletion(ctx, request)
	if err == nil {
		return stream, nil
	}
	for attempt := 1; attempt <= maxStreamReconnects; attempt++ {
		if !shouldReconnect(ctx, err) {
			return nil, err
		}
		if notify != nil {
			notify(attempt, maxStreamReconnects)
		}
		if waitErr := sleepWithContext(ctx, backoffFor(attempt)); waitErr != nil {
			return nil, err // ctx cancelled while waiting; surface the original error
		}
		stream, err = provider.StreamCompletion(ctx, request)
		if err == nil {
			return stream, nil
		}
	}
	return nil, err
}

// shouldReconnect reports whether err is a transient disconnect worth retrying.
// It excludes context cancellation/expiry (caller is shutting down) and
// context-limit errors (the compactor recovers those), so the reconnect path
// never fights the existing handlers.
func shouldReconnect(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if isContextLimitError(msg) || isImageRejectionError(err) {
		return false
	}
	for _, needle := range []string{
		"eof",
		"connection reset",
		"connection refused",
		"broken pipe",
		"connection closed",
		"timeout",
		"timed out",
		"temporarily unavailable",
		"i/o timeout",
		"503",
		"502",
		"server closed",
		"unexpected end",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func backoffFor(attempt int) time.Duration {
	d := streamReconnectBase
	for i := 1; i < attempt; i++ {
		d *= 2
	}
	return d
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
