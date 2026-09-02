package tui

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// isClearScreenCmd reports whether c is the tea.ClearScreen command by
// function-pointer identity. That avoids evaluating the command (and any
// sibling commands in a batch) just to detect a clear.
func isClearScreenCmd(c tea.Cmd) bool {
	return c != nil && reflect.ValueOf(c).Pointer() == reflect.ValueOf(tea.ClearScreen).Pointer()
}

// cmdIncludesClearScreen looks for a tea.ClearScreen among cmd and, if cmd
// is a tea.Batch wrapper, among its children. Batch wrappers return a
// BatchMsg without running their children; Tick (and similar) commands block,
// so those are only expanded with a short timeout and treated as "not a
// clear" if they don't return promptly. Nil commands are "not found".
func cmdIncludesClearScreen(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	if isClearScreenCmd(cmd) {
		return true
	}
	// Expand batches (and any other immediately-returning cmds). Bound the
	// wait so a deferred stream-clear Tick is never a multi-100ms sleep.
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msgIncludesClearScreen(msg)
	case <-time.After(20 * time.Millisecond):
		return false
	}
}

func msgIncludesClearScreen(msg tea.Msg) bool {
	if msg == nil {
		return false
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if cmdIncludesClearScreen(c) {
				return true
			}
		}
		return false
	}
	// clearScreenMsg is unexported; match by value equality with ClearScreen().
	return msg == tea.ClearScreen()
}

// withFrozenClock pins m.now to t0 for deterministic throttle math.
func withFrozenClock(m *model, t0 time.Time) {
	m.now = func() time.Time { return t0 }
}

// TestStreamClearThrottledNewlineIsDeferredNotDropped guards against a
// regression in the multipass/Windows-Terminal ghost-caret workaround: a
// newline arriving inside the stream-clear throttle window used to have its
// redraw silently dropped. If that throttled newline turned out to be the
// turn's last delta, nothing later would ever repair the ghost caret. The
// throttled newline must instead be remembered and flushed, here at stream
// end, rather than discarded.
func TestStreamClearThrottledNewlineIsDeferredNotDropped(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m = m.beginRun(nil)
	rid := m.activeRunID
	t0 := time.Unix(1_700_000_000, 0)
	withFrozenClock(&m, t0)

	// lastStreamClear is still the zero Time here, so it's far outside the
	// throttle window and the first newline fires an immediate ClearScreen.
	updated, cmd := m.Update(agentTextMsg{runID: rid, delta: "first line\n"})
	m = updated.(model)
	if !cmdIncludesClearScreen(cmd) {
		t.Fatal("first newline after a long gap should fire an immediate ClearScreen")
	}
	if m.pendingStreamClear {
		t.Fatal("an immediate clear should not also leave a clear pending")
	}
	if !m.lastStreamClear.Equal(t0) {
		t.Fatalf("lastStreamClear should record m.now()=%v, got %v", t0, m.lastStreamClear)
	}

	// A second newline lands inside the 100ms throttle window: it must not
	// fire its own ClearScreen, but it must be remembered as owed.
	updated, cmd = m.Update(agentTextMsg{runID: rid, delta: "second line\n"})
	m = updated.(model)
	if cmdIncludesClearScreen(cmd) {
		t.Fatal("a throttled newline should not fire its own ClearScreen")
	}
	if !m.pendingStreamClear {
		t.Fatal("a throttled newline must mark a clear as pending instead of dropping it")
	}

	// This was, in fact, the turn's last delta — stream end must flush the
	// pending clear rather than losing it.
	updated, cmd = m.Update(agentResponseMsg{runID: rid})
	m = updated.(model)
	if !cmdIncludesClearScreen(cmd) {
		t.Fatal("stream end must flush a pending stream-clear rather than dropping it")
	}
	if m.pendingStreamClear {
		t.Fatal("stream end should clear the pending flag once it's flushed")
	}
}

// TestStreamClearTwoNewlinesWithinThrottleWindow is the explicit two-newline
// regression for the rate-limit path: both deltas arrive inside the same
// streamClearThrottle window after an initial clear, so neither should fire
// an immediate ClearScreen, and the deferred pending flag must stay set
// (coalesced) until a later flush.
func TestStreamClearTwoNewlinesWithinThrottleWindow(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m = m.beginRun(nil)
	rid := m.activeRunID
	t0 := time.Unix(1_700_000_000, 0)
	withFrozenClock(&m, t0)

	updated, cmd := m.Update(agentTextMsg{runID: rid, delta: "line one\n"})
	m = updated.(model)
	if !cmdIncludesClearScreen(cmd) {
		t.Fatal("setup: first newline should clear immediately")
	}

	// Stay frozen at t0 so both follow-ups fall inside the throttle window.
	updated, cmd = m.Update(agentTextMsg{runID: rid, delta: "line two\n"})
	m = updated.(model)
	if cmdIncludesClearScreen(cmd) {
		t.Fatal("second newline inside the window must not ClearScreen immediately")
	}
	if !m.pendingStreamClear {
		t.Fatal("second newline inside the window must set pendingStreamClear")
	}

	// A third newline still inside the window must coalesce onto the same
	// pending clear rather than dropping it or firing early.
	updated, cmd = m.Update(agentTextMsg{runID: rid, delta: "line three\n"})
	m = updated.(model)
	if cmdIncludesClearScreen(cmd) {
		t.Fatal("third newline inside the window must not ClearScreen immediately")
	}
	if !m.pendingStreamClear {
		t.Fatal("pendingStreamClear must remain set across coalesced newlines")
	}

	// Advance past the throttle window and let the scheduled flush repair it.
	withFrozenClock(&m, t0.Add(streamClearThrottle))
	updated, cmd = m.Update(streamClearFlushMsg{})
	m = updated.(model)
	if !cmdIncludesClearScreen(cmd) {
		t.Fatal("flush after the window must fire ClearScreen for coalesced newlines")
	}
	if m.pendingStreamClear {
		t.Fatal("flush should clear the pending flag")
	}
}

// TestStreamClearScheduledFlushRepairsGhostCaretMidStream guards the
// mid-stream flush path: once the throttle window has elapsed, the one-shot
// timer scheduled alongside the pending clear (see scheduleStreamClearFlush)
// repairs a throttled newline's redraw instead of waiting for stream end,
// which may be much later for a long-running turn. This has to be
// independent of the streaming-text fade tick: fadeDisabled is hardcoded true
// in newModel, so that ticker never actually runs.
func TestStreamClearScheduledFlushRepairsGhostCaretMidStream(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m = m.beginRun(nil)
	rid := m.activeRunID
	t0 := time.Unix(1_700_000_000, 0)
	withFrozenClock(&m, t0)

	updated, _ := m.Update(agentTextMsg{runID: rid, delta: "first line\n"})
	m = updated.(model)
	updated, _ = m.Update(agentTextMsg{runID: rid, delta: "second line\n"})
	m = updated.(model)
	if !m.pendingStreamClear {
		t.Fatal("setup: expected the second newline to be throttled and pending")
	}

	// Move past the throttle window via the mockable clock, without a sleep.
	withFrozenClock(&m, t0.Add(time.Second))

	updated, cmd := m.Update(streamClearFlushMsg{})
	m = updated.(model)
	if !cmdIncludesClearScreen(cmd) {
		t.Fatal("the scheduled flush should fire ClearScreen once the throttle window has elapsed")
	}
	if m.pendingStreamClear {
		t.Fatal("the scheduled flush should clear the pending flag once it's flushed")
	}
}

// TestStreamClearScheduledFlushNoopsWhenNothingPending guards against a stale
// timer re-firing a ClearScreen after its pending clear was already flushed
// by something else (e.g. stream end), or when nothing was ever pending.
func TestStreamClearScheduledFlushNoopsWhenNothingPending(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m = m.beginRun(nil)

	updated, cmd := m.Update(streamClearFlushMsg{})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("a flush with nothing pending should be a no-op")
	}
	if m.pendingStreamClear {
		t.Fatal("a no-op flush should not set the pending flag")
	}
}
