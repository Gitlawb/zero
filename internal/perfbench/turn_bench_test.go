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
			{ID: "t1", Class: "nav", Prompt: "p1"},                                             // latency-only
			{ID: "t2", Class: "nav", Prompt: "p2"},                                             // latency-only
			{ID: "t3", Class: "edit", Prompt: "p3", VerificationCommand: []string{"true"}},     // correctness
			{ID: "t4", Class: "refactor", Prompt: "p4", VerificationCommand: []string{"true"}}, // build-only
		},
		BuildOnlyClasses: []string{"refactor"},
	}
	canned := map[string]*trace.TurnTrace{
		"t1": cannedTrace(100, 10, 1000),
		"t2": cannedTrace(300, 10, 1000),
		"t3": cannedTrace(200, 50, 2000),
		"t4": cannedTrace(150, 20, 1500),
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
	// 4 tasks attempted; the tier split is 2 latency-only, 1 correctness, 1 build.
	if result.TasksAttempted != 4 {
		t.Fatalf("attempted=%d, want 4", result.TasksAttempted)
	}
	if result.TasksVerified != 1 || result.TasksPassed != 1 {
		t.Fatalf("correctness verified=%d passed=%d, want 1/1", result.TasksVerified, result.TasksPassed)
	}
	if result.LatencyOnlyTasks != 2 {
		t.Fatalf("latencyOnly=%d, want 2", result.LatencyOnlyTasks)
	}
	if result.BuildCheckedTasks != 1 || result.BuildPassedTasks != 1 {
		t.Fatalf("build checked=%d passed=%d, want 1/1", result.BuildCheckedTasks, result.BuildPassedTasks)
	}
	if result.CorrectnessPassRate != 1.0 {
		t.Fatalf("correctnessPassRate=%v, want 1.0", result.CorrectnessPassRate)
	}
	if result.BuildPassRate != 1.0 {
		t.Fatalf("buildPassRate=%v, want 1.0", result.BuildPassRate)
	}
	if len(result.CorrectnessClasses) != 1 || result.CorrectnessClasses[0] != "edit" {
		t.Fatalf("correctnessClasses=%v, want [edit]", result.CorrectnessClasses)
	}
	if len(result.BuildOnlyClasses) != 1 || result.BuildOnlyClasses[0] != "refactor" {
		t.Fatalf("buildOnlyClasses=%v, want [refactor]", result.BuildOnlyClasses)
	}
	if len(result.LatencyOnlyClasses) != 1 || result.LatencyOnlyClasses[0] != "nav" {
		t.Fatalf("latencyOnlyClasses=%v, want [nav]", result.LatencyOnlyClasses)
	}
	if result.SchemaVersion != TurnSchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", result.SchemaVersion, TurnSchemaVersion)
	}
	if result.Date != "2026-01-02T03:04:05Z" {
		t.Fatalf("date = %q", result.Date)
	}

	// Per-span: generation appears in all four (100+300+200+150=750ms), tool in
	// all four (10+10+50+20=90ms). Count must equal the number of tasks * iterations.
	gen := result.PerSpan[trace.SpanGeneration]
	if gen.Count != 4 {
		t.Fatalf("generation count = %d, want 4", gen.Count)
	}
	if gen.TotalMs != 750 {
		t.Fatalf("generation totalMs = %v, want 750", gen.TotalMs)
	}
	tool := result.PerSpan[trace.SpanToolExecution]
	if tool.TotalMs != 90 {
		t.Fatalf("tool totalMs = %v, want 90", tool.TotalMs)
	}

	// Top latency: generation (750) ranks above tool (90). Exactly two spans
	// here, so both appear and generation is first.
	if len(result.TopLatency) != 2 || result.TopLatency[0].Span != trace.SpanGeneration {
		t.Fatalf("topLatency = %+v", result.TopLatency)
	}
	if result.TopLatency[0].Share <= result.TopLatency[1].Share {
		t.Fatalf("top latency not ranked by share: %+v", result.TopLatency)
	}

	// Totals: 4 model requests, 4 tool calls, input tokens 1000+1000+2000+1500=5500.
	if result.Totals.ModelRequests != 4 {
		t.Fatalf("modelRequests = %d, want 4", result.Totals.ModelRequests)
	}
	if result.Totals.ToolCalls != 4 {
		t.Fatalf("toolCalls = %d, want 4", result.Totals.ToolCalls)
	}
	if result.Totals.InputTokens != 5500 {
		t.Fatalf("inputTokens = %d, want 5500", result.Totals.InputTokens)
	}
	if result.Totals.OutputTokens != 2750 {
		t.Fatalf("outputTokens = %d, want 2750", result.Totals.OutputTokens)
	}

	// Per-class tier roll-up: nav is latency-only (0 verified, 2 latency-only),
	// edit is correctness (1/1 verified passed), refactor is build (1/1 passed).
	nav := result.PerClass["nav"]
	if nav.Tasks != 2 || nav.Verified != 0 || nav.Passed != 0 || nav.LatencyOnly != 2 {
		t.Fatalf("nav class = %+v", nav)
	}
	edit := result.PerClass["edit"]
	if edit.Tasks != 1 || edit.Verified != 1 || edit.Passed != 1 || edit.LatencyOnly != 0 {
		t.Fatalf("edit class = %+v", edit)
	}
	refactor := result.PerClass["refactor"]
	if refactor.Tasks != 1 || refactor.Verified != 1 || refactor.Passed != 1 || refactor.LatencyOnly != 0 {
		t.Fatalf("refactor class = %+v", refactor)
	}
}

// TestRunTurnBenchLatencyOnlyNeverPassed asserts the honesty gate: a task with
// no verificationCommand reports Passed=true from the (stub) runner, yet the
// harness counts it ONLY in latencyOnlyTasks — never in tasksPassed or any pass
// rate — so an exit-0 read-only run cannot inflate a correctness number.
func TestRunTurnBenchLatencyOnlyNeverPassed(t *testing.T) {
	set := TaskSet{
		ID: "lo-suite",
		Tasks: []BenchTask{
			{ID: "n1", Class: "nav", Prompt: "p"}, // no oracle — runner still says Passed=true
		},
	}
	cfg := TurnBenchConfig{
		Model:  "fake-model",
		Runner: fakeTurnRunner(map[string]*trace.TurnTrace{"n1": cannedTrace(50, 5, 100)}),
		Now:    func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
	result, err := RunTurnBench(context.Background(), set, cfg)
	if err != nil {
		t.Fatalf("RunTurnBench: %v", err)
	}
	if result.TasksPassed != 0 || result.TasksVerified != 0 {
		t.Fatalf("latency-only leaked into pass: passed=%d verified=%d, want 0/0",
			result.TasksPassed, result.TasksVerified)
	}
	if result.CorrectnessPassRate != 0 || result.BuildPassRate != 0 {
		t.Fatalf("pass rates should be 0 with no oracle tasks: c=%v b=%v",
			result.CorrectnessPassRate, result.BuildPassRate)
	}
	if result.LatencyOnlyTasks != 1 {
		t.Fatalf("latencyOnlyTasks=%d, want 1", result.LatencyOnlyTasks)
	}
	if result.PerClass["nav"].Passed != 0 || result.PerClass["nav"].LatencyOnly != 1 {
		t.Fatalf("nav class = %+v, want passed=0 latencyOnly=1", result.PerClass["nav"])
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
	set := TaskSet{ID: "json-suite", Tasks: []BenchTask{
		{ID: "t1", Class: "edit", Prompt: "p1", VerificationCommand: []string{"true"}},
	}}
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
	if decoded.SchemaVersion != TurnSchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", decoded.SchemaVersion, TurnSchemaVersion)
	}
	if decoded.TasksVerified != 1 || decoded.TasksPassed != 1 || decoded.LatencyOnlyTasks != 0 {
		t.Fatalf("decoded tier counts = verified=%d passed=%d latency=%d, want 1/1/0",
			decoded.TasksVerified, decoded.TasksPassed, decoded.LatencyOnlyTasks)
	}
	if decoded.CorrectnessPassRate != 1.0 {
		t.Fatalf("decoded correctnessPassRate = %v, want 1.0", decoded.CorrectnessPassRate)
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

// TestCopyFixtureIsolatesSourceFromMutation asserts the property that lets a
// mutating task run twice (or across iterations) without poisoning the next
// sample: the runner operates on a per-invocation copy, so mutating the copy
// leaves the checked-in source fixture byte-identical. This is verified by
// reading the code today; a test makes it durable against future refactors of
// the runner's isolation path.
func TestCopyFixtureIsolatesSourceFromMutation(t *testing.T) {
	src, err := os.MkdirTemp("", "zero-fixture-src-*")
	if err != nil {
		t.Fatalf("mkdtemp src: %v", err)
	}
	defer os.RemoveAll(src)
	orig := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(orig), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "a.go"), []byte("package sub\n"), 0o644); err != nil {
		t.Fatalf("write sub: %v", err)
	}

	// Snapshot the source tree before any mutation of the copy.
	wantMain, err := os.ReadFile(filepath.Join(src, "main.go"))
	if err != nil {
		t.Fatalf("read source main.go: %v", err)
	}

	copyDir, err := copyFixture(src)
	if err != nil {
		t.Fatalf("copyFixture: %v", err)
	}
	defer os.RemoveAll(copyDir)

	// The copy must be a real copy, not a symlink/alias of the source, and
	// mutating it must not touch the source.
	if copyDir == src {
		t.Fatalf("copyFixture returned the source dir, not a copy: %s", copyDir)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "main.go"), []byte("package main\n\n// mutated\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("mutate copy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("add file to copy: %v", err)
	}

	gotMain, err := os.ReadFile(filepath.Join(src, "main.go"))
	if err != nil {
		t.Fatalf("re-read source main.go: %v", err)
	}
	if string(gotMain) != string(wantMain) {
		t.Fatalf("source fixture mutated by copy: got %q, want %q", gotMain, wantMain)
	}
	if _, err := os.Stat(filepath.Join(src, "new.go")); !os.IsNotExist(err) {
		t.Fatalf("file added to copy appeared in source: %v", err)
	}
}
