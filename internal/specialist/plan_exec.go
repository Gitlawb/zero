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
	// Stalled reports that the stall watchdog stopped this task — the child
	// emitted nothing for the whole timeout. Set by the runner, read by the
	// executor to decide whether the task is worth another attempt.
	//
	// A FLAG, not a message match: deciding to spend another child by looking
	// for a phrase in Err is the same class as comparing errors with == instead
	// of errors.Is, which is what silently disabled every stall retry in the
	// prototype.
	Stalled bool
	// Attempts is how many times the task ran, always at least 1 for a task that
	// was dispatched. Duration and Tokens are the TOTALS across those attempts,
	// because what the plan spent is what the plan spent.
	Attempts int
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
	// Workers is how many tasks this plan ACTUALLY ran at once, and
	// WorkersRequested is what it asked for. Both, because the machine's
	// capacity may be lower than the request and a plan that asked for sixteen
	// and ran six has not been given sixteen — reporting only the request would
	// make the number a fiction, which is the same reason max_workers is
	// rejected outside its range rather than trimmed into it.
	Workers          int
	WorkersRequested int
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
	// Cwd overrides where this task runs. Empty means the parent's workspace,
	// which is every read-only plan. A write-capable plan sets it to its
	// isolated worktree, and it is carried per REQUEST rather than captured in
	// PlanTaskContext because the workspace belongs to a plan, not to the
	// process — the same reason ParentModel lives here.
	Cwd string
	// StallTimeout bounds how long this task may emit nothing. Resolved by
	// ExecutePlan from the plan's budget so every task in a plan shares one
	// answer, rather than each runner re-deriving it.
	StallTimeout time.Duration
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
	return ExecutePlanIn(ctx, plan, PlanWorkspace{}, parentTools, run, recorder)
}

// ExecutePlanIn is ExecutePlan with the plan's WORKSPACE named. ExecutePlan is
// the read-only case — the workspace a plan does not need — kept as the name
// every existing caller and test already uses.
func ExecutePlanIn(ctx context.Context, plan Plan, workspace PlanWorkspace, parentTools []string, run PlanRunner, recorder PlanRecorder) PlanReport {
	// THE PLAN'S OWN CONTEXT, derived here rather than by the caller.
	//
	// Cancelling it abandons the PLAN and leaves the TURN alive; cancelling the
	// run still cancels this too, because it is a child. Deriving it here rather
	// than in the orchestrate tool is the difference between one call path
	// having per-plan cancellation and every call path having it — this is the
	// function that owns a plan's lifetime, and the seam belongs where the
	// lifetime is.
	ctx, cancelPlan := context.WithCancel(ctx)
	defer cancelPlan()
	planRunning(recorder, cancelPlan)

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
	// budgetLeft is only a bound when the plan asked for one. Zero means
	// unbounded: spend is metered and reported, not gated.
	budgetLeft := plan.Budget().MaxTokens
	bounded := budgetLeft > 0
	budgetExhausted := false

	deadline := time.Time{}
	if wall := plan.Budget().MaxWall; wall > 0 {
		deadline = time.Now().Add(wall)
	}
	// One stall timeout for the whole plan, resolved once.
	stallTimeout := stallTimeoutFor(plan.Budget())

	cancelled := false

	// THE WORKER POOL. One worker is the sequential path: every wait below
	// becomes "until the previous task finished", and the walk applies the same
	// checks in the same order it always has.
	workers := effectivePlanWorkers(plan.Budget().MaxWorkers)
	report.Workers = workers
	report.WorkersRequested = plan.Budget().MaxWorkers
	slots := newPlanSlots(workers)

	// harvest applies one completed dispatch: its spend, its outcome, its
	// record. Called only from THIS goroutine, so results, failed, report and
	// budgetLeft are never touched concurrently and need no lock — the
	// concurrency is in the dispatches, not in the bookkeeping.
	harvest := func(completion taskCompletion) {
		slots.release()
		id, result, err := completion.id, completion.result, completion.err
		result.ID = id
		if result.Duration == 0 {
			result.Duration = time.Since(completion.started)
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
				return
			}
			result.Outcome = TaskFailed
			if result.Err == "" && err != nil {
				result.Err = err.Error()
			}
			results[id] = result
			failed[id] = true
			report.Failed++
			recordFailed(recorder, result)
			return
		}
		result.Outcome = TaskSucceeded
		results[id] = result
		report.Succeeded++
		recordCompleted(recorder, result)
	}

	// resolved reports whether every dependency of a task has finished, one way
	// or another. A task waits for its dependencies to RESOLVE, not to succeed:
	// a failed dependency resolves it too, and the skip check below turns that
	// into the recorded dependency_failed.
	resolved := func(task Task) bool {
		for _, dep := range task.DependsOn {
			if _, done := results[dep]; !done {
				return false
			}
		}
		return true
	}

	for _, id := range plan.Order() {
		task := tasks[id]

		// WAIT FOR THIS TASK'S TURN: until its dependencies have resolved and a
		// worker is free. Waiting on the task the WALK reached — rather than
		// picking whichever ready task appeared first — is what keeps dispatch
		// in the plan's validated order, so one worker reproduces the sequential
		// executor exactly and more workers only overlap what was already
		// adjacent.
		for slots.busy() && (!resolved(task) || slots.full()) {
			harvest(<-slots.done)
		}

		// PAUSE, at the task boundary, BEFORE the cancellation check — so a user
		// who stops a paused plan is not left waiting for a resume that will
		// never come. WaitWhilePaused returns on ctx, and the check below then
		// turns it into a cancellation exactly as if the plan had been running.
		waitWhilePaused(recorder, ctx)

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
		if budgetExhausted || (bounded && budgetLeft <= 0) || (!deadline.IsZero() && time.Now().After(deadline)) {
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
		policy := retryPolicy{
			task:         task,
			tools:        granted,
			cwd:          workspace.Path,
			stallTimeout: stallTimeout,
			maxRetries:   plan.Budget().MaxRetries,
			deadline:     deadline,
		}
		slots.take()
		started := time.Now()
		go func(id string) {
			// Every goroutine gets recover(): a panic in one task must not take
			// the plan with it, and the slot must be returned either way or the
			// scheduler waits forever on a worker that will never report.
			defer func() {
				if panicked := recover(); panicked != nil {
					slots.done <- taskCompletion{
						id: id, started: started,
						result: TaskResult{Outcome: TaskFailed, Err: fmt.Sprintf("task panicked: %v", panicked)},
					}
				}
			}()
			result, err := runTaskWithRetries(ctx, policy, run)
			slots.done <- taskCompletion{id: id, result: result, err: err, started: started}
		}(id)
	}

	// DRAIN. Everything still in flight is harvested before the report is
	// assembled, or a plan would report on tasks that had not finished.
	for slots.busy() {
		harvest(<-slots.done)
	}

	for _, id := range plan.Order() {
		report.Tasks = append(report.Tasks, results[id])
	}
	report.CriticalPath = criticalPath(plan, results)
	report.MaxSpeedup = speedup(report.SequentialTotal, report.CriticalPath)
	report.Status = terminalStatus(report)
	return report
}

// retryPolicy is one task's retry inputs. A struct for the same reason
// PlanTaskRequest is one: this parameter list is the thing that grows, and a
// positional list is where a second call site quietly omits a field.
type retryPolicy struct {
	task         Task
	tools        []string
	cwd          string
	stallTimeout time.Duration
	maxRetries   int
	deadline     time.Time
}

// runTaskWithRetries runs one task, retrying it ONLY when it stalled.
//
// The retry lives HERE, in the executor, and not in the runner — the executor
// owns the budget, the wall deadline, cancellation and the record, and a retry
// hidden inside the runner would spend a second child's tokens without any of
// them counting. Duration and Tokens come back as the TOTAL across attempts,
// so a plan's reported spend is its real spend.
//
// Every reason to stop is checked BEFORE launching another child: a cancelled
// run, an expired wall deadline, or an attempt budget that is used up. The
// prototype's equivalent loop retried a cancelled task, which turned Ctrl-C into
// another spawn.
func runTaskWithRetries(ctx context.Context, policy retryPolicy, run PlanRunner) (TaskResult, error) {
	var result TaskResult
	var err error
	var totalDuration time.Duration
	var totalTokens int

	for attempt := 1; ; attempt++ {
		started := time.Now()
		result, err = run(ctx, PlanTaskRequest{
			Task:         policy.task,
			Tools:        policy.tools,
			Cwd:          policy.cwd,
			StallTimeout: policy.stallTimeout,
		})
		if result.Duration == 0 {
			result.Duration = time.Since(started)
		}
		totalDuration += result.Duration
		totalTokens += result.Tokens
		result.Attempts = attempt
		result.Duration = totalDuration
		result.Tokens = totalTokens

		switch {
		case !result.Stalled:
			// Anything other than a stall is an ANSWER, including a failure. The
			// child ran and reported; running it again buys the same report.
			return result, err
		case attempt > policy.maxRetries:
			// Out of attempts. Restate the failure with the count, because
			// "stalled" and "stalled on every one of three attempts" call for
			// different responses.
			result.Err = stallError(policy.task.ID, policy.stallTimeout, attempt).Error()
			return result, err
		case ctx.Err() != nil:
			// The run was stopped. NOT retried, and not relabelled either — the
			// executor's own cancellation handling turns this into TaskCancelled.
			return result, err
		case !policy.deadline.IsZero() && time.Now().After(policy.deadline):
			// The plan's wall budget is gone. Another attempt would overrun it
			// on behalf of the task that already exhausted it.
			result.Err = stallError(policy.task.ID, policy.stallTimeout, attempt).Error()
			return result, err
		}
	}
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
		// THE DEFAULT STAYS READ-ONLY even though a named write tool is now
		// permitted. A task that asked for nothing must not inherit the ability
		// to change things — writing is opted into per task, by name, or every
		// unqualified task in every plan silently becomes write-capable the day
		// the parent grant widens.
		for _, name := range parentTools {
			if planReadOnlyTools[name] {
				out = append(out, name)
			}
		}
	} else {
		for _, name := range task.Tools {
			// THE PARENT'S GRANT, unconditionally. The read-only half of this
			// check moved out with its sibling in validateTaskTools — a named
			// write tool is now permitted — and the two had to move TOGETHER:
			// admission permitting what dispatch drops would produce a task that
			// validated and then ran with less than it asked for, silently.
			//
			// The old "only check the parent when it supplied a list" form was
			// what let a task widen its authority whenever the grant was unwired,
			// and that half is untouched.
			// The same two bounds as admission, in the same order: grantable at
			// all, then held by the parent. Dropping rather than refusing is
			// what makes this the narrowing layer.
			if !planReadOnlyTools[name] && !planWriteTools[name] {
				continue
			}
			if !parent[name] {
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
		// A retried task cost more than its one line suggests. Stated only when
		// it happened, so the common case stays unchanged.
		if task.Attempts > 1 {
			fmt.Fprintf(&b, " (%d attempts)", task.Attempts)
		}
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

// PlanTaskProgressRecorder is the optional PER-TASK streaming half of a
// recorder.
//
// It exists because a child's stream events carry no task identity. The agent
// loop's progress callback holds the parent's TOOL-CALL id, which is the same
// for every task in a plan, so a display attributing those events could only
// guess — and the guess it made was "whichever task was dispatched last". That
// was sound while exactly one task ran at a time and became a lie the moment
// two could.
//
// The recorder already knows which card belongs to which task, because it
// opened them. So the identity travels with the event from the one place that
// has it, instead of being reconstructed at the other end from a guess.
type PlanTaskProgressRecorder interface {
	TaskProgress(taskID string, event streamjson.Event)
}

// planTaskProgress is best-effort and nil-safe, like every other recorder call.
func planTaskProgress(recorder PlanRecorder, taskID string, event streamjson.Event) {
	if progress, ok := recorder.(PlanTaskProgressRecorder); ok && progress != nil {
		progress.TaskProgress(taskID, event)
	}
}

// PlanController is the optional CONTROL half of a recorder.
//
// Stopping a plan meant stopping the whole turn: Ctrl-C cancels the run, and
// there was no way to abandon a twenty-task plan while keeping the conversation.
// The surface that displays a plan is the one a user asks to stop it, so control
// arrives through the same seam the display does — type-asserted exactly like
// PlanLifecycleRecorder, so a recorder that only records is unaffected and no
// existing signature changes.
type PlanController interface {
	// PlanRunning hands the surface a cancel scoped to THIS PLAN, not to the
	// run that issued it. Called once before the first task; the surface drops
	// it when the plan ends, so a later stop cannot cancel a context that has
	// already been reused.
	PlanRunning(cancel context.CancelFunc)
	// WaitWhilePaused blocks at a TASK BOUNDARY while the user has paused.
	//
	// A boundary, not mid-task, and that is the honest limit: a child process
	// already talking to a provider cannot be suspended, and pretending
	// otherwise would mean "paused" while tokens kept being spent. It must
	// return when ctx is done, or stopping a paused plan would deadlock.
	WaitWhilePaused(ctx context.Context)
}

// PlanSurfaceBusy is the optional CONCURRENCY half of a recorder: the surface
// says whether it is already carrying a plan.
//
// ONE PLAN PER SURFACE, and it is not a policy choice — it is what the display
// can actually represent. The panel holds one plan, the sidebar's PLAN section
// draws one plan, and the card table that pairs a task with its row is keyed by
// TASK ID, which is unique within a plan and not between two. Two plans sharing
// an id as ordinary as "tests" makes one plan's completion close the other
// plan's row, and leaves a card that nothing can ever close spinning in AGENTS
// for the rest of the session.
//
// The TUI already refused this on the path a USER drives (/plans restart), with
// a comment saying exactly why. It did not refuse it on the path the MODEL
// drives, and the model reaches it by launching a background plan — which
// returns immediately, by design — and then calling orchestrate again.
type PlanSurfaceBusy interface {
	// RunningPlanName reports the plan this surface is already carrying. The
	// name is for the refusal message; the bool is the answer.
	RunningPlanName() (string, bool)
}

// runningPlanOn asks the surface whether it is free, best-effort and nil-safe
// like every other optional half. A recorder that cannot answer is treated as
// free: the headless path runs one plan per process and has no surface to
// contend for.
func runningPlanOn(recorder PlanRecorder) (string, bool) {
	if busy, ok := recorder.(PlanSurfaceBusy); ok && busy != nil {
		return busy.RunningPlanName()
	}
	return "", false
}

// planRunning and waitWhilePaused are best-effort and nil-safe, mirroring
// recordPlanAdmitted: a recorder that does not implement control simply cannot
// be asked to control anything.
func planRunning(recorder PlanRecorder, cancel context.CancelFunc) {
	if controller, ok := recorder.(PlanController); ok && controller != nil {
		controller.PlanRunning(cancel)
	}
}

func waitWhilePaused(recorder PlanRecorder, ctx context.Context) {
	if controller, ok := recorder.(PlanController); ok && controller != nil {
		controller.WaitWhilePaused(ctx)
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
