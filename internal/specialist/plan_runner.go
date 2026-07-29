package specialist

import (
	"context"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
)

// PlanTaskContext is the per-RUN state a plan task inherits from its parent.
//
// It is captured at registration and does not change for the process's life —
// unlike the posture, which flips between runs and therefore lives behind a
// PostureGate pointer. Everything here is genuinely run-invariant (paths,
// workspace) or supplied per-call.
type PlanTaskContext struct {
	Executor Executor
	Cwd      string
	// PermissionMode / Depth describe the run issuing the plan, so a task
	// inherits exactly the parent's policy.
	//
	// ParentSessionID and ParentModel are DELIBERATELY NOT HERE. They looked
	// run-invariant and are not: the TUI builds this once per session while the
	// user can change model with /model between runs, so a value captured here
	// would be whatever was active at startup — and in practice both call sites
	// left them empty, so a plan task inherited no model at all. They arrive
	// per call on PlanTaskRequest instead, from the same tools.RunOptions the
	// Task tool reads.
	PermissionMode string
	Depth          int
	// SpecialistName is the read-only specialist each plan task runs as.
	SpecialistName string
}

// NewPlanRunner adapts Executor.Run into a PlanRunner.
//
// LIFETIME, deliberately: the returned closure captures only run-INVARIANT
// state — the executor, the workspace, the parent's identity and policy. It
// captures NO context. The ctx it uses is the one ExecutePlan hands it per
// task, which is the tool call's own context, so a cancelled run cancels the
// task in flight. Capturing a context at construction is precisely how the
// prototype's background goroutine kept running after cancellation, and a
// runner that outlived its plan would do it again.
//
// The runner does not outlive the plan in any meaningful sense either: it is
// synchronous, returns before ExecutePlan moves to the next task, and holds no
// goroutine of its own.
func NewPlanRunner(planCtx PlanTaskContext) PlanRunner {
	return func(ctx context.Context, req PlanTaskRequest) (TaskResult, error) {
		task, grantedTools := req.Task, req.Tools
		if ctx == nil {
			ctx = context.Background()
		}
		// Honour cancellation BEFORE launching a child: a cancelled plan must
		// not spend another task's budget.
		if err := ctx.Err(); err != nil {
			return TaskResult{Outcome: TaskFailed, Err: err.Error()}, err
		}

		// THE STALL WATCHDOG. Its clock resets on every event the child emits,
		// so a task that is working — however slowly — is never stopped; only
		// silence counts. The context it cancels is this task's alone, so a
		// wedged task does not take the plan with it.
		watchdog := newStallWatchdog(req.StallTimeout, nil)
		taskCtx, cancelTask := context.WithCancel(ctx)
		defer cancelTask()
		stopWatchdog := watchdog.watch(taskCtx, cancelTask)
		defer stopWatchdog()

		started := time.Now()
		manifest := planTaskManifest(planCtx.SpecialistName, grantedTools)
		res, err := planCtx.Executor.Run(taskCtx, TaskParameters{
			Name:        planCtx.SpecialistName,
			Prompt:      task.Prompt,
			Description: "plan task " + task.ID,
			Manifest:    &manifest,
		}, TaskRunOptions{
			// Per-call, from the tool's RunOptions — see PlanTaskRequest.
			ParentSessionID:       req.ParentSessionID,
			ParentModel:           req.ParentModel,
			ParentReasoningEffort: req.ParentReasoningEffort,
			CurrentDepth:          planCtx.Depth,
			Cwd:                   planCtx.Cwd,
			PermissionMode:        planCtx.PermissionMode,
			// THE SECOND HALF of the same defect. The Task tool forwards its
			// caller's progress callback here (task_tool.go); this path built
			// the same struct and omitted the field, so a plan task's child
			// streamed to nobody. Same class as finding 7 (ParentModel): a
			// second construction path that does not carry what the first one
			// did. Fixed with the tool-side half, not separately.
			Progress: watchedProgress(watchdog, req.Progress),
			// Explicitly NOT MemberAutonomy: Phase 2 tasks are read-only, and
			// granting it here would be the authority widening that was ruled
			// out as needing its own decision.
		})
		result := TaskResult{
			ID:        task.ID,
			Duration:  time.Since(started),
			SessionID: res.SessionID,
			Output:    res.Result.Output,
			// The meter the plan budget is spent from. A task whose stream
			// reported no usage costs 0 here, which is honest — but it means a
			// provider that never reports usage cannot be budget-bounded by
			// token count; MaxWall is the backstop in that case.
			Tokens: res.TotalTokens,
		}
		if watchdog.didFire() {
			// A stall and a user cancellation both surface as a context error;
			// they are not the same event and must not read the same.
			//
			// Stalled is what makes the task RETRYABLE, and it is set here rather
			// than inferred by the executor from the message: matching on error
			// text to decide whether to spend another child is exactly the shape
			// invariant 9 warns about, one layer up from errors.Is.
			result.Outcome = TaskFailed
			result.Stalled = true
			result.Err = stallError(task.ID, watchdog.timeout, 1).Error()
			return result, nil
		}
		if err != nil {
			result.Outcome = TaskFailed
			result.Err = err.Error()
			return result, err
		}
		if res.Result.Status == tools.StatusError {
			// The child ran but its task FAILED. Surfacing it as success would
			// let a plan report work that did not happen.
			result.Outcome = TaskFailed
			result.Err = res.Result.Output
			return result, nil
		}
		result.Outcome = TaskSucceeded
		return result, nil
	}
}

// planTaskManifest builds the inline manifest a plan task runs under. The tool
// list is the ALREADY-INTERSECTED grant ExecutePlan computed, so this cannot
// widen it — it only carries it.
func planTaskManifest(name string, grantedTools []string) Manifest {
	if strings.TrimSpace(name) == "" {
		name = "explorer"
	}
	return Manifest{
		Metadata: Metadata{
			Name:        name,
			Description: "Read-only plan task.",
			Tools:       grantedTools,
		},
		SystemPrompt: "You are executing one task of a larger plan. You have read-only tools. " +
			"Complete exactly the task described and report what you found; do not attempt to modify anything.",
		Location: LocationBuiltin,
		FilePath: "(plan)",
		// AUTHORITATIVE, not a hint: this is the already-intersected grant, and
		// an empty one must refuse the child rather than expand to the default
		// read-only category. ExecutePlan refuses before reaching here, so this
		// is the second layer.
		ResolvedTools: grantedTools,
		ToolsResolved: true,
	}
}
