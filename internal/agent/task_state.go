package agent

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/trace"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type taskStatus string

const (
	taskStatusActive     taskStatus = "active"
	taskStatusComplete   taskStatus = "complete"
	taskStatusIncomplete taskStatus = "incomplete"
)

type taskPlanParity string

const (
	taskPlanParityUnknown  taskPlanParity = "unknown"
	taskPlanParityMatch    taskPlanParity = "match"
	taskPlanParityMismatch taskPlanParity = "mismatch"
)

type taskPlanItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
	Notes   string `json:"notes,omitempty"`
}

type taskPlanState struct {
	Items      []taskPlanItem `json:"items,omitempty"`
	Pending    int            `json:"pending"`
	InProgress int            `json:"in_progress"`
	Completed  int            `json:"completed"`
	Failed     int            `json:"failed"`
}

type taskToolState struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

type taskVerificationState struct {
	Passed      int     `json:"passed"`
	Failed      int     `json:"failed"`
	LastOutcome Outcome `json:"last_outcome,omitempty"`
}

type taskStateSnapshot struct {
	Revision           int                   `json:"revision"`
	Objective          string                `json:"objective"`
	Status             taskStatus            `json:"status"`
	Plan               taskPlanState         `json:"plan"`
	Tools              taskToolState         `json:"tools"`
	Verification       taskVerificationState `json:"verification"`
	ChangedFiles       []string              `json:"changed_files,omitempty"`
	CompletionDecision CompletionDecision    `json:"completion_decision,omitempty"`
	CompletionReason   string                `json:"completion_reason,omitempty"`
	PlanParity         taskPlanParity        `json:"plan_parity"`
}

type taskStateEventKind int

const (
	taskStateEventPlan taskStateEventKind = iota + 1
	taskStateEventToolResult
	taskStateEventVerification
	taskStateEventCompletion
)

type taskStateEvent struct {
	kind         taskStateEventKind
	arguments    string
	toolResult   ToolResult
	verification Outcome
	completion   completionEvaluation
}

// taskState is a deterministic projection of facts the loop already observes.
// It does not initiate work or replace the transcript; consumers must check
// transcript parity before using its compact representation.
type taskState struct {
	snapshotValue taskStateSnapshot
	changedFiles  map[string]struct{}
	recorder      *trace.Recorder
	planObserved  bool
}

func newTaskState(objective string, recorder *trace.Recorder) *taskState {
	state := &taskState{
		snapshotValue: taskStateSnapshot{
			Objective:  strings.TrimSpace(objective),
			Status:     taskStatusActive,
			PlanParity: taskPlanParityUnknown,
		},
		changedFiles: map[string]struct{}{},
	}
	state.recorder = recorder
	return state
}

func (state *taskState) observe(event taskStateEvent) bool {
	if state == nil {
		return false
	}
	changed := false
	switch event.kind {
	case taskStateEventPlan:
		plan, ok := parseTaskPlan(event.arguments)
		if !ok {
			return false
		}
		state.snapshotValue.Plan = summarizeTaskPlan(plan)
		state.planObserved = true
		state.markActive()
		changed = true
	case taskStateEventToolResult:
		state.markActive()
		if event.toolResult.Status == tools.StatusOK {
			state.snapshotValue.Tools.Succeeded++
		} else {
			state.snapshotValue.Tools.Failed++
		}
		for _, path := range event.toolResult.ChangedFiles {
			path = strings.TrimSpace(path)
			if path != "" {
				state.changedFiles[path] = struct{}{}
			}
		}
		state.snapshotValue.ChangedFiles = sortedKeys(state.changedFiles)
		changed = true
	case taskStateEventVerification:
		if event.verification == OutcomeDisabled {
			return false
		}
		state.markActive()
		state.snapshotValue.Verification.LastOutcome = event.verification
		switch event.verification {
		case OutcomePassed:
			state.snapshotValue.Verification.Passed++
		case OutcomeCorrecting, OutcomeReported, OutcomeAborted:
			state.snapshotValue.Verification.Failed++
		}
		changed = true
	case taskStateEventCompletion:
		state.snapshotValue.CompletionDecision = event.completion.Decision
		state.snapshotValue.CompletionReason = event.completion.Reason
		switch event.completion.Decision {
		case CompletionComplete:
			state.snapshotValue.Status = taskStatusComplete
		case CompletionIncomplete:
			state.snapshotValue.Status = taskStatusIncomplete
		default:
			state.snapshotValue.Status = taskStatusActive
		}
		changed = true
	}
	if changed {
		state.snapshotValue.Revision++
		if event.kind != taskStateEventToolResult {
			state.emit()
		}
	}
	return changed
}

func (state *taskState) markActive() {
	state.snapshotValue.Status = taskStatusActive
	state.snapshotValue.CompletionDecision = ""
	state.snapshotValue.CompletionReason = ""
}

func (state *taskState) snapshot() taskStateSnapshot {
	if state == nil {
		return taskStateSnapshot{}
	}
	snapshot := state.snapshotValue
	snapshot.Plan.Items = append([]taskPlanItem(nil), state.snapshotValue.Plan.Items...)
	snapshot.ChangedFiles = append([]string(nil), state.snapshotValue.ChangedFiles...)
	return snapshot
}

// observePlanParity compares only the latest plan projection with the plan tool
// calls still present in messages. It mutates the snapshot and emits when the
// parity value changes; objective, tool, and verification fields are not part of
// this comparison.
func (state *taskState) observePlanParity(messages []zeroruntime.Message) taskPlanParity {
	if state == nil {
		return taskPlanParityUnknown
	}
	transcriptPlan, found, valid := latestTaskPlan(messages)
	tracked := state.snapshotValue.Plan.Items
	parity := taskPlanParityMatch
	if !valid || found != state.planObserved || (found && !reflect.DeepEqual(transcriptPlan, tracked)) {
		parity = taskPlanParityMismatch
	}
	if state.snapshotValue.PlanParity != parity {
		state.snapshotValue.PlanParity = parity
		state.snapshotValue.Revision++
		state.emit()
	}
	return parity
}

type completionContext struct {
	Objective             string
	PlanPending           bool
	PlanMatchesTranscript bool
}

func (state *taskState) completionContext(messages []zeroruntime.Message, transcriptPlanPending bool) completionContext {
	context := completionContext{PlanPending: transcriptPlanPending}
	if state == nil {
		return context
	}
	context.Objective = state.snapshotValue.Objective
	context.PlanMatchesTranscript = state.observePlanParity(messages) == taskPlanParityMatch
	if context.PlanMatchesTranscript {
		context.PlanPending = state.snapshotValue.Plan.Pending+state.snapshotValue.Plan.InProgress > 0
	}
	return context
}

func (state *taskState) snapshotForCompaction(messages []zeroruntime.Message) *taskStateSnapshot {
	if state == nil {
		return nil
	}
	state.observePlanParity(messages)
	snapshot := state.snapshot()
	return &snapshot
}

func (state *taskState) emit() {
	if state == nil || state.recorder == nil {
		return
	}
	snapshot := state.snapshotValue
	state.recorder.EmitTaskState(trace.TaskStateEvent{
		Revision:            snapshot.Revision,
		Status:              string(snapshot.Status),
		PlanPending:         snapshot.Plan.Pending,
		PlanInProgress:      snapshot.Plan.InProgress,
		PlanCompleted:       snapshot.Plan.Completed,
		PlanFailed:          snapshot.Plan.Failed,
		ToolsSucceeded:      snapshot.Tools.Succeeded,
		ToolsFailed:         snapshot.Tools.Failed,
		VerificationPassed:  snapshot.Verification.Passed,
		VerificationFailed:  snapshot.Verification.Failed,
		VerificationOutcome: string(snapshot.Verification.LastOutcome),
		ChangedFileCount:    len(snapshot.ChangedFiles),
		CompletionDecision:  string(snapshot.CompletionDecision),
		PlanParity:          string(snapshot.PlanParity),
	})
}

func parseTaskPlan(arguments string) ([]taskPlanItem, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.TrimSpace(arguments)), &object) != nil {
		return nil, false
	}
	rawPlan, ok := object["plan"]
	if !ok || string(rawPlan) == "null" {
		return nil, false
	}
	var parsed []struct {
		ID      *string `json:"id"`
		Content string  `json:"content"`
		Status  string  `json:"status"`
		Notes   string  `json:"notes"`
	}
	if json.Unmarshal(rawPlan, &parsed) != nil {
		return nil, false
	}
	plan := make([]taskPlanItem, 0, len(parsed))
	for _, raw := range parsed {
		if raw.Content == "" {
			return nil, false
		}
		plan = append(plan, taskPlanItem{
			Content: raw.Content,
			Status:  normalizeTaskPlanStatus(raw.Status),
			Notes:   raw.Notes,
		})
	}
	lastInProgress := -1
	for index := range plan {
		if plan[index].Status == "in_progress" {
			if lastInProgress >= 0 {
				plan[lastInProgress].Status = "completed"
			}
			lastInProgress = index
		}
	}
	if len(plan) == 0 {
		return nil, true
	}
	return plan, true
}

func normalizeTaskPlanStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done", "finished", "resolved", "✓", "x", "[x]":
		return "completed"
	case "in_progress", "in-progress", "inprogress", "in progress", "active", "doing", "started", "current", "wip", "ongoing", "running":
		return "in_progress"
	case "failed", "fail", "error", "errored", "blocked", "cancelled", "canceled", "abandoned", "skipped":
		return "failed"
	default:
		return "pending"
	}
}

func summarizeTaskPlan(items []taskPlanItem) taskPlanState {
	plan := taskPlanState{Items: append([]taskPlanItem(nil), items...)}
	for _, item := range items {
		switch item.Status {
		case "completed":
			plan.Completed++
		case "in_progress":
			plan.InProgress++
		case "failed":
			plan.Failed++
		default:
			plan.Pending++
		}
	}
	return plan
}

func latestTaskPlan(messages []zeroruntime.Message) ([]taskPlanItem, bool, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		for j := len(messages[i].ToolCalls) - 1; j >= 0; j-- {
			call := messages[i].ToolCalls[j]
			if call.Name != planToolName {
				continue
			}
			plan, ok := parseTaskPlan(call.Arguments)
			return plan, true, ok
		}
	}
	return nil, false, true
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	// Paths are evidence, not chronology. Stable ordering makes replay snapshots
	// deterministic even if events originate from parallel read batches later.
	sort.Strings(keys)
	return keys
}
