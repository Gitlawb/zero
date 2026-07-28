package specialist

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
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

// PlanRunner runs one task. The executor depends on this seam rather than on
// Executor directly so the budget, ordering and failure semantics are testable
// without launching child processes.
type PlanRunner func(ctx context.Context, task Task, tools []string) (TaskResult, error)

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

	for _, id := range plan.Order() {
		task := tasks[id]

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

		recordDispatched(recorder, task)
		started := time.Now()
		// BELT AND BRACES with the validator: the grant is intersected again
		// here, so a validation bug cannot widen a task's authority. Same shape
		// as Phase 1's forwardedReasoningEffort guard.
		result, err := run(ctx, task, planToolGrant(task, parentTools))
		result.ID = id
		if result.Duration == 0 {
			result.Duration = time.Since(started)
		}
		report.SequentialTotal += result.Duration
		budgetLeft -= result.Tokens
		report.TokensUsed += result.Tokens

		if err != nil || result.Outcome == TaskFailed {
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
	case report.Succeeded == 0:
		return PlanFailed
	case report.Failed == 0 && report.Skipped == 0:
		return PlanCompleted
	default:
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
func planToolGrant(task Task, parentTools []string) []string {
	parent := map[string]bool{}
	for _, name := range parentTools {
		parent[name] = true
	}
	if len(task.Tools) == 0 {
		out := []string{}
		for _, name := range parentTools {
			if planReadOnlyTools[name] {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}
	out := []string{}
	for _, name := range task.Tools {
		if !planReadOnlyTools[name] {
			continue
		}
		if len(parentTools) > 0 && !parent[name] {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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
	fmt.Fprintf(&b, "Plan %s: %d succeeded, %d failed, %d skipped.\n", report.Status, report.Succeeded, report.Failed, report.Skipped)
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
