package cli

import (
	"fmt"

	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/specialist"
)

// planSessionRecorder bridges plan lifecycle events onto the existing exec
// session recorder.
//
// IT PRESERVES THE BEST-EFFORT CONTRACT EXACTLY. execSessionRecorder.append
// returns nothing, latches its first error, and short-circuits every later
// call; warnIfRecordingFailed surfaces that once at run end. This type adds no
// error path of its own: every method returns nothing, so there is no way for a
// recording failure to reach ExecutePlan, and therefore no way for it to abort
// a plan mid-flight. A nil recorder is a no-op rather than a panic, for the
// same reason.
//
// The payloads are deliberately small and structured — ids, counts, durations,
// the terminal status and max_speedup — not whole task outputs. A plan's task
// outputs already reach the transcript through the tool result; duplicating
// them into the event log would multiply a large plan's on-disk size for no
// added recoverability.
type planSessionRecorder struct {
	recorder *execSessionRecorder
	// worst remembers the most serious non-completed plan status seen this run,
	// so the PROCESS exit code can reflect it. Asserting the executor's status
	// missed the tool's status, and asserting the tool's status would miss the
	// exit code the same way — this is the third link in that chain.
	worst       specialist.PlanStatus
	worstReason string
}

// Incomplete reports whether any plan this run did not fully complete, with a
// reason for the run-end message. A partial plan is work left undone, which is
// exactly what exitIncomplete exists for.
func (bridge *planSessionRecorder) Incomplete() (string, bool) {
	if bridge == nil || bridge.worst == "" || bridge.worst == specialist.PlanCompleted {
		return "", false
	}
	return bridge.worstReason, true
}

func (bridge *planSessionRecorder) append(eventType sessions.EventType, payload any) {
	// Nil-safe at every level: a nil bridge or a nil inner recorder simply does
	// not record. Recording must never be the thing that fails a run.
	if bridge == nil || bridge.recorder == nil {
		return
	}
	bridge.recorder.append(eventType, payload)
}

func (bridge *planSessionRecorder) PlanAdmitted(plan specialist.Plan) {
	tasks := make([]map[string]any, 0, plan.TaskCount())
	for _, task := range plan.Tasks() {
		tasks = append(tasks, map[string]any{
			"id": task.ID, "depends_on": task.DependsOn, "phase": task.Phase,
		})
	}
	bridge.append(sessions.EventPlanAdmitted, map[string]any{
		"name":       plan.Name(),
		"task_count": plan.TaskCount(),
		"order":      plan.Order(),
		"tasks":      tasks,
		"max_tokens": plan.Budget().MaxTokens,
	})
}

func (bridge *planSessionRecorder) TaskDispatched(task specialist.Task) {
	bridge.append(sessions.EventTaskDispatched, map[string]any{
		"id": task.ID, "depends_on": task.DependsOn,
	})
}

func (bridge *planSessionRecorder) TaskCompleted(result specialist.TaskResult) {
	bridge.append(sessions.EventTaskCompleted, map[string]any{
		"id": result.ID,
		// Duration is what the metric is computed from, so it is recorded per
		// task rather than only in the aggregate.
		"duration_ms": result.Duration.Milliseconds(),
		"session_id":  result.SessionID,
		"tokens":      result.Tokens,
	})
}

func (bridge *planSessionRecorder) TaskFailed(result specialist.TaskResult) {
	bridge.append(sessions.EventTaskFailed, map[string]any{
		"id": result.ID,
		// The outcome distinguishes a real failure from a dependency or budget
		// skip. A skipped task is RECORDED, never dropped.
		"outcome":     string(result.Outcome),
		"reason":      result.Err,
		"duration_ms": result.Duration.Milliseconds(),
	})
}

func (bridge *planSessionRecorder) PlanCompleted(plan specialist.Plan, report specialist.PlanReport) {
	if bridge != nil && report.Status != specialist.PlanCompleted {
		// PlanFailed outranks PlanPartial: if any plan failed outright, that is
		// the run's story.
		if bridge.worst != specialist.PlanFailed {
			bridge.worst = report.Status
			bridge.worstReason = fmt.Sprintf("plan %q ended %s: %d succeeded, %d failed, %d skipped",
				plan.Name(), report.Status, report.Succeeded, report.Failed, report.Skipped)
		}
	}
	bridge.append(sessions.EventPlanCompleted, map[string]any{
		"name":      plan.Name(),
		"status":    string(report.Status),
		"succeeded": report.Succeeded,
		"failed":    report.Failed,
		"skipped":   report.Skipped,
		// THE METRIC. Recorded so the kill criterion can be evaluated across
		// many sessions without re-running anything.
		"sequential_total_ms": report.SequentialTotal.Milliseconds(),
		"critical_path_ms":    report.CriticalPath.Milliseconds(),
		"max_speedup":         report.MaxSpeedup,
		"tokens_used":         report.TokensUsed,
	})
}
