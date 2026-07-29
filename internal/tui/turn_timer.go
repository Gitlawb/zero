package tui

import (
	"sync"
	"time"
)

// activeTurnTimer measures run time while excluding intervals where execution
// is paused waiting for a user decision. It is shared by the Bubble Tea model
// and the agent command, so access must remain safe across their goroutines.
type activeTurnTimer struct {
	mu        sync.Mutex
	startedAt time.Time
	pausedAt  time.Time
	pausedFor time.Duration
}

func newActiveTurnTimer(startedAt time.Time) *activeTurnTimer {
	return &activeTurnTimer{startedAt: startedAt}
}

func (timer *activeTurnTimer) start(startedAt time.Time) {
	if timer == nil {
		return
	}
	timer.mu.Lock()
	defer timer.mu.Unlock()
	timer.startedAt = startedAt
	timer.pausedAt = time.Time{}
	timer.pausedFor = 0
}

func (timer *activeTurnTimer) pause(at time.Time) {
	if timer == nil {
		return
	}
	timer.mu.Lock()
	defer timer.mu.Unlock()
	if timer.startedAt.IsZero() || !timer.pausedAt.IsZero() {
		return
	}
	timer.pausedAt = at
}

func (timer *activeTurnTimer) resume(at time.Time) {
	if timer == nil {
		return
	}
	timer.mu.Lock()
	defer timer.mu.Unlock()
	if timer.pausedAt.IsZero() {
		return
	}
	if at.After(timer.pausedAt) {
		timer.pausedFor += at.Sub(timer.pausedAt)
	}
	timer.pausedAt = time.Time{}
}

func (timer *activeTurnTimer) elapsed(at time.Time) time.Duration {
	if timer == nil {
		return 0
	}
	timer.mu.Lock()
	defer timer.mu.Unlock()
	if timer.startedAt.IsZero() {
		return 0
	}
	end := at
	if !timer.pausedAt.IsZero() && timer.pausedAt.Before(end) {
		end = timer.pausedAt
	}
	elapsed := end.Sub(timer.startedAt) - timer.pausedFor
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func (m model) activeTurnElapsed(fallbackStartedAt time.Time) time.Duration {
	return m.activeTurnElapsedAt(fallbackStartedAt, m.now())
}

func (m model) activeTurnElapsedAt(fallbackStartedAt, at time.Time) time.Duration {
	if m.turnTimer != nil {
		return m.turnTimer.elapsed(at)
	}
	elapsed := at.Sub(fallbackStartedAt)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
