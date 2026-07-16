package trace

import (
	"sync"
	"time"
)

// Recorder is the in-process handle one agent.Run stamps spans and counters
// into. It is concurrency-safe: parallel tool execution, provider reconnects,
// and async streaming stamp concurrently from different goroutines.
//
// A nil *Recorder is valid to call: the no-op helpers below route every
// stamp through a nil check so callers can write `options.Trace.Start()`
// unconditionally and pay nothing when tracing is off.
type Recorder struct {
	mu                  sync.Mutex
	tr                  TurnTrace
	started             bool
	finished            bool
	firstTokenStamped   bool
	firstVisibleStamped bool
	firstActionStamped  bool
}

// NewRecorder returns a ready recorder. sessionID correlates with the agent
// session (Options.SessionID); runID is a per-Run sequence; profile is an
// optional label (e.g. "cold", "warm") for benchmark runs.
func NewRecorder(sessionID, runID, profile string) *Recorder {
	return &Recorder{tr: TurnTrace{
		SessionID: sessionID,
		RunID:     runID,
		Profile:   profile,
	}}
}

// Start stamps StartedAt. Safe to call at most once; idempotent on repeat.
func (r *Recorder) Start() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	r.started = true
	r.tr.StartedAt = time.Now()
}

// SpanHandle is a live timing span. Call End exactly once to commit it; a
// second End is a no-op. Not calling End leaks the span (it is dropped).
type SpanHandle struct {
	recorder *Recorder
	name     string
	start    time.Time
	once     sync.Once
}

// End commits the span's elapsed duration to the recorder. Durations
// accumulate by name so repeated stamps of the same span add together.
func (s *SpanHandle) End() {
	if s == nil || s.recorder == nil {
		return
	}
	s.once.Do(func() {
		s.recorder.addSpan(s.name, time.Since(s.start))
	})
}

// Span begins a named span and returns a handle. Caller is responsible for
// calling End when the span completes. Example:
//
//	span := r.Span(trace.SpanGeneration)
//	defer span.End()
func (r *Recorder) Span(name string) *SpanHandle {
	if r == nil {
		return nil
	}
	return &SpanHandle{recorder: r, name: name, start: time.Now()}
}

// RecordSpan commits an already-measured duration to name, accumulating with
// any prior value. Use this when the caller already holds a start time (for
// example, a span that began before the recorder existed).
func (r *Recorder) RecordSpan(name string, d time.Duration) {
	if r == nil {
		return
	}
	r.addSpan(name, d)
}

// Counter adds n to the named counter, accumulating across calls.
func (r *Recorder) Counter(name string, n int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.addCounterLocked(name, n)
}

// StampFirstToken records the time of the first output token. Only the first
// call wins; later calls are no-ops.
func (r *Recorder) StampFirstToken() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || r.firstTokenStamped {
		return
	}
	r.firstTokenStamped = true
	r.tr.FirstTokenAt = time.Now()
}

// StampFirstVisibleEvent records the first event visible to the user. First
// call wins.
func (r *Recorder) StampFirstVisibleEvent() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || r.firstVisibleStamped {
		return
	}
	r.firstVisibleStamped = true
	r.tr.FirstVisibleEventAt = time.Now()
}

// StampFirstUsefulAction records the first tool call or substantive action.
// First call wins.
func (r *Recorder) StampFirstUsefulAction() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || r.firstActionStamped {
		return
	}
	r.firstActionStamped = true
	r.tr.FirstUsefulActionAt = time.Now()
}

// Finish stamps CompletedAt and returns a snapshot of the trace. Calling
// Finish more than once returns the same snapshot.
func (r *Recorder) Finish() *TurnTrace {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.finished {
		r.finished = true
		r.tr.CompletedAt = time.Now()
	}
	snap := r.tr
	// Copy the slices so callers cannot mutate the recorder's state.
	snap.Spans = append([]Span(nil), r.tr.Spans...)
	snap.Counters = append([]Counter(nil), r.tr.Counters...)
	return &snap
}

// addSpan accumulates d into the named span, merging with an existing entry.
func (r *Recorder) addSpan(name string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	for i := range r.tr.Spans {
		if r.tr.Spans[i].Name == name {
			r.tr.Spans[i].Duration += d
			return
		}
	}
	r.tr.Spans = append(r.tr.Spans, Span{Name: name, Duration: d})
}

func (r *Recorder) addCounterLocked(name string, n int64) {
	for i := range r.tr.Counters {
		if r.tr.Counters[i].Name == name {
			r.tr.Counters[i].Value += n
			return
		}
	}
	r.tr.Counters = append(r.tr.Counters, Counter{Name: name, Value: n})
}
