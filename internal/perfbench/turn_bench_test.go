package perfbench

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/trace"
)

// fakeTurnRunner returns a canned *trace.TurnTrace per task so the harness's
// aggregation logic can be exercised without a model or a binary. Each task
// yields one generation span (the dominant cost), one tool_execution span, and
// the counters the totals aggregate.
func fakeTurnRunner(canned map[string]*trace.TurnTrace) TurnRunner {
	return func(ctx context.Context, task BenchTask, rc RunContext) TurnTaskOutcome {
		tr, ok := canned[task.ID]
		if !ok {
			return TurnTaskOutcome{Err: errNoCanned}
		}
		wallMs := float64(tr.WallDuration().Microseconds()) / 1000
		return TurnTaskOutcome{Passed: true, WallMs: wallMs, Trace: tr}
	}
}

var errNoCanned = &cannedError{}

type cannedError struct{}

func (*cannedError) Error() string { return "no canned trace for task" }

// cannedTrace builds a deterministic *trace.TurnTrace with the given spans and
// counters. Spans are recorded as fixed durations (no real timing) so the
// aggregation math is predictable in assertions.
func cannedTrace(genMs, toolMs int, tokens int64) *trace.TurnTrace {
	r := trace.NewRecorder("sess", "run-1", "test")
	r.Start()
	r.RecordSpan(trace.SpanGeneration, time.Duration(genMs)*time.Millisecond)
	r.RecordSpan(trace.SpanToolExecution, time.Duration(toolMs)*time.Millisecond)
	r.Counter(trace.CounterInputTokens, tokens)
	r.Counter(trace.CounterOutputTokens, tokens/2)
	r.Counter(trace.CounterModelRequests, 1)
	r.Counter(trace.CounterToolCalls, 1)
	r.StampFirstToken()
	return r.Finish()
}

func TestRunTurnBenchAggregation(t *testing.T) {
	set := TaskSet{
		ID: "fake-suite",
		Tasks: []BenchTask{
			{ID: "t1", Class: "nav", Prompt: "p1"},
			{ID: "t2", Class: "nav", Prompt: "p2"},
			{ID: "t3", Class: "edit", Prompt: "p3"},
		},
	}
	canned := map[string]*trace.TurnTrace{
		"t1": cannedTrace(100, 10, 1000),
		"t2": cannedTrace(300, 10, 1000),
		"t3": cannedTrace(200, 50, 2000),
	}
	cfg := TurnBenchConfig{
		Model:      "fake-model",
		Iterations: 1,
		Runner:     fakeTurnRunner(canned),
		Now:        func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
	result, err := RunTurnBench(context.Background(), set, cfg)
	if err != nil {
		t.Fatalf("RunTurnBench: %v", err)
	}
	if result.TasksAttempted != 3 || result.TasksPassed != 3 {
		t.Fatalf("attempted=%d passed=%d, want 3/3", result.TasksAttempted, result.TasksPassed)
	}
	if result.SchemaVersion != TurnSchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", result.SchemaVersion, TurnSchemaVersion)
	}
	if result.Date != "2026-01-02T03:04:05Z" {
		t.Fatalf("date = %q", result.Date)
	}

	// Per-span: generation appears in all three (100+300+200=600ms), tool in all
	// three (10+10+50=70ms). Count must equal the number of tasks * iterations.
	gen := result.PerSpan[trace.SpanGeneration]
	if gen.Count != 3 {
		t.Fatalf("generation count = %d, want 3", gen.Count)
	}
	if gen.TotalMs != 600 {
		t.Fatalf("generation totalMs = %v, want 600", gen.TotalMs)
	}
	tool := result.PerSpan[trace.SpanToolExecution]
	if tool.TotalMs != 70 {
		t.Fatalf("tool totalMs = %v, want 70", tool.TotalMs)
	}

	// Top latency: generation (600) ranks above tool (70). Exactly two spans
	// here, so both appear and generation is first.
	if len(result.TopLatency) != 2 || result.TopLatency[0].Span != trace.SpanGeneration {
		t.Fatalf("topLatency = %+v", result.TopLatency)
	}
	if result.TopLatency[0].Share <= result.TopLatency[1].Share {
		t.Fatalf("top latency not ranked by share: %+v", result.TopLatency)
	}

	// Totals: 3 model requests, 3 tool calls, input tokens 1000+1000+2000=4000.
	if result.Totals.ModelRequests != 3 {
		t.Fatalf("modelRequests = %d, want 3", result.Totals.ModelRequests)
	}
	if result.Totals.ToolCalls != 3 {
		t.Fatalf("toolCalls = %d, want 3", result.Totals.ToolCalls)
	}
	if result.Totals.InputTokens != 4000 {
		t.Fatalf("inputTokens = %d, want 4000", result.Totals.InputTokens)
	}
	if result.Totals.OutputTokens != 2000 {
		t.Fatalf("outputTokens = %d, want 2000", result.Totals.OutputTokens)
	}

	// Per-class: nav has two tasks, edit has one; both fully passed.
	nav := result.PerClass["nav"]
	if nav.Tasks != 2 || nav.Passed != 2 {
		t.Fatalf("nav class = %+v", nav)
	}
	edit := result.PerClass["edit"]
	if edit.Tasks != 1 || edit.Passed != 1 {
		t.Fatalf("edit class = %+v", edit)
	}
}

func TestRunTurnBenchIterationsAggregates(t *testing.T) {
	set := TaskSet{
		ID:    "iter-suite",
		Tasks: []BenchTask{{ID: "t1", Class: "nav", Prompt: "p1"}},
	}
	canned := map[string]*trace.TurnTrace{"t1": cannedTrace(100, 10, 500)}
	cfg := TurnBenchConfig{
		Model:      "fake-model",
		Iterations: 3,
		Runner:     fakeTurnRunner(canned),
		Now:        func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
	result, err := RunTurnBench(context.Background(), set, cfg)
	if err != nil {
		t.Fatalf("RunTurnBench: %v", err)
	}
	if result.PerSpan[trace.SpanGeneration].Count != 3 {
		t.Fatalf("generation count = %d, want 3 (one per iteration)", result.PerSpan[trace.SpanGeneration].Count)
	}
	if result.PerSpan[trace.SpanGeneration].TotalMs != 300 {
		t.Fatalf("generation totalMs = %v, want 300", result.PerSpan[trace.SpanGeneration].TotalMs)
	}
}

func TestRunTurnBenchRequiresModelAndRunner(t *testing.T) {
	set := TaskSet{ID: "s", Tasks: []BenchTask{{ID: "t", Prompt: "p"}}}
	if _, err := RunTurnBench(context.Background(), set, TurnBenchConfig{Runner: fakeTurnRunner(nil)}); err == nil {
		t.Fatal("expected error for missing model")
	}
	if _, err := RunTurnBench(context.Background(), set, TurnBenchConfig{Model: "m"}); err == nil {
		t.Fatal("expected error for missing runner")
	}
	if _, err := RunTurnBench(context.Background(), TaskSet{ID: "empty"}, TurnBenchConfig{Model: "m", Runner: fakeTurnRunner(nil)}); err == nil {
		t.Fatal("expected error for empty task set")
	}
}

func TestTopLatencySourcesRanksByTotalAndCapsTopN(t *testing.T) {
	perSpan := map[string]SpanStats{
		"a": {TotalMs: 100},
		"b": {TotalMs: 500},
		"c": {TotalMs: 300},
		"d": {TotalMs: 50},
	}
	top := topLatencySources(perSpan, 3)
	if len(top) != 3 {
		t.Fatalf("len = %d, want 3", len(top))
	}
	wantOrder := []string{"b", "c", "a"}
	for i, w := range wantOrder {
		if top[i].Span != w {
			t.Fatalf("top[%d] = %q, want %q", i, top[i].Span, w)
		}
	}
	// Shares sum to 1 across all four (100+500+300+50=950); the top-3 retain
	// their global share (not renormalized to the top-3).
	// Share is rounded to 2 decimals by RoundMetric (500/950 -> 0.53), so compare
	// against the rounded value with a small tolerance.
	if got, want := top[0].Share, RoundMetric(500.0/950.0); !approxEqual(got, want, 0.001) {
		t.Fatalf("top[0] share = %v, want %v", got, want)
	}
}

func TestWriteTurnBenchJSONRoundTrip(t *testing.T) {
	set := TaskSet{ID: "json-suite", Tasks: []BenchTask{{ID: "t1", Class: "nav", Prompt: "p1"}}}
	canned := map[string]*trace.TurnTrace{"t1": cannedTrace(150, 20, 800)}
	result, err := RunTurnBench(context.Background(), set, TurnBenchConfig{
		Model:  "fake-model",
		Runner: fakeTurnRunner(canned),
		Now:    func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("RunTurnBench: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteTurnBenchJSON(&buf, result); err != nil {
		t.Fatalf("WriteTurnBenchJSON: %v", err)
	}
	var decoded TurnBenchResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if decoded.SchemaVersion != TurnSchemaVersion || decoded.TasksPassed != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.PerSpan[trace.SpanGeneration].TotalMs != 150 {
		t.Fatalf("decoded generation totalMs = %v, want 150", decoded.PerSpan[trace.SpanGeneration].TotalMs)
	}
}

func TestFormatTurnBenchSummaryNamesTopSources(t *testing.T) {
	set := TaskSet{ID: "fmt-suite", Tasks: []BenchTask{{ID: "t1", Class: "nav", Prompt: "p1"}}}
	canned := map[string]*trace.TurnTrace{"t1": cannedTrace(150, 20, 800)}
	result, err := RunTurnBench(context.Background(), set, TurnBenchConfig{
		Model:  "fake-model",
		Runner: fakeTurnRunner(canned),
		Now:    func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("RunTurnBench: %v", err)
	}
	summary := FormatTurnBenchSummary(result)
	if !strings.Contains(summary, "top latency sources") {
		t.Fatalf("summary missing top-latency header:\n%s", summary)
	}
	if !strings.Contains(summary, trace.SpanGeneration) {
		t.Fatalf("summary missing top span name %q:\n%s", trace.SpanGeneration, summary)
	}
}

func TestLoadBaselineManifest(t *testing.T) {
	path := filepath.Join("manifests", "baseline.json")
	set, err := LoadTaskSet(path)
	if err != nil {
		t.Fatalf("LoadTaskSet: %v", err)
	}
	if set.ID == "" {
		t.Fatal("manifest has no id")
	}
	// The baseline must clear the "do not proceed until ≥30 tasks" gate.
	if len(set.Tasks) < 30 {
		t.Fatalf("baseline has %d tasks, want >= 30", len(set.Tasks))
	}
	// The six required classes must all be present and non-empty.
	wantClasses := map[string]bool{
		"nav": false, "edit": false, "fix": false,
		"refactor": false, "longproc": false, "longctx": false, "parallel": false,
	}
	counts := map[string]int{}
	for _, task := range set.Tasks {
		class := strings.TrimSpace(task.Class)
		if class == "" {
			t.Fatalf("task %q has no class", task.ID)
		}
		if _, ok := wantClasses[class]; !ok {
			t.Fatalf("unexpected class %q on task %q", class, task.ID)
		}
		wantClasses[class] = true
		counts[class]++
	}
	for class, present := range wantClasses {
		if !present {
			t.Fatalf("manifest missing required class %q", class)
		}
		if counts[class] == 0 {
			t.Fatalf("class %q has zero tasks", class)
		}
	}
	// Every task must have a prompt and a workspace fixture pointing under testdata,
	// and that fixture must actually exist on disk — a manifest referencing a
	// missing fixture would make every task in its class error out at run time, so
	// catch it at load time instead.
	seen := map[string]bool{}
	for _, task := range set.Tasks {
		if strings.TrimSpace(task.Prompt) == "" {
			t.Fatalf("task %q has empty prompt", task.ID)
		}
		if strings.TrimSpace(task.WorkspaceFixture) == "" {
			t.Fatalf("task %q has no workspace fixture", task.ID)
		}
		if !strings.Contains(task.WorkspaceFixture, "testdata") {
			t.Fatalf("task %q fixture %q not under testdata", task.ID, task.WorkspaceFixture)
		}
		if seen[task.WorkspaceFixture] {
			continue
		}
		seen[task.WorkspaceFixture] = true
		info, err := os.Stat(task.WorkspaceFixture)
		if err != nil {
			t.Fatalf("task %q fixture %q does not exist: %v", task.ID, task.WorkspaceFixture, err)
		}
		if !info.IsDir() {
			t.Fatalf("task %q fixture %q is not a directory", task.ID, task.WorkspaceFixture)
		}
	}
}

func approxEqual(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < tol
}
