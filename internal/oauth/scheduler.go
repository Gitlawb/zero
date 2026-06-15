package oauth

import (
	"context"
	"sync"
	"time"
)

// RefreshScheduler proactively refreshes a stored provider token shortly before
// it expires (mirrors token-refresh.js createTokenRefreshScheduler). It is an
// OPTIMIZATION over the on-demand GetFresh path, never the source of truth: a
// failed scheduled refresh is non-fatal and simply retried at the next expiry.
// It is a no-op for a token with no refresh token or no expiry.
type RefreshScheduler struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

// NewRefreshScheduler returns an idle scheduler.
func NewRefreshScheduler() *RefreshScheduler {
	return &RefreshScheduler{}
}

// Start begins proactively refreshing key via the manager until the context is
// canceled or Stop is called. It returns immediately; refresh happens in a
// goroutine. Calling Start twice is a no-op after the first.
func (s *RefreshScheduler) Start(ctx context.Context, m *Manager, key string) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	s.started = true
	done := s.done
	s.mu.Unlock()

	go s.loop(runCtx, m, key, done)
}

func (s *RefreshScheduler) loop(ctx context.Context, m *Manager, key string, done chan struct{}) {
	defer close(done)
	for {
		token, _, err := m.loadForKey(key)
		// No token, no refresh token, or no expiry => nothing to schedule.
		if err != nil || token.RefreshToken == "" || token.ExpiresAt.IsZero() {
			return
		}
		delay := s.delayUntilRefresh(token.ExpiresAt, m.buffer, m.now())
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		// Best-effort refresh; failure is non-fatal — reload and reschedule (the
		// on-demand GetFresh remains the safety net for callers).
		if _, err := m.GetFresh(ctx, key); err != nil {
			// Wait a bounded backoff before retrying so a persistently failing
			// refresh does not hot-loop.
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.buffer):
			}
		}
	}
}

// delayUntilRefresh computes how long to wait before refreshing: aim for buffer
// before expiry, clamped to >= 0, plus a small deterministic jitter so many
// sessions do not refresh in lockstep.
func (s *RefreshScheduler) delayUntilRefresh(expiresAt time.Time, buffer time.Duration, now time.Time) time.Duration {
	target := expiresAt.Add(-buffer)
	delay := target.Sub(now)
	if delay < 0 {
		delay = 0
	}
	// Jitter up to ~10% of the buffer, derived from the expiry nanos (no RNG dep).
	if buffer > 0 {
		jitter := time.Duration(expiresAt.UnixNano()%int64(buffer/10+1)) % (buffer/10 + 1)
		delay += jitter
	}
	return delay
}

// Stop cancels the scheduler and waits for its goroutine to exit. Safe to call
// more than once and on a never-started scheduler.
func (s *RefreshScheduler) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
