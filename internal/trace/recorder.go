package trace

import (
	"sort"
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
//
// Spans are stored as occurrences (one entry per stamp) with their wall
// interval. Parent/child nesting and exclusive time are derived at Finish by
// interval containment, so concurrent (provider_connect inside generation) and
// nested (permission_wait inside tool_execution) spans are not double-counted.
type Recorder struct {
	mu                  sync.Mutex
	tr                  TurnTrace
	cursor              time.Time // synthesis cursor for RecordSpan (no real interval)
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

// End commits the span's wall interval to the recorder. Each stamp is its own
// occurrence (spans are not merged by name); exclusive time is derived at
// Finish from the recorded intervals.
func (s *SpanHandle) End() {
	if s == nil || s.recorder == nil {
		return
	}
	s.once.Do(func() {
		s.recorder.addOccurrence(s.name, s.start, time.Now())
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

// RecordSpan commits an already-measured duration to name as a synthesized
// sequential occurrence. Use this when the caller already holds a duration
// (for example, in tests) rather than a live interval; production code uses
// Span/End, which record the real wall interval. Synthesized occurrences
// chain from the recorder's StartedAt (or a moving cursor) so coverage and
// exclusive math remain consistent. Each stamp is its own entry.
func (r *Recorder) RecordSpan(name string, d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	start := r.cursor
	if start.IsZero() {
		start = r.tr.StartedAt
	}
	if start.IsZero() {
		start = time.Now()
	}
	end := start.Add(d)
	r.cursor = end
	r.tr.Spans = append(r.tr.Spans, Span{Name: name, Start: start, End: end, Duration: d})
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

// EmitPrefixHash records one prompt-prefix fingerprint on the trace. Multiple
// calls are allowed within a run (one per turn, typically) and accumulate in
// order. The first call after Finish is a no-op; later calls are also no-ops
// because the trace has been sealed.
func (r *Recorder) EmitPrefixHash(p PrefixHash) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.tr.PrefixHashes = append(r.tr.PrefixHashes, p)
}

// EmitOutputBudget records one content-free output budgeting decision. Calls
// are retained in transcript emission order, including when the underlying
// tools executed concurrently.
func (r *Recorder) EmitOutputBudget(event OutputBudgetEvent) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.tr.OutputBudgets = append(r.tr.OutputBudgets, event)
}

// Finish stamps CompletedAt, derives each span's parent (by interval
// containment) and exclusive time, and returns a snapshot of the trace. Calling
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
		deriveNesting(r.tr.Spans)
	}
	snap := r.tr
	// Copy the slices so callers cannot mutate the recorder's state.
	snap.Spans = append([]Span(nil), r.tr.Spans...)
	snap.Counters = append([]Counter(nil), r.tr.Counters...)
	snap.PrefixHashes = append([]PrefixHash(nil), r.tr.PrefixHashes...)
	snap.OutputBudgets = append([]OutputBudgetEvent(nil), r.tr.OutputBudgets...)
	return &snap
}

// addOccurrence appends a span occurrence with its wall interval.
func (r *Recorder) addOccurrence(name string, start, end time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	d := end.Sub(start)
	if d < 0 {
		d = 0
	}
	r.tr.Spans = append(r.tr.Spans, Span{Name: name, Start: start, End: end, Duration: d})
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

// interval is a half-open [start, end) wall window used for containment and
// coverage math.
type interval struct{ start, end time.Time }

// deriveNesting computes each span's parent (the tightest span whose interval
// contains it) and its exclusive time (duration minus the union of its direct
// children's intervals). Spans without a usable interval are left top-level
// with exclusive = duration. This is O(n²) in the span count, which is tiny
// (a handful to a few dozen per run), and runs once at Finish.
func deriveNesting(spans []Span) {
	parent := make([]int, len(spans))
	for i := range parent {
		parent[i] = -1
	}
	for i, a := range spans {
		if a.Start.IsZero() || a.End.IsZero() {
			continue
		}
		bestIdx := -1
		bestDur := time.Duration(0)
		for j, b := range spans {
			if i == j || b.Start.IsZero() || b.End.IsZero() {
				continue
			}
			// b contains a when a starts at/after b and ends at/before b.
			contained := !a.Start.Before(b.Start) && !a.End.After(b.End)
			if !contained {
				continue
			}
			// Strict containment makes b an unambiguous parent of a. When the
			// two intervals are identical (contained but not strictly — equality
			// satisfies the check symmetrically), they would otherwise pick
			// each other as parent and form a 2-cycle that drops both from
			// top-level. Break that tie deterministically: only the
			// lower-indexed span may parent a co-extensive higher-indexed one,
			// so one stays top-level and the other nests under it.
			strict := a.Start.After(b.Start) || a.End.Before(b.End)
			if !strict && j >= i {
				continue
			}
			bd := b.End.Sub(b.Start)
			if bestIdx == -1 || bd < bestDur {
				bestIdx = j
				bestDur = bd
			}
		}
		parent[i] = bestIdx
	}
	for i := range spans {
		if spans[i].Start.IsZero() || spans[i].End.IsZero() {
			spans[i].Exclusive = spans[i].Duration
			continue
		}
		var children []interval
		for j := range spans {
			if parent[j] == i {
				children = append(children, interval{spans[j].Start, spans[j].End})
			}
		}
		exclusive := spans[i].Duration - unionOf(children)
		if exclusive < 0 {
			exclusive = 0
		}
		spans[i].Exclusive = exclusive
		if parent[i] != -1 {
			spans[i].Parent = spans[parent[i]].Name
		}
	}
}

// unionIntervalDuration returns the total wall time covered by the union of
// all span intervals (used for Coverage). Spans without a usable interval are
// skipped.
func unionIntervalDuration(spans []Span) time.Duration {
	var intervals []interval
	for _, s := range spans {
		if s.Start.IsZero() || s.End.IsZero() || s.End.Before(s.Start) {
			continue
		}
		intervals = append(intervals, interval{s.Start, s.End})
	}
	return unionOf(intervals)
}

// unionOf returns the total duration covered by the union of the intervals.
func unionOf(intervals []interval) time.Duration {
	if len(intervals) == 0 {
		return 0
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	var total time.Duration
	cur := intervals[0]
	for _, iv := range intervals[1:] {
		if !iv.start.After(cur.end) {
			// overlap or touch: extend the current window.
			if iv.end.After(cur.end) {
				cur.end = iv.end
			}
			continue
		}
		total += cur.end.Sub(cur.start)
		cur = iv
	}
	total += cur.end.Sub(cur.start)
	return total
}
