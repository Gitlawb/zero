package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// cmdIncludesClearScreen runs cmd (and, transitively, every command inside a
// tea.BatchMsg it produces) looking for a tea.ClearScreen. nil commands and
// nil messages are treated as "not found" rather than a crash so callers can
// pass a cmd straight off an Update return without a nil check.
func cmdIncludesClearScreen(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return msgIncludesClearScreen(cmd())
}

func msgIncludesClearScreen(msg tea.Msg) bool {
	if msg == nil {
		return false
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if msgIncludesClearScreen(c()) {
				return true
			}
		}
		return false
	}
	return msg == tea.ClearScreen()
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

	updated, _ := m.Update(agentTextMsg{runID: rid, delta: "first line\n"})
	m = updated.(model)
	updated, _ = m.Update(agentTextMsg{runID: rid, delta: "second line\n"})
	m = updated.(model)
	if !m.pendingStreamClear {
		t.Fatal("setup: expected the second newline to be throttled and pending")
	}

	// Move lastStreamClear far enough into the past that the throttle
	// window has elapsed, without an actual sleep.
	m.lastStreamClear = time.Now().Add(-time.Second)

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
