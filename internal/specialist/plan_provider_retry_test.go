package specialist

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/streamjson"
)

// A PROVIDER FAILURE IS NOT AN ANSWER.
//
// The transport deliberately does not replay a 500/502/504 — unlike 429/503/529
// those do not guarantee the request had no effect, so a replay could pay for
// the same completion twice. That is right for a replay and wrong for a plan: a
// measured ten-task run lost one task to a bare "Internal Server Error" after 6
// tool calls and 42,524 tokens, and everything depending on it went with it. A
// fresh child is a new request, not that replay.

// providerFailingRunner fails on the provider for the first `failures` attempts
// of each task, then succeeds — counting attempts per id.
func providerFailingRunner(failures map[string]int, counts map[string]int) PlanRunner {
	return func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
		id := req.Task.ID
		counts[id]++
		if counts[id] <= failures[id] {
			return TaskResult{
				ID: id, Outcome: TaskFailed, ProviderFailed: true,
				Err: "provider error: Internal Server Error (ref: 4bab40ea)", Tokens: 42524,
			}, nil
		}
		return TaskResult{ID: id, Outcome: TaskSucceeded, Tokens: 7}, nil
	}
}

func TestAProviderFailureIsRetriedOnce(t *testing.T) {
	plan := mustPlan(t, []any{task("a", "find every worktree creation site")}, okBudget(), readOnlyLimits())
	counts := map[string]int{}
	report := ExecutePlan(context.Background(), plan, []string{"read_file"},
		providerFailingRunner(map[string]int{"a": 1}, counts), nil)

	if counts["a"] != 2 {
		t.Fatalf("the task ran %d times; a provider failure is worth exactly one more attempt", counts["a"])
	}
	if report.Succeeded != 1 {
		t.Fatalf("report = %+v; the second attempt succeeded and the plan must say so", report)
	}
	// The spend of BOTH attempts is reported: a retry that hides its cost is how
	// a plan's reported spend stops being its real spend.
	if got := report.Tasks[0].Tokens; got != 42524+7 {
		t.Fatalf("Tokens = %d, want both attempts counted (%d)", got, 42524+7)
	}
	if got := report.Tasks[0].Attempts; got != 2 {
		t.Fatalf("Attempts = %d, want 2", got)
	}
}

// BOUNDED AT ONE. A provider failing twice in a row is having an outage, not a
// bad moment, and a second retry spends another child to learn that.
func TestAProviderFailureIsRetriedOnlyOnce(t *testing.T) {
	plan := mustPlan(t, []any{task("a", "find every worktree creation site")}, okBudget(), readOnlyLimits())
	counts := map[string]int{}
	report := ExecutePlan(context.Background(), plan, []string{"read_file"},
		providerFailingRunner(map[string]int{"a": 99}, counts), nil)

	if counts["a"] != 2 {
		t.Fatalf("the task ran %d times; the provider retry is bounded at one", counts["a"])
	}
	if report.Failed != 1 {
		t.Fatalf("report = %+v; a provider that keeps failing must still fail the task", report)
	}
}

// THE SAFETY BOUND, and the whole reason this is not a blanket retry: a WRITE
// task may have applied part of its change before the provider died, and no
// exit code says how far it got. Re-running it could apply that change twice.
// A read-only task re-reads, which costs tokens and nothing else.
func TestAWriteTaskIsNotRetriedOnAProviderFailure(t *testing.T) {
	writeTask := task("a", "add the missing guard")
	writeTask["tools"] = []any{"read_file", "write_file"}
	limits := readOnlyLimits()
	limits.ParentTools = []string{"read_file", "write_file"}
	plan := mustPlan(t, []any{writeTask}, okBudget(), limits)

	counts := map[string]int{}
	report := ExecutePlan(context.Background(), plan, []string{"read_file", "write_file"},
		providerFailingRunner(map[string]int{"a": 1}, counts), nil)

	if counts["a"] != 1 {
		t.Fatalf("a write task ran %d times; re-running one that may have half-applied its change "+
			"could apply it twice", counts["a"])
	}
	if report.Failed != 1 {
		t.Fatalf("report = %+v; the write task must fail rather than silently retry", report)
	}
}

// An ordinary failure is still an answer: the task read the code and concluded
// wrongly, and running it again buys the same report. Only the provider flag
// earns the extra attempt.
func TestAnOrdinaryFailureIsStillNotRetried(t *testing.T) {
	plan := mustPlan(t, []any{task("a", "find every worktree creation site")}, okBudget(), readOnlyLimits())
	counts := map[string]int{}
	run := func(_ context.Context, req PlanTaskRequest) (TaskResult, error) {
		counts[req.Task.ID]++
		return TaskResult{ID: req.Task.ID, Outcome: TaskFailed, Err: "could not find it", Tokens: 5}, nil
	}
	ExecutePlan(context.Background(), plan, []string{"read_file"}, run, nil)

	if counts["a"] != 1 {
		t.Fatalf("an ordinary failure ran %d times; only a provider failure earns a retry", counts["a"])
	}
}

// THE JOIN, and the reason this test exists separately from the four above.
//
// Those drive the executor with a fake runner that SETS ProviderFailed, so they
// prove the retry logic and nothing about where the flag comes from. Gutting the
// classification in plan_runner.go left every one of them green — the exact
// shape of defect this package keeps relearning: a value written at one layer,
// consumed at another, with nothing asserting the join. This drives the REAL
// runner and reads the flag off its result.
func TestTheChildsProviderExitCodeBecomesTheFlag(t *testing.T) {
	for _, tc := range []struct {
		name     string
		exitCode int
		want     bool
	}{
		{"provider failure", childExitProvider, true},
		{"declined", childExitIncomplete, false},
		{"ordinary failure", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := tc.exitCode
			executor := Executor{
				BinaryPath:   "/bin/true",
				NewSessionID: func() (string, error) { return "specialist_00000000000000000000000a", nil },
				Load:         func(LoadOptions) (LoadResult, error) { return LoadResult{}, nil },
				RunChild: func(_ context.Context, _ string, _ []string, emit func(streamjson.Event)) (ChildRunResult, error) {
					events := []streamjson.Event{
						{Type: streamjson.EventRunStart, RunID: "run_1", SessionID: "specialist_00000000000000000000000a"},
						{Type: streamjson.EventRunEnd, RunID: "run_1", Status: "error", ExitCode: &exit},
					}
					for _, event := range events {
						if emit != nil {
							emit(event)
						}
					}
					return ChildRunResult{Events: events, ExitCode: exit, Started: true}, nil
				},
			}
			run := NewPlanRunner(PlanTaskContext{Executor: executor, Cwd: t.TempDir(), SpecialistName: "explorer"})
			result, err := run(context.Background(), PlanTaskRequest{
				Task:  Task{ID: "a", Prompt: "find every worktree creation site"},
				Tools: []string{"read_file"},
			})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if result.ProviderFailed != tc.want {
				t.Fatalf("exit %d produced ProviderFailed=%v, want %v — the retry keys on this flag",
					tc.exitCode, result.ProviderFailed, tc.want)
			}
		})
	}
}

// THE OTHER HALF OF THE AGREEMENT. internal/cli asserts its exitProvider equals
// the literal 3; this asserts THIS package's copy does too. Both sides pinned
// against the same literal is what makes the pair actually catch a drift —
// pinning only one side leaves the other free to move silently, which is what a
// mutation of this constant proved.
func TestTheProviderExitCodeThisPackageRetriesOnIsPinned(t *testing.T) {
	if childExitProvider != 3 {
		t.Fatalf("childExitProvider is %d; internal/cli exits 3 for a provider failure, so a task "+
			"killed by one would no longer be recognised or retried", childExitProvider)
	}
}
