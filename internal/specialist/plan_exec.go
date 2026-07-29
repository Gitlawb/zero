package specialist

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/streamjson"
)

// PlanStatus is a plan's terminal state.
//
// PARTIAL IS ITS OWN STATUS, with its own exit code. This repo has repeatedly
// reported failure as success (audit RC-F), and in a plan that is worse:
// nineteen of twenty tasks failing must never surface as a completed run.
type PlanStatus string

const (
	// PlanCompleted: every task succeeded.
	PlanCompleted PlanStatus = "completed"
	// PlanPartial: at least one task succeeded and at least one failed, was
	// skipped, or was cut off by the budget. Maps to exit 4 (exitIncomplete).
	PlanPartial PlanStatus = "partial"
	// PlanFailed: no task succeeded.
	PlanFailed PlanStatus = "failed"
	// PlanCancelled: the run was stopped and nothing had succeeded yet. Its own
	// status so a deliberate stop is never reported as a failure.
	PlanCancelled PlanStatus = "cancelled"
)

// TaskOutcome is why a task ended the way it did.
type TaskOutcome string

const (
	TaskSucceeded TaskOutcome = "succeeded"
	TaskFailed    TaskOutcome = "failed"
	// TaskSkippedDependency: a dependency failed, so this never ran. RECORDED,
	// never silently dropped — a task that vanished from the report would be
	// indistinguishable from one that was never planned.
	TaskSkippedDependency TaskOutcome = "dependency_failed"
	// TaskSkippedBudget: the plan's token budget was exhausted before this
	// task could be dispatched.
	TaskSkippedBudget TaskOutcome = "budget_exhausted"
	// TaskCancelled: the run was cancelled before this task finished, or before
	// it started. ITS OWN OUTCOME, not a failure — cancelling a twenty-task
	// plan used to mark every remaining task "failed" with "context canceled",
	// so a deliberate Ctrl-C read as nineteen defects. Nothing failed; the user
	// stopped it.
	TaskCancelled TaskOutcome = "cancelled"
)

// TaskResult is one task's record.
type TaskResult struct {
	ID       string
	Outcome  TaskOutcome
	Duration time.Duration
	// Output is the task's FULL result, verbatim. Not truncated here: the tool
	// boundary budgets and redacts it exactly like any other tool output.
	Output    string
	Err       string
	SessionID string
	Tokens    int
}

// PlanReport is the plan's terminal record, carried in plan_completed.
type PlanReport struct {
	Status    PlanStatus
	Tasks     []TaskResult
	Succeeded int
	Failed    int
	Skipped   int
	// Cancelled counts tasks stopped by the user rather than broken. Kept
	// separate from Failed so the summary and the panel can say so.
	Cancelled int
	// SequentialTotal is the sum of task durations — what this run actually
	// spent.
	SequentialTotal time.Duration
	// CriticalPath is the longest dependency-weighted path through the DAG: the
	// wall time a perfectly parallel run could not go below.
	CriticalPath time.Duration
	// MaxSpeedup is SequentialTotal / CriticalPath — the THEORETICAL ceiling on
	// what fan-out could buy, measured from a sequential run with no writes.
	//
	// This number decides whether Phase 3 is built. The kill criterion is a
	// median of >= 2.0 across >= 20 real plans: below that the ceiling is too
	// low for real coordination overhead to fit underneath, and concurrency
	// would cost more than it returns.
	//
	// It answers VALUE, not safety. Independence-violation would answer safety,
	// but read-only tasks are always safe to parallelise, so that rate would be
	// 0 regardless and would decide nothing.
	MaxSpeedup float64
	TokensUsed int
}

// PlanTaskRequest is everything one task needs to run.
//
// A STRUCT, not a widening parameter list. The runner's inputs are the thing
// that grows every stage — 2a adds Progress, 2b will add background wiring —
// and each addition through a positional parameter is another chance for a
// second construction path to omit a field the first one carried. That is
// exactly the class that produced findings 1 and 7: a caller that forgets a
// field compiles fine and fails silently.
type PlanTaskRequest struct {
	// Task is the validated task to run.
	Task Task
	// Tools is the already-intersected grant. Never widened downstream.
	Tools []string
	// Progress, when set, receives each stream-json event the task's child
	// emits. nil is a no-op — the behaviour for every caller that does not wire
	// live progress.
	Progress func(streamjson.Event)
	// ParentSessionID / ParentModel / ParentReasoningEffort identify the run
	// issuing the plan, so a task runs on the SAME model its parent is running
	// on rather than whatever the child's own config resolves to.
	//
	// Per call, not per registration: the TUI's registry is built once while
	// /model can change the model between runs. Attached by the orchestrate
	// tool from tools.RunOptions, which is where the Task tool reads the same
	// three values.
	ParentSessionID       string
	ParentModel           string
	ParentReasoningEffort string
}

// PlanRunner runs one task. The executor depends on this seam rather than on
// Executor directly so the budget, ordering and failure semantics are testable
// without launching child processes.
type PlanRunner func(ctx context.Context, req PlanTaskRequest) (TaskResult, error)

// PlanRecorder receives plan lifecycle events. Recording is BEST-EFFORT and
// must never fail the run — it mirrors execSessionRecorder.append's contract,
// where a latched error is surfaced once but the run continues.
type PlanRecorder interface {
	TaskDispatched(task Task)
	TaskCompleted(result TaskResult)
	TaskFailed(result TaskResult)
}

// ExecutePlan runs a plan SEQUENTIALLY in its validated topological order.
//
// The order comes from the same Kahn pass that proved the graph acyclic, so
// admission and execution cannot disagree about it.
func ExecutePlan(ctx context.Context, plan Plan, parentTools []string, run PlanRunner, recorder PlanRecorder) PlanReport {
	tasks := map[string]Task{}
	for _, task := range plan.Tasks() {
		tasks[task.ID] = task
	}

	report := PlanReport{}
	results := map[string]TaskResult{}
	failed := map[string]bool{}
	// budgetLeft is decremented as tasks complete. Enforced HERE, at dispatch —
	// validation alone would be a promise, not a bound. Under the zeromaxing
	// posture every child inherits a 320-turn ceiling, so a twenty-task plan
	// authorises 6,400 child turns from a single tool call; this is what stands
	// between that number and the user's bill.
	budgetLeft := plan.Budget().MaxTokens
	budgetExhausted := false

	deadline := time.Time{}
	if wall := plan.Budget().MaxWall; wall > 0 {
		deadline = time.Now().Add(wall)
	}

	cancelled := false
	for _, id := range plan.Order() {
		task := tasks[id]

		// Cancellation is checked FIRST and recorded as its own outcome. Once
		// the run is cancelled every remaining task is cancelled too — they are
		// not blocked by a dependency and the budget did not run out.
		if cancelled || ctx.Err() != nil {
			cancelled = true
			result := TaskResult{
				ID:      id,
				Outcome: TaskCancelled,
				Err:     "cancelled: the run was stopped before this task ran",
			}
			results[id] = result
			failed[id] = true
			report.Cancelled++
			recordFailed(recorder, result)
			continue
		}

		if blocker, blocked := firstFailedDependency(task, failed); blocked {
			result := TaskResult{
				ID:      id,
				Outcome: TaskSkippedDependency,
				Err:     fmt.Sprintf("skipped: dependency %q did not succeed", blocker),
			}
			results[id] = result
			failed[id] = true // its own dependents are blocked too
			report.Skipped++
			recordFailed(recorder, result)
			continue
		}

		// A budget-exhausted or timed-out plan SKIPS the rest rather than
		// aborting: independent work already done still counts, and the record
		// must show what was not attempted.
		if budgetExhausted || budgetLeft <= 0 || (!deadline.IsZero() && time.Now().After(deadline)) {
			budgetExhausted = true
			result := TaskResult{
				ID:      id,
				Outcome: TaskSkippedBudget,
				Err:     "skipped: the plan's budget was exhausted before this task ran",
			}
			results[id] = result
			failed[id] = true
			report.Skipped++
			recordFailed(recorder, result)
			continue
		}

		// BELT AND BRACES with the validator: the grant is intersected again
		// here, so a validation bug cannot widen a task's authority. Same shape
		// as Phase 1's forwardedReasoningEffort guard. Computed BEFORE dispatch
		// is recorded: a task that cannot be granted anything was never
		// dispatched, and the event log must not say it was.
		granted, grantErr := planToolGrant(task, parentTools)
		if grantErr != nil {
			result := TaskResult{ID: id, Outcome: TaskFailed, Err: grantErr.Error()}
			results[id] = result
			failed[id] = true
			report.Failed++
			recordFailed(recorder, result)
			continue
		}

		recordDispatched(recorder, task)
		started := time.Now()
		result, err := run(ctx, PlanTaskRequest{Task: task, Tools: granted})
		result.ID = id
		if result.Duration == 0 {
			result.Duration = time.Since(started)
		}
		report.SequentialTotal += result.Duration
		budgetLeft -= result.Tokens
		report.TokensUsed += result.Tokens

		if err != nil || result.Outcome == TaskFailed {
			// A task cut short by cancellation is CANCELLED, not failed. The
			// distinction survives all the way to the terminal status, so a
			// stopped plan never reports as a broken one.
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				cancelled = true
				result.Outcome = TaskCancelled
				if result.Err == "" {
					result.Err = "cancelled: the run was stopped while this task was running"
				}
				results[id] = result
				failed[id] = true
				report.Cancelled++
				recordFailed(recorder, result)
				continue
			}
			result.Outcome = TaskFailed
			if result.Err == "" && err != nil {
				result.Err = err.Error()
			}
			results[id] = result
			failed[id] = true
			report.Failed++
			recordFailed(recorder, result)
			continue
		}
		result.Outcome = TaskSucceeded
		results[id] = result
		report.Succeeded++
		recordCompleted(recorder, result)
	}

	for _, id := range plan.Order() {
		report.Tasks = append(report.Tasks, results[id])
	}
	report.CriticalPath = criticalPath(plan, results)
	report.MaxSpeedup = speedup(report.SequentialTotal, report.CriticalPath)
	report.Status = terminalStatus(report)
	return report
}

// terminalStatus maps counts onto the three terminal states. Partial is its own
// status precisely so a mostly-failed plan can never be reported as success.
func terminalStatus(report PlanReport) PlanStatus {
	switch {
	case report.Succeeded == 0 && report.Cancelled > 0:
		// Stopped before anything finished. Not a failure — nothing broke.
		return PlanCancelled
	case report.Succeeded == 0:
		return PlanFailed
	case report.Failed == 0 && report.Skipped == 0 && report.Cancelled == 0:
		return PlanCompleted
	default:
		// Cancelled MUST be part of this condition. Without it a plan with two
		// successes and two cancellations reported "completed" — work that
		// never ran, reported as done, which is RC-F exactly.
		return PlanPartial
	}
}

// firstFailedDependency names the dependency that blocked a task, so the record
// says WHICH one rather than just that something upstream broke.
func firstFailedDependency(task Task, failed map[string]bool) (string, bool) {
	deps := append([]string(nil), task.DependsOn...)
	sort.Strings(deps)
	for _, dep := range deps {
		if failed[dep] {
			return dep, true
		}
	}
	return "", false
}

// planToolGrant intersects a task's requested tools with the parent's grant.
// An empty request inherits the parent's read-only grant; it never widens it.
//
// It REFUSES an empty result rather than returning one. An empty grant used to
// be handed on to the manifest, where an empty tool list read as "unspecified"
// and expanded to the default read-only category — so the narrower the parent,
// the wider the child. Returning an error keeps the empty case from ever
// reaching a place that has to guess what it meant. See Manifest.ToolsResolved
// for the other half of that fix.
func planToolGrant(task Task, parentTools []string) ([]string, error) {
	parent := map[string]bool{}
	for _, name := range parentTools {
		parent[name] = true
	}
	out := []string{}
	if len(task.Tools) == 0 {
		for _, name := range parentTools {
			if planReadOnlyTools[name] {
				out = append(out, name)
			}
		}
	} else {
		for _, name := range task.Tools {
			// UNCONDITIONAL on both sides: read-only AND held by the parent.
			// The old "only check the parent when it supplied a list" form was
			// what let a task widen its authority whenever the grant was
			// unwired.
			if !planReadOnlyTools[name] || !parent[name] {
				continue
			}
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf(
			"task %q resolved no tools it may use: this run holds no read-only tools a plan task can inherit (parent grant: %s)",
			task.ID, describeGrant(parentTools))
	}
	sort.Strings(out)
	return out, nil
}

// describeGrant renders a parent grant for an error message, naming the empty
// case explicitly so the reason is never a blank space.
func describeGrant(parentTools []string) string {
	if len(parentTools) == 0 {
		return "none"
	}
	sorted := append([]string(nil), parentTools...)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}

// criticalPath is the longest dependency-weighted path through the DAG: for
// each task, its own duration plus the longest path among its dependencies.
// Computed over the validated topological order, so every dependency is already
// resolved when a task is reached.
func criticalPath(plan Plan, results map[string]TaskResult) time.Duration {
	longest := map[string]time.Duration{}
	tasks := map[string]Task{}
	for _, task := range plan.Tasks() {
		tasks[task.ID] = task
	}
	var best time.Duration
	for _, id := range plan.Order() {
		var upstream time.Duration
		for _, dep := range tasks[id].DependsOn {
			if longest[dep] > upstream {
				upstream = longest[dep]
			}
		}
		longest[id] = upstream + results[id].Duration
		if longest[id] > best {
			best = longest[id]
		}
	}
	return best
}

// speedup is sequential / critical-path, guarded against a zero denominator (a
// plan whose tasks all took no measurable time).
func speedup(sequential, critical time.Duration) float64 {
	if critical <= 0 {
		return 0
	}
	return float64(sequential) / float64(critical)
}

// Summary renders the plan's result for the model, including max_speedup — the
// number the Phase 3 decision rests on, surfaced rather than buried in an event.
func (report PlanReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan %s: %d succeeded, %d failed, %d skipped", report.Status, report.Succeeded, report.Failed, report.Skipped)
	if report.Cancelled > 0 {
		fmt.Fprintf(&b, ", %d cancelled", report.Cancelled)
	}
	b.WriteString(".\n")
	fmt.Fprintf(&b, "sequential total: %s · critical path: %s · max_speedup: %.2fx\n",
		report.SequentialTotal.Round(time.Millisecond), report.CriticalPath.Round(time.Millisecond), report.MaxSpeedup)
	for _, task := range report.Tasks {
		fmt.Fprintf(&b, "\n  - %s [%s] %s", task.ID, task.Outcome, task.Duration.Round(time.Millisecond))
		if task.Output != "" {
			b.WriteString("\n      result:\n" + task.Output)
		}
		if task.Err != "" {
			b.WriteString("\n      error:\n" + task.Err)
		}
	}
	return b.String()
}

func recordDispatched(recorder PlanRecorder, task Task) {
	if recorder != nil {
		recorder.TaskDispatched(task)
	}
}

func recordCompleted(recorder PlanRecorder, result TaskResult) {
	if recorder != nil {
		recorder.TaskCompleted(result)
	}
}

func recordFailed(recorder PlanRecorder, result TaskResult) {
	if recorder != nil {
		recorder.TaskFailed(result)
	}
}

// PlanLifecycleRecorder extends PlanRecorder with the plan-level events. Kept
// separate so a caller that only wants task events need not implement both.
type PlanLifecycleRecorder interface {
	PlanRecorder
	PlanAdmitted(plan Plan)
	PlanCompleted(plan Plan, report PlanReport)
}

// recordPlanAdmitted and recordPlanCompleted are best-effort and nil-safe, and
// they type-assert rather than requiring the wider interface, so recording can
// never be the thing that fails a run.
func recordPlanAdmitted(recorder PlanRecorder, plan Plan) {
	if full, ok := recorder.(PlanLifecycleRecorder); ok && full != nil {
		full.PlanAdmitted(plan)
	}
}

func recordPlanCompleted(recorder PlanRecorder, plan Plan, report PlanReport) {
	if full, ok := recorder.(PlanLifecycleRecorder); ok && full != nil {
		full.PlanCompleted(plan, report)
	}
}
