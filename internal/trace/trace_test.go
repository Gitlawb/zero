package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agenteval"
)

func TestRecorderSpanAccumulates(t *testing.T) {
	r := NewRecorder("s1", "r1", "")
	r.Start()
	s := r.Span(SpanGeneration)
	time.Sleep(2 * time.Millisecond)
	s.End()
	s2 := r.Span(SpanGeneration)
	time.Sleep(1 * time.Millisecond)
	s2.End()

	tr := r.Finish()
	got := tr.Span(SpanGeneration)
	if got < 3*time.Millisecond {
		t.Fatalf("generation span did not accumulate across stamps; got %v", got)
	}
	if len(tr.Spans) != 1 {
		t.Fatalf("expected one generation span entry, got %d", len(tr.Spans))
	}
}

func TestRecorderCounters(t *testing.T) {
	r := NewRecorder("s1", "r1", "")
	r.Counter(CounterToolCalls, 1)
	r.Counter(CounterToolCalls, 2)
	r.Counter(CounterModelRequests, 3)

	tr := r.Finish()
	if got := tr.Counter(CounterToolCalls); got != 3 {
		t.Fatalf("tool_calls = %d, want 3", got)
	}
	if got := tr.Counter(CounterModelRequests); got != 3 {
		t.Fatalf("model_requests = %d, want 3", got)
	}
}

func TestRecorderFirstTokenOnce(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	r.StampFirstToken()
	first := r.Finish().FirstTokenAt
	r.StampFirstToken() // no-op after Finish; should not panic
	if r.Finish().FirstTokenAt != first {
		t.Fatal("StampFirstToken should not move the timestamp after the first stamp")
	}
}

func TestFinishSnapshotIsCopy(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Counter(CounterToolCalls, 5)
	tr := r.Finish()
	tr.Counters[0].Value = 999
	if got := r.Finish().Counter(CounterToolCalls); got != 5 {
		t.Fatalf("Finish snapshot must be a copy; mutating it changed recorder state to %d", got)
	}
}

func TestFinishFreezesState(t *testing.T) {
	// Once Finish returns a snapshot, the recorder is frozen: later stamps must
	// not mutate its state, so a second Finish yields the same trace.
	r := NewRecorder("s", "r", "")
	r.Start()
	r.Counter(CounterToolCalls, 2)
	r.RecordSpan(SpanGeneration, 5*time.Millisecond)
	r.StampFirstToken()
	first := r.Finish()

	// Post-finish stamps of every kind must be dropped.
	r.Counter(CounterToolCalls, 100)
	r.RecordSpan(SpanGeneration, 100*time.Millisecond)
	r.StampFirstToken()
	r.StampFirstVisibleEvent()
	r.StampFirstUsefulAction()
	s := r.Span(SpanToolExecution)
	time.Sleep(time.Millisecond)
	s.End()

	second := r.Finish()
	if got := second.Counter(CounterToolCalls); got != 2 {
		t.Fatalf("post-finish Counter leaked into snapshot: got %d, want 2", got)
	}
	if got := second.Span(SpanGeneration); got != 5*time.Millisecond {
		t.Fatalf("post-finish RecordSpan leaked into snapshot: got %v, want 5ms", got)
	}
	if got := second.Span(SpanToolExecution); got != 0 {
		t.Fatalf("post-finish Span leaked into snapshot: got %v, want 0", got)
	}
	if second.FirstTokenAt != first.FirstTokenAt {
		t.Fatalf("post-finish StampFirstToken moved timestamp")
	}
	if !second.FirstVisibleEventAt.IsZero() {
		t.Fatalf("post-finish StampFirstVisibleEvent leaked into snapshot")
	}
	if !second.FirstUsefulActionAt.IsZero() {
		t.Fatalf("post-finish StampFirstUsefulAction leaked into snapshot")
	}
}

func TestNilRecorderIsNoOp(t *testing.T) {
	var r *Recorder
	r.Start()
	r.Counter(CounterToolCalls, 1)
	r.StampFirstToken()
	r.StampFirstVisibleEvent()
	r.StampFirstUsefulAction()
	r.RecordSpan(SpanGeneration, time.Millisecond)
	s := r.Span(SpanGeneration)
	s.End()
	if tr := r.Finish(); tr != nil {
		t.Fatalf("nil recorder Finish should return nil, got %+v", tr)
	}
}

func TestRecorderConcurrent(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := r.Span(SpanToolExecution)
			time.Sleep(time.Millisecond)
			s.End()
			r.Counter(CounterToolCalls, 1)
			r.StampFirstToken()
		}()
	}
	wg.Wait()
	tr := r.Finish()
	if got := tr.Counter(CounterToolCalls); got != 50 {
		t.Fatalf("tool_calls = %d, want 50", got)
	}
	if got := tr.Span(SpanToolExecution); got <= 0 {
		t.Fatalf("tool_execution span empty after concurrent stamps; got %v", got)
	}
}

func TestContextRoundTrip(t *testing.T) {
	r := NewRecorder("s", "r", "")
	ctx := WithContext(context.Background(), r)
	if got := FromContext(ctx); got != r {
		t.Fatal("FromContext did not return the injected recorder")
	}
	if got := FromContext(context.Background()); got != nil {
		t.Fatalf("FromContext on a bare context should return nil, got %v", got)
	}
}

func TestContextNilRecorder(t *testing.T) {
	ctx := WithContext(context.Background(), nil)
	if got := FromContext(ctx); got != nil {
		t.Fatalf("FromContext should return nil for an injected nil recorder, got %v", got)
	}
}

func TestWriteNDJSONMatchesAgentevalContract(t *testing.T) {
	r := NewRecorder("s1", "r1", "cold")
	r.Start()
	r.RecordSpan(SpanPromptBuild, 10*time.Millisecond)
	r.RecordSpan(SpanGeneration, 50*time.Millisecond)
	r.RecordSpan(SpanToolExecution, 5*time.Millisecond)
	r.RecordSpan(SpanPermissionWait, 1*time.Millisecond)
	r.RecordSpan(SpanCompaction, 2*time.Millisecond)
	r.RecordSpan(SpanPersistence, 1*time.Millisecond)
	r.RecordSpan(SpanProviderConnect, 8*time.Millisecond)
	r.Counter(CounterModelRequests, 2)
	r.Counter(CounterToolCalls, 3)
	r.Counter(CounterInputTokens, 100)
	r.Counter(CounterOutputTokens, 40)
	tr := r.Finish()

	var buf bytes.Buffer
	if err := WriteNDJSON(&buf, tr); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	stdout := buf.String()

	missing := agenteval.MissingTraceEvents(RequiredEventKeys(), stdout)
	if len(missing) > 0 {
		t.Fatalf("NDJSON missing required event keys: %v\noutput:\n%s", missing, stdout)
	}

	keys := agenteval.ParseTraceEventKeys(stdout)
	want := map[string]bool{
		"trace:run":                       true,
		"span:" + SpanPromptBuild:         true,
		"span:" + SpanGeneration:          true,
		"span:" + SpanToolExecution:       true,
		"span:" + SpanProviderConnect:     true,
		"counter:" + CounterModelRequests: true,
		"counter:" + CounterToolCalls:     true,
		"counter:" + CounterInputTokens:   true,
		"counter:" + CounterOutputTokens:  true,
	}
	for k := range want {
		if !contains(keys, k) {
			t.Fatalf("expected key %q in parsed keys %v", k, keys)
		}
	}

	// Each line must be valid JSON.
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("non-JSON NDJSON line: %q (%v)", line, err)
		}
	}
}

func TestWriteTextIsReadable(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	r.RecordSpan(SpanGeneration, 42*time.Millisecond)
	r.Counter(CounterToolCalls, 7)
	tr := r.Finish()
	var buf bytes.Buffer
	if err := WriteText(&buf, tr); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"trace run=", "spans:", "generation", "counters:", "tool_calls"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text trace missing %q:\n%s", want, out)
		}
	}
}

func TestWriteTextPropagatesWriteError(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	r.RecordSpan(SpanGeneration, 42*time.Millisecond)
	r.Counter(CounterToolCalls, 7)
	tr := r.Finish()
	if err := WriteText(errWriter{}, tr); err == nil {
		t.Fatal("WriteText to a failing sink returned nil; want the write error")
	}
}

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("write failed") }

func TestAttributionRatio(t *testing.T) {
	r := NewRecorder("s", "r", "")
	r.Start()
	r.RecordSpan(SpanGeneration, 10*time.Millisecond)
	r.RecordSpan(SpanToolExecution, 10*time.Millisecond)
	tr := r.Finish()

	// Attributed time is the deterministic sum of recorded span durations; it
	// does not depend on wall-clock timing.
	if want := 20 * time.Millisecond; tr.AttributedDuration() != want {
		t.Fatalf("attributed = %v, want %v", tr.AttributedDuration(), want)
	}

	// AttributionRatio is attributed / wall. The spans are synthetic durations
	// with no real delay, so wall is on the order of microseconds and the ratio
	// can legitimately exceed 1.0 (overlapping parallel spans push it above 1.0
	// in real runs too). Assert the contract — ratio == attributed/wall — rather
	// than a fixed bound, so the test is clock-independent.
	wall := tr.WallDuration()
	if wall <= 0 {
		// Clock granularity collapsed the run to zero wall; the ratio is defined 0.
		if got := tr.AttributionRatio(); got != 0 {
			t.Fatalf("ratio with zero wall = %v, want 0", got)
		}
		return
	}
	wantRatio := float64(tr.AttributedDuration()) / float64(wall)
	if got := tr.AttributionRatio(); got != wantRatio {
		t.Fatalf("ratio = %v, want %v (attributed/wall)", got, wantRatio)
	}
}

func TestAttributionRatioZeroWall(t *testing.T) {
	// A trace with no completed run has zero wall and a defined-zero ratio
	// (covers the divide-by-zero guard).
	tr := &TurnTrace{}
	if got := tr.AttributionRatio(); got != 0 {
		t.Fatalf("zero-wall ratio = %v, want 0", got)
	}
	if got := tr.AttributedDuration(); got != 0 {
		t.Fatalf("empty attributed = %v, want 0", got)
	}
	if got := tr.WallDuration(); got != 0 {
		t.Fatalf("empty wall = %v, want 0", got)
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestReadNDJSONRoundTrip(t *testing.T) {
	r := NewRecorder("s1", "r1", "cold")
	r.Start()
	r.RecordSpan(SpanPromptBuild, 10*time.Millisecond)
	r.RecordSpan(SpanGeneration, 50*time.Millisecond)
	r.RecordSpan(SpanGeneration, 5*time.Millisecond) // accumulates to 55ms
	r.Counter(CounterModelRequests, 3)
	r.Counter(CounterToolCalls, 7)
	r.StampFirstToken()
	original := r.Finish()

	var buf bytes.Buffer
	if err := WriteNDJSON(&buf, original); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	parsed, err := ReadNDJSON(&buf)
	if err != nil {
		t.Fatalf("ReadNDJSON: %v", err)
	}
	if parsed == nil {
		t.Fatal("ReadNDJSON returned nil")
	}
	if parsed.RunID != original.RunID || parsed.SessionID != original.SessionID || parsed.Profile != original.Profile {
		t.Fatalf("identity mismatch: got %+v want %+v", parsed, original)
	}
	if got := parsed.Span(SpanGeneration); got != 55*time.Millisecond {
		t.Fatalf("generation span after round-trip = %v, want 55ms", got)
	}
	if got := parsed.Counter(CounterModelRequests); got != 3 {
		t.Fatalf("model_requests after round-trip = %d, want 3", got)
	}
	if got := parsed.Counter(CounterToolCalls); got != 7 {
		t.Fatalf("tool_calls after round-trip = %d, want 7", got)
	}
	if parsed.FirstTokenAt.IsZero() {
		t.Fatal("first_token_at lost in round-trip")
	}
}
