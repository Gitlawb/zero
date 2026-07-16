package perfbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/trace"
)

// TurnSchemaVersion is the schema version of a published turn-benchmark result.
// Bump when the TurnBenchResult shape changes so consumers can detect drift.
const TurnSchemaVersion = 1

// TurnRunner runs one benchmark task and reports its outcome plus the captured
// per-turn trace. A non-nil Err means the run failed to execute (process crash);
// Passed reflects the verification result. Trace is the parsed NDJSON trace
// (nil when the run errored before emitting one).
type TurnRunner func(ctx context.Context, task BenchTask, rc RunContext) TurnTaskOutcome

// TurnTaskOutcome is what a TurnRunner reports for one task iteration.
type TurnTaskOutcome struct {
	Passed    bool
	VerifyErr string
	WallMs    float64
	Trace     *trace.TurnTrace
	// TraceIssue, when non-empty, explains why the per-turn trace could not be
	// parsed (file missing or malformed). The run still has a pass/fail verdict,
	// but an incomplete measurement is surfaced as a result warning so it can't
	// masquerade as a clean attribution sample.
	TraceIssue string
	Err        error
}

// TurnBenchConfig configures a turn-benchmark run.
type TurnBenchConfig struct {
	Model       string
	Mode        string
	SelfCorrect bool
	Version     string
	Commit      string
	// Iterations is how many times each task is run. The per-process `zero exec`
	// runner is inherently cold-start, so this is the sample count for per-span
	// median/P95 — a genuine warm path needs an in-process runner (future work).
	Iterations int
	// Runner executes one task iteration. Required.
	Runner TurnRunner
	// Now overrides the clock for the recorded date (tests inject a fixed time).
	Now func() time.Time
}

// SpanStats summarizes one span's duration across all measured task iterations.
type SpanStats struct {
	Count    int     `json:"count"`
	TotalMs  float64 `json:"totalMs"`
	MedianMs float64 `json:"medianMs"`
	P95Ms    float64 `json:"p95Ms"`
	MaxMs    float64 `json:"maxMs"`
}

// LatencySource is one of the top controllable latency sources, ranked by total
// exclusive time across the whole run. Share is its fraction of total exclusive
// span time. Because exclusive time subtracts nested children, concurrent and
// nested spans no longer double-count, so shares of the top sources sum to ~1
// for a well-instrumented run.
type LatencySource struct {
	Span    string  `json:"span"`
	TotalMs float64 `json:"totalMs"`
	Share   float64 `json:"share"`
}

// ClassSummary is the per-class (task group) roll-up.
type ClassSummary struct {
	Tasks      int                `json:"tasks"`
	Passed     int                `json:"passed"`
	WallMs     NumericStats       `json:"wallMs"`
	SpanTotals map[string]float64 `json:"spanTotals"`
}

// TurnBenchResult is the publishable turn-benchmark record.
type TurnBenchResult struct {
	SchemaVersion  int                     `json:"schemaVersion"`
	Suite          string                  `json:"suite"`
	Model          string                  `json:"model"`
	Mode           string                  `json:"mode,omitempty"`
	SelfCorrect    bool                    `json:"selfCorrect"`
	Version        string                  `json:"version,omitempty"`
	Commit         string                  `json:"commit,omitempty"`
	Date           string                  `json:"date"`
	TasksAttempted int                     `json:"tasksAttempted"`
	TasksPassed    int                     `json:"tasksPassed"`
	Iterations     int                     `json:"iterations"`
	PerSpan        map[string]SpanStats    `json:"perSpan"`
	TopLatency     []LatencySource         `json:"topLatency"`
	PerClass       map[string]ClassSummary `json:"perClass"`
	Totals         TurnBenchTotals         `json:"totals"`
	Warnings       []Warning               `json:"warnings,omitempty"`
}

// TurnBenchTotals aggregates token and count totals across the whole run.
type TurnBenchTotals struct {
	InputTokens       int64 `json:"inputTokens"`
	CachedInputTokens int64 `json:"cachedInputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	ModelRequests     int64 `json:"modelRequests"`
	ToolCalls         int64 `json:"toolCalls"`
	Retries           int64 `json:"retries"`
	Reconnects        int64 `json:"reconnects"`
	Compactions       int64 `json:"compactions"`
}

// RunTurnBench executes every task in the set with the configured runner and
// returns a self-describing per-turn result. It never aborts on a single task
// failure — every task is attempted and recorded. Per-span stats aggregate
// across iterations; the top three controllable latency sources are ranked by
// total attributed time.
func RunTurnBench(ctx context.Context, set TaskSet, cfg TurnBenchConfig) (TurnBenchResult, error) {
	if len(set.Tasks) == 0 {
		return TurnBenchResult{}, errors.New("task set has no tasks")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return TurnBenchResult{}, errors.New("turn benchmark requires a model")
	}
	if cfg.Runner == nil {
		return TurnBenchResult{}, errors.New("turn benchmark requires a runner")
	}
	iterations := cfg.Iterations
	if iterations < 1 {
		iterations = 1
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	rc := RunContext{Model: cfg.Model, Mode: cfg.Mode, SelfCorrect: cfg.SelfCorrect}

	perSpanSamples := map[string][]float64{}
	classWalls := map[string][]float64{}
	classSpanTotals := map[string]map[string]float64{}
	classTasks := map[string]int{}
	classPassed := map[string]int{}
	var totals TurnBenchTotals

	result := TurnBenchResult{
		SchemaVersion: TurnSchemaVersion,
		Suite:         strings.TrimSpace(set.ID),
		Model:         strings.TrimSpace(cfg.Model),
		Mode:          strings.TrimSpace(cfg.Mode),
		SelfCorrect:   cfg.SelfCorrect,
		Version:       strings.TrimSpace(cfg.Version),
		Commit:        strings.TrimSpace(cfg.Commit),
		Date:          now().UTC().Format(time.RFC3339),
		Iterations:    iterations,
		PerSpan:       map[string]SpanStats{},
		PerClass:      map[string]ClassSummary{},
	}

	for _, task := range set.Tasks {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		class := strings.TrimSpace(task.Class)
		if class == "" {
			class = "default"
		}
		classTasks[class]++
		// A task counts as passed only when every iteration passed. The per-process
		// runner is cold-start, so a flaky pass on one iteration and a fail on
		// another is a real regression signal, not noise to average away.
		passedForTask := true
		for iter := 0; iter < iterations; iter++ {
			outcome := cfg.Runner(ctx, task, rc)
			if outcome.TraceIssue != "" {
				result.Warnings = append(result.Warnings, Warning{
					Metric:  "trace",
					Message: fmt.Sprintf("task %s: %s", task.ID, outcome.TraceIssue),
				})
			}
			if outcome.Err != nil {
				passedForTask = false
				continue
			}
			if !outcome.Passed {
				passedForTask = false
			}
			wall := outcome.WallMs
			if wall <= 0 && outcome.Trace != nil {
				wall = float64(outcome.Trace.WallDuration().Microseconds()) / 1000
			}
			if wall > 0 {
				classWalls[class] = append(classWalls[class], wall)
			}
			if outcome.Trace != nil {
				aggregateTotals(&totals, outcome.Trace)
				for _, span := range outcome.Trace.Spans {
					// Rank by exclusive time (duration minus nested children) so
					// concurrent/nested spans do not double-count: provider_connect
					// inside generation and permission_wait inside tool_execution
					// each contribute their own exclusive time, not their parent's.
					d := span.Exclusive
					if d <= 0 {
						d = span.Duration
					}
					ms := float64(d.Microseconds()) / 1000
					perSpanSamples[span.Name] = append(perSpanSamples[span.Name], ms)
					if classSpanTotals[class] == nil {
						classSpanTotals[class] = map[string]float64{}
					}
					classSpanTotals[class][span.Name] += ms
				}
			}
		}
		result.TasksAttempted++
		if passedForTask {
			result.TasksPassed++
			classPassed[class]++
		}
	}

	for name, samples := range perSpanSamples {
		result.PerSpan[name] = summarizeSpan(samples)
	}
	result.TopLatency = topLatencySources(result.PerSpan, 3)
	for class := range classTasks {
		walls := classWalls[class]
		var wallStats NumericStats
		if len(walls) > 0 {
			wallStats = SummarizeSamples(walls)
		}
		result.PerClass[class] = ClassSummary{
			Tasks:      classTasks[class],
			Passed:     classPassed[class],
			WallMs:     wallStats,
			SpanTotals: classSpanTotals[class],
		}
	}
	result.Totals = totals
	return result, nil
}

func summarizeSpan(samples []float64) SpanStats {
	if len(samples) == 0 {
		return SpanStats{}
	}
	stats := SummarizeSamples(samples)
	total := 0.0
	for _, s := range samples {
		total += s
	}
	return SpanStats{
		Count:    len(samples),
		TotalMs:  RoundMetric(total),
		MedianMs: stats.Median,
		P95Ms:    stats.P95,
		MaxMs:    stats.Max,
	}
}

func topLatencySources(perSpan map[string]SpanStats, top int) []LatencySource {
	sources := make([]LatencySource, 0, len(perSpan))
	totalAttributed := 0.0
	for _, s := range perSpan {
		totalAttributed += s.TotalMs
	}
	for name, s := range perSpan {
		share := 0.0
		if totalAttributed > 0 {
			share = s.TotalMs / totalAttributed
		}
		sources = append(sources, LatencySource{
			Span:    name,
			TotalMs: s.TotalMs,
			Share:   RoundMetric(share),
		})
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].TotalMs != sources[j].TotalMs {
			return sources[i].TotalMs > sources[j].TotalMs
		}
		return sources[i].Span < sources[j].Span
	})
	if top > 0 && len(sources) > top {
		sources = sources[:top]
	}
	return sources
}

func aggregateTotals(totals *TurnBenchTotals, tr *trace.TurnTrace) {
	totals.InputTokens += tr.Counter(trace.CounterInputTokens)
	totals.CachedInputTokens += tr.Counter(trace.CounterCachedInputTokens)
	totals.OutputTokens += tr.Counter(trace.CounterOutputTokens)
	totals.ModelRequests += tr.Counter(trace.CounterModelRequests)
	totals.ToolCalls += tr.Counter(trace.CounterToolCalls)
	totals.Retries += tr.Counter(trace.CounterRetryCount)
	totals.Reconnects += tr.Counter(trace.CounterReconnectCount)
	totals.Compactions += tr.Counter(trace.CounterCompactionCount)
}

// FormatTurnBenchSummary renders a human-readable turn-benchmark summary that
// names the top controllable latency sources — the baseline's "do not proceed
// until" criterion.
func FormatTurnBenchSummary(result TurnBenchResult) string {
	lines := []string{
		"Zero turn benchmark: " + displayOrUnknown(result.Suite),
		"model: " + displayOrUnknown(result.Model),
		fmt.Sprintf("tasks: %d/%d passed across %d iteration(s)", result.TasksPassed, result.TasksAttempted, result.Iterations),
	}
	if result.Mode != "" {
		lines = append(lines, "mode: "+result.Mode)
	}
	if len(result.TopLatency) > 0 {
		lines = append(lines, "top latency sources:")
		for _, src := range result.TopLatency {
			lines = append(lines, fmt.Sprintf("  %-18s %10s  %5.1f%%", src.Span, FormatMetric(src.TotalMs, "ms"), src.Share*100))
		}
	}
	lines = append(lines, fmt.Sprintf("totals: in=%d (cached %d) out=%d | requests=%d tools=%d retries=%d reconnects=%d compactions=%d",
		result.Totals.InputTokens, result.Totals.CachedInputTokens, result.Totals.OutputTokens,
		result.Totals.ModelRequests, result.Totals.ToolCalls, result.Totals.Retries,
		result.Totals.Reconnects, result.Totals.Compactions))
	for _, class := range sortedClasses(result.PerClass) {
		summary := result.PerClass[class]
		median := FormatMetric(summary.WallMs.Median, "ms")
		lines = append(lines, fmt.Sprintf("  [%s] %d/%d passed, wall median %s", class, summary.Passed, summary.Tasks, median))
	}
	return strings.Join(lines, "\n")
}

func sortedClasses(perClass map[string]ClassSummary) []string {
	classes := make([]string, 0, len(perClass))
	for c := range perClass {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	return classes
}

// WriteTurnBenchJSON writes the indented JSON form of a turn-benchmark result.
func WriteTurnBenchJSON(w io.Writer, result TurnBenchResult) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

// NewTurnExecRunner builds the production turn-benchmark runner: it invokes
// headless `zero exec` with stream-json output AND `--trace <tmpfile>`, then
// parses the emitted NDJSON trace into a *trace.TurnTrace. binary is the path to
// the `zero` binary; extraArgs are appended to every invocation. Pass/fail is
// decided from the stream-json run_end exit code (and the task's
// VerificationCommand when present), exactly like NewExecRunner.
func NewTurnExecRunner(binary string, extraArgs ...string) TurnRunner {
	return func(ctx context.Context, task BenchTask, rc RunContext) TurnTaskOutcome {
		// Isolate the workspace: copy the fixture into a fresh temp dir so a
		// mutating task (edit/fix/refactor) can't dirty the shared, checked-in
		// fixture or bleed into a later iteration of the same task. When no
		// fixture is configured the agent runs in the caller's cwd as before.
		if fixture := strings.TrimSpace(task.WorkspaceFixture); fixture != "" {
			copyDir, cerr := copyFixture(fixture)
			if cerr != nil {
				return TurnTaskOutcome{Err: fmt.Errorf("isolate fixture: %w", cerr)}
			}
			defer os.RemoveAll(copyDir)
			task.WorkspaceFixture = copyDir
		}

		traceFile, err := os.CreateTemp("", "zero-turn-trace-*.ndjson")
		if err != nil {
			return TurnTaskOutcome{Err: fmt.Errorf("create trace file: %w", err)}
		}
		_ = traceFile.Close()
		tracePath := traceFile.Name()
		defer os.Remove(tracePath)

		args := buildTurnExecArgs(task, rc, tracePath, extraArgs)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = appendNoColor(os.Environ())
		if dir := strings.TrimSpace(task.WorkspaceFixture); dir != "" {
			cmd.Dir = dir
		}
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		start := time.Now()
		runErr := cmd.Run()
		wallMs := float64(time.Since(start).Microseconds()) / 1000
		_ = runErr

		exitCode, haveExit := streamJSONExitCode(outBuf.Bytes())
		outcome := TurnTaskOutcome{WallMs: wallMs}
		if haveExit && exitCode != 0 {
			outcome.VerifyErr = fmt.Sprintf("agent run_end exit code %d", exitCode)
		} else if !haveExit {
			detail := strings.TrimSpace(errBuf.String())
			if detail == "" {
				detail = "missing terminal run_end event"
			}
			outcome.Err = fmt.Errorf("zero exec failed: %s", detail)
			return outcome
		}

		// Parse the captured trace. The trace is the attribution layer, so a
		// missing/malformed file is not fatal — but it IS surfaced as a warning
		// (via outcome.TraceIssue) so an incomplete measurement can't look valid.
		if f, ferr := os.Open(tracePath); ferr != nil {
			outcome.TraceIssue = fmt.Sprintf("trace open failed: %v", ferr)
		} else {
			tr, perr := trace.ReadNDJSON(f)
			_ = f.Close()
			if perr != nil {
				outcome.TraceIssue = fmt.Sprintf("trace parse failed: %v", perr)
			} else {
				outcome.Trace = tr
			}
		}

		// A nonzero agent exit already decided failure; don't run verification or
		// mark the task passed.
		if outcome.VerifyErr != "" {
			return outcome
		}
		if len(task.VerificationCommand) > 0 {
			if vOutcome := runVerification(ctx, task); !vOutcome.Passed {
				outcome.VerifyErr = strings.TrimSpace(vOutcome.Detail)
				return outcome
			}
		}
		outcome.Passed = true
		return outcome
	}
}

// copyFixture copies the fixture directory at src into a fresh temp dir and
// returns the copy's path. Used to give each benchmark invocation an isolated
// workspace so mutating tasks (edit/fix/refactor) can't dirty the checked-in
// fixture or a later iteration.
func copyFixture(src string) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("fixture %q is not a directory", src)
	}
	dst, err := os.MkdirTemp("", "zero-turn-fixture-*")
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	return dst, nil
}

func buildTurnExecArgs(task BenchTask, rc RunContext, tracePath string, extraArgs []string) []string {
	args := []string{"exec", "--output-format", "stream-json", "--trace", tracePath}
	if model := strings.TrimSpace(rc.Model); model != "" {
		args = append(args, "--model", model)
	}
	if mode := strings.TrimSpace(rc.Mode); mode != "" {
		args = append(args, "--mode", mode)
	}
	if rc.SelfCorrect {
		args = append(args, "--self-correct")
	}
	args = append(args, extraArgs...)
	args = append(args, task.Prompt)
	return args
}

// ResolveBinary locates the zero binary for a benchmark run: an explicit path
// when provided, else a `zero` (or zero.exe) on PATH, else a binary built into
// the repo root. Returns an error when none is found.
func ResolveBinary(explicit string) (string, error) {
	if v := strings.TrimSpace(explicit); v != "" {
		if _, err := os.Stat(v); err != nil {
			return "", fmt.Errorf("trace binary not found: %w", err)
		}
		return v, nil
	}
	if path, err := exec.LookPath("zero"); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath("zero.exe"); err == nil {
		return path, nil
	}
	cwd, err := os.Getwd()
	if err == nil {
		for _, name := range []string{"zero", "zero.exe"} {
			candidate := filepath.Join(cwd, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	return "", errors.New("zero binary not found; build it first or pass an explicit path")
}
