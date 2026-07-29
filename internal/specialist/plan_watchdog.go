package specialist

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/streamjson"
)

// The per-task stall watchdog.
//
// WHY IT EXISTS NOW. The plan's token budget became optional and unbounded by
// default, because it never bounded anything: it was checked only between
// tasks, so a six-task chain asking for 200k spent 469,555. That left
// max_wall_seconds — also optional, also model-supplied — as the only bound on
// a plan, and nothing at all bounding a SINGLE task. A child that wedges
// against an unresponsive provider hangs until the user notices.
//
// KEYED ON SILENCE, NOT DURATION. A task legitimately reading forty files for
// four minutes is working; a task that has emitted nothing for three is not.
// Killing the first would be worse than not having a watchdog — a timeout that
// fires on healthy work teaches users to raise it until it never fires. So the
// clock resets on every stream event the child emits, and only silence counts.
//
// It is a BOUND, not a retry policy. A stalled task is reported as stalled and
// the plan carries on with its dependents skipped, exactly as for any other
// failure. Retrying is a separate decision: a stall is usually a provider or
// network condition that a second identical attempt will hit again, and
// spending another task's worth of budget to find that out needs its own
// argument.

// defaultStallTimeout is how long a task may emit nothing before it is
// considered wedged. Deliberately generous: a slow model on a large file can be
// quiet for a long time, and a false positive here costs real work.
const defaultStallTimeout = 3 * time.Minute

// minStallTimeout floors a caller-supplied value. Below this the watchdog would
// fire on ordinary think-time and become a random task-killer.
const minStallTimeout = 30 * time.Second

// stallWatchdog cancels a task's context when its child goes quiet.
//
// One goroutine per task, started and stopped inside a single runner call, so
// it cannot outlive the task it watches — the failure mode the prototype's
// captured-context goroutine had.
type stallWatchdog struct {
	mu       sync.Mutex
	lastSeen time.Time
	fired    bool
	timeout  time.Duration
	now      func() time.Time
	// poll is how often the watcher checks. Zero means timeout/6. Injectable so
	// a test can exercise the real goroutine in milliseconds instead of
	// sleeping for the production interval — a watchdog whose only test is
	// "wait three minutes" does not get tested.
	poll time.Duration
}

// newStallWatchdog honours the timeout it is GIVEN. The floor lives in
// planBudget, where model-supplied input is validated; enforcing it a second
// time here would mean an internal caller could not construct a fast watchdog
// even when it has already been validated, which is how the tests ended up
// sleeping for the production interval.
func newStallWatchdog(timeout time.Duration, now func() time.Time) *stallWatchdog {
	if now == nil {
		now = time.Now
	}
	if timeout <= 0 {
		timeout = defaultStallTimeout
	}
	return &stallWatchdog{lastSeen: now(), timeout: timeout, now: now}
}

// touch records activity. Called on every stream event the child emits.
func (w *stallWatchdog) touch() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.lastSeen = w.now()
	w.mu.Unlock()
}

// stalledFor reports how long the child has been silent.
func (w *stallWatchdog) stalledFor() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now().Sub(w.lastSeen)
}

// didFire reports whether the watchdog cancelled the task, so the runner can
// tell a stall apart from an ordinary cancellation — the two produce the same
// context error and must not produce the same message.
func (w *stallWatchdog) didFire() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fired
}

func (w *stallWatchdog) markFired() {
	w.mu.Lock()
	w.fired = true
	w.mu.Unlock()
}

// watch runs until the task finishes, the parent is cancelled, or the child
// goes quiet for longer than the timeout — whichever comes first. It returns a
// stop function the caller MUST defer.
//
// The ticker interval is a fraction of the timeout rather than a fixed second:
// a three-minute watchdog polling every second is 180 wakeups to answer a
// question that changes slowly.
func (w *stallWatchdog) watch(ctx context.Context, cancel context.CancelFunc) func() {
	done := make(chan struct{})
	interval := w.poll
	if interval <= 0 {
		interval = w.timeout / 6
		if interval < time.Second {
			interval = time.Second
		}
	}

	go func() {
		// Every goroutine gets recover(): a panic in a watchdog must not take
		// down the run it exists to protect.
		defer func() { _ = recover() }()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if w.stalledFor() >= w.timeout {
					w.markFired()
					cancel()
					return
				}
			}
		}
	}()

	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// stallTimeoutFor resolves the timeout for a plan: the budget's own value when
// it set one, otherwise the default.
func stallTimeoutFor(budget Budget) time.Duration {
	if budget.MaxStall > 0 {
		return budget.MaxStall
	}
	return defaultStallTimeout
}

// watchedProgress wraps a task's progress callback so every event the child
// emits resets the stall clock. Returns a callback even when the caller wired
// none — the watchdog needs the liveness signal whether or not a UI wants it.
func watchedProgress(watchdog *stallWatchdog, forward func(streamjson.Event)) func(streamjson.Event) {
	return func(event streamjson.Event) {
		watchdog.touch()
		if forward != nil {
			forward(event)
		}
	}
}

// stallError is the failure a wedged task reports. Distinct wording from a
// cancellation, because a user who stopped a plan and a plan that hung are
// looking at very different problems.
func stallError(taskID string, timeout time.Duration) error {
	return fmt.Errorf("task %q produced no output for %s and was stopped; "+
		"raise budget.max_stall_seconds if this task is legitimately slow", taskID, timeout)
}
