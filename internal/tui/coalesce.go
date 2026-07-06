package tui

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// streamCoalesceInterval is roughly one 60fps frame. Assistant-text deltas that
// arrive within this window are merged into a single agentTextMsg, so the render
// rate decouples from the token rate: a fast local model (100+ tok/s) no longer
// forces 100+ full Update→View cycles (each re-parsing the growing markdown) per
// second. Rendering stays smooth regardless of provider speed.
const streamCoalesceInterval = 16 * time.Millisecond

// textCoalescer batches agentTextMsg deltas before forwarding them to the Bubble
// Tea program. Any OTHER message flushes the pending text first, so ordering
// between streamed prose and tool-call / reasoning / row / usage messages is
// preserved. The turn's final agentResponseMsg does not pass through here (it is
// a tea.Cmd return, not a sink message), but the model drops deltas whose runID
// is no longer active, so a flush that races just past end-of-turn is harmless.
//
// Sink messages originate from the single agent goroutine and so arrive
// serially; the only concurrent caller is the flush timer. The mutex guards the
// buffer/timer AND is held across the downstream forward, so a timer-fired text
// flush can never overtake a concurrent non-text message: whoever holds the lock
// drains and forwards atomically, and the other caller blocks until it is done.
type textCoalescer struct {
	forward func(tea.Msg) // downstream sink (external sink + program.Send)
	// afterFunc schedules fn to run after one frame interval and returns a
	// stoppable timer. Defaults to a real time.AfterFunc(streamCoalesceInterval, …);
	// tests swap in a controllable timer so flush timing is deterministic instead of
	// racing the 16ms wall clock.
	afterFunc func(fn func()) coalesceTimer

	mu    sync.Mutex
	buf   []byte
	runID int
	timer coalesceTimer
}

// coalesceTimer is the subset of *time.Timer the coalescer needs. Abstracted
// behind afterFunc so a test can substitute a timer it controls.
type coalesceTimer interface {
	Stop() bool
}

func newTextCoalescer(forward func(tea.Msg)) *textCoalescer {
	return &textCoalescer{
		forward: forward,
		afterFunc: func(fn func()) coalesceTimer {
			return time.AfterFunc(streamCoalesceInterval, fn)
		},
	}
}

// send is the coalescing entry point installed as the RuntimeMessageSink.
func (c *textCoalescer) send(msg tea.Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()

	text, ok := msg.(agentTextMsg)
	if !ok {
		// Non-text message: flush buffered text first (preserving order), then
		// forward it — both under the lock so nothing can interleave between them.
		c.drainAndForwardLocked()
		c.forward(msg)
		return
	}

	// A delta for a different run than the one buffered: flush the old run's text
	// before buffering the new run's. In practice runs are sequential (the prior
	// run's end already flushed via a non-text message), so this is belt-and-braces.
	if len(c.buf) > 0 && text.runID != c.runID {
		c.drainAndForwardLocked()
	}
	c.runID = text.runID
	c.buf = append(c.buf, text.delta...)
	if c.timer == nil {
		c.timer = c.afterFunc(c.flush)
	}
}

// flush forwards any buffered text as one agentTextMsg. Runs on the timer
// goroutine; the lock it takes serializes it against send so its output can't be
// reordered around a concurrent non-text message.
func (c *textCoalescer) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drainAndForwardLocked()
}

// drainAndForwardLocked forwards any buffered text as one agentTextMsg and stops
// the timer, all while the caller holds c.mu — so a text flush and any non-text
// forward are strictly ordered and never interleave. A no-op when nothing is
// buffered. string(c.buf) copies, so reusing the backing array via [:0] is safe.
func (c *textCoalescer) drainAndForwardLocked() {
	if len(c.buf) == 0 {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	msg := agentTextMsg{runID: c.runID, delta: string(c.buf)}
	c.buf = c.buf[:0]
	c.forward(msg)
}
