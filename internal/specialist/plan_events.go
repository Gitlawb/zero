package specialist

import "github.com/Gitlawb/zero/internal/sessions"

// The plan lifecycle's session-event payloads, built in ONE place.
//
// There are two recorders — the headless one in internal/cli and the TUI's —
// and they must produce identical events for the same plan, because resume is a
// deterministic reduction over these events and a reducer cannot be written
// against two shapes. Two builders would drift (invariant 5), and the drift
// would only surface when someone resumed a plan recorded by the other surface.
//
// The payloads are deliberately small and structured — ids, counts, durations,
// terminal status — not whole task outputs. A plan's outputs already reach the
// transcript through the tool result; duplicating them here would multiply a
// large plan's on-disk size for no added recoverability.

// PlanAdmittedEvent is the record of a validated plan, written before its first
// task runs so a crash mid-plan still leaves the shape on disk.
func PlanAdmittedEvent(plan Plan) (sessions.EventType, map[string]any) {
	tasks := make([]map[string]any, 0, plan.TaskCount())
	for _, task := range plan.Tasks() {
		tasks = append(tasks, map[string]any{
			"id": task.ID, "depends_on": task.DependsOn, "phase": task.Phase,
		})
	}
	return sessions.EventPlanAdmitted, map[string]any{
		"name":       plan.Name(),
		"task_count": plan.TaskCount(),
		"order":      plan.Order(),
		"tasks":      tasks,
		"max_tokens": plan.Budget().MaxTokens,
	}
}

// TaskDispatchedEvent marks a task as started. Written BEFORE the child runs, so
// a task that was in flight when the process died is distinguishable on resume
// from one that never started.
func TaskDispatchedEvent(task Task) (sessions.EventType, map[string]any) {
	return sessions.EventTaskDispatched, map[string]any{
		"id": task.ID, "depends_on": task.DependsOn,
	}
}

// TaskCompletedEvent records a successful task, including the child session id
// so it stays drillable, and its spend so the per-task records add up to the
// plan's total.
func TaskCompletedEvent(result TaskResult) (sessions.EventType, map[string]any) {
	return sessions.EventTaskCompleted, map[string]any{
		"id":          result.ID,
		"duration_ms": result.Duration.Milliseconds(),
		"session_id":  result.SessionID,
		"tokens":      result.Tokens,
	}
}

// TaskFailedEvent records a task that failed, was skipped or was cancelled.
//
// It carries the SAME identity fields as TaskCompletedEvent. They diverged once
// — session_id and tokens were recorded only on success — and the effect was
// that the one task worth investigating was the one the log could not point at.
func TaskFailedEvent(result TaskResult) (sessions.EventType, map[string]any) {
	return sessions.EventTaskFailed, map[string]any{
		"id":          result.ID,
		"outcome":     string(result.Outcome),
		"reason":      result.Err,
		"duration_ms": result.Duration.Milliseconds(),
		"session_id":  result.SessionID,
		"tokens":      result.Tokens,
	}
}

// PlanCompletedEvent is the plan's terminal record.
func PlanCompletedEvent(plan Plan, report PlanReport) (sessions.EventType, map[string]any) {
	return sessions.EventPlanCompleted, map[string]any{
		"name":                plan.Name(),
		"status":              string(report.Status),
		"succeeded":           report.Succeeded,
		"failed":              report.Failed,
		"skipped":             report.Skipped,
		"cancelled":           report.Cancelled,
		"sequential_total_ms": report.SequentialTotal.Milliseconds(),
		"critical_path_ms":    report.CriticalPath.Milliseconds(),
		"max_speedup":         report.MaxSpeedup,
		"tokens_used":         report.TokensUsed,
	}
}
