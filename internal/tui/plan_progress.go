package tui

import (
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/specialist"
)

// PlanProgressBridge turns plan lifecycle events into TUI messages.
//
// LIFETIME, and it is the PostureGate problem again: the registry is built once
// per session and the orchestrate tool holds this recorder for the process's
// life, while the run id changes on every run. So this is a POINTER with
// mutable state that the model re-attaches per run, not a closure over a
// value-typed model that would freeze the first run's id forever.
//
// THREAD SAFETY: every method here is called from the tool's goroutine, never
// from the Bubble Tea event loop. It takes a mutex, builds a message, and hands
// it to the sink — which is the same asynchronous path OnToolProgress already
// uses. Nothing here renders, blocks, or touches model state.
//
// BEST EFFORT, like every other recorder on this path: a nil bridge, a nil sink
// or an unattached run is a silent no-op. Recording must never be the thing
// that fails a plan (execSessionRecorder.append's contract).
type PlanProgressBridge struct {
	mu    sync.Mutex
	sink  func(tea.Msg)
	runID int
	// dispatched counts tasks so each gets a unique temporary card key. The
	// child's real session id is not known until the child process creates it,
	// so the card is keyed by this and reconciled on completion — exactly how
	// the Task tool's card works.
	dispatched int
}

// NewPlanProgressBridge returns a bridge that is inert until Attach is called.
func NewPlanProgressBridge() *PlanProgressBridge { return &PlanProgressBridge{} }

// Attach binds the bridge to the run that is about to start. Called on every
// run so a plan's cards belong to the run that produced them; the stale-run
// guard in the message handlers does the rest.
func (bridge *PlanProgressBridge) Attach(sink func(tea.Msg), runID int) {
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.sink = sink
	bridge.runID = runID
	bridge.dispatched = 0
}

// send posts a message if the bridge is attached. Nil-safe at every level.
func (bridge *PlanProgressBridge) send(build func(runID int) tea.Msg) {
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	sink, runID := bridge.sink, bridge.runID
	bridge.mu.Unlock()
	if sink == nil {
		return
	}
	sink(build(runID))
}

// planTaskKey is the temporary card key for the nth dispatched task. Namespaced
// so it can never collide with a tool call id.
func planTaskKey(n int) string { return fmt.Sprintf("plantask_%d", n) }

// PlanAdmitted announces the plan so the transcript can show that N tasks are
// about to run rather than going silent until the first one finishes.
func (bridge *PlanProgressBridge) PlanAdmitted(plan specialist.Plan) {
	name := plan.Name()
	count := plan.TaskCount()
	limit := plan.Budget().MaxTokens

	// Copied into the message in EXECUTION ORDER, with the dependency edges, so
	// the panel can draw the graph without reaching back into the plan — which
	// it could not do anyway: Plan's fields are unexported and it lives in
	// another package on the tool's goroutine.
	byID := map[string]specialist.Task{}
	for _, task := range plan.Tasks() {
		byID[task.ID] = task
	}
	graph := make([]planGraphTask, 0, count)
	for _, id := range plan.Order() {
		task := byID[id]
		graph = append(graph, planGraphTask{
			id:        id,
			dependsOn: append([]string(nil), task.DependsOn...),
			phase:     task.Phase,
		})
	}

	bridge.send(func(runID int) tea.Msg {
		return planAdmittedMsg{runID: runID, name: name, taskCount: count, tasks: graph, tokenLimit: limit}
	})
}

// TaskDispatched opens a card for the task that is about to run.
func (bridge *PlanProgressBridge) TaskDispatched(task specialist.Task) {
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	bridge.dispatched++
	key := planTaskKey(bridge.dispatched)
	bridge.mu.Unlock()

	id, summary := task.ID, planTaskSummary(task)
	bridge.send(func(runID int) tea.Msg {
		return planTaskStartMsg{runID: runID, taskID: id, summary: summary, cardKey: key}
	})
}

// TaskCompleted closes the card and reconciles it to the child's real session
// id so the user can drill into it.
func (bridge *PlanProgressBridge) TaskCompleted(result specialist.TaskResult) {
	bridge.finish(result, specialistCompleted)
}

// TaskFailed closes the card with the outcome's own status. A cancelled or
// skipped task is NOT rendered as an error: nothing broke.
func (bridge *PlanProgressBridge) TaskFailed(result specialist.TaskResult) {
	bridge.finish(result, planOutcomeStatus(result.Outcome))
}

func (bridge *PlanProgressBridge) finish(result specialist.TaskResult, status specialistStatus) {
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	key := planTaskKey(bridge.dispatched)
	bridge.mu.Unlock()

	// A task that was never dispatched (dependency-skipped, budget-skipped,
	// cancelled before it ran) has no card. Reporting it against the LAST
	// dispatched task's key would close the wrong card, so those carry their
	// own key and the handler creates the card on demand.
	dispatched := result.Outcome == specialist.TaskSucceeded || result.Outcome == specialist.TaskFailed
	taskID, sessionID, reason := result.ID, result.SessionID, result.Err
	outcome, tokens := result.Outcome, result.Tokens
	bridge.send(func(runID int) tea.Msg {
		return planTaskDoneMsg{
			runID:      runID,
			taskID:     taskID,
			cardKey:    key,
			dispatched: dispatched,
			sessionID:  sessionID,
			status:     status,
			outcome:    string(outcome),
			reason:     reason,
			tokens:     tokens,
		}
	})
}

// PlanCompleted reports the plan's terminal state.
func (bridge *PlanProgressBridge) PlanCompleted(plan specialist.Plan, report specialist.PlanReport) {
	name := plan.Name()
	status := string(report.Status)
	succeeded, failed := report.Succeeded, report.Failed
	skipped, cancelled := report.Skipped, report.Cancelled
	tokens, limit := report.TokensUsed, plan.Budget().MaxTokens
	speedup := report.MaxSpeedup
	bridge.send(func(runID int) tea.Msg {
		return planCompletedMsg{
			runID: runID, name: name, status: status,
			succeeded: succeeded, failed: failed, skipped: skipped, cancelled: cancelled,
			tokensUsed: tokens, tokenLimit: limit, maxSpeedup: speedup,
		}
	})
}

// planOutcomeStatus maps a task outcome onto the card status. Cancelled and
// skipped are deliberately NOT specialistError: a user who stopped a plan did
// not break it, and a task skipped because its dependency failed is not itself
// a failure.
func planOutcomeStatus(outcome specialist.TaskOutcome) specialistStatus {
	switch outcome {
	case specialist.TaskSucceeded:
		return specialistCompleted
	case specialist.TaskFailed:
		return specialistError
	default:
		// Cancelled, dependency-skipped, budget-skipped: ended without running
		// to completion, but not an error.
		return specialistCancelled
	}
}

// planTaskSummary is a SHORT label for the card. The full prompt stays in the
// tool output; a display surface never becomes the data path.
func planTaskSummary(task specialist.Task) string {
	summary := strings.TrimSpace(task.Prompt)
	if summary == "" {
		return ""
	}
	if index := strings.IndexAny(summary, "\r\n"); index >= 0 {
		summary = summary[:index]
	}
	return truncateRunes(summary, planTaskSummaryWidth)
}

// planTaskSummaryWidth bounds the card label. truncateRunes (view.go) does the
// cut on RUNE boundaries — reusing it rather than reimplementing keeps one
// truncation rule, since slicing by byte index produces mojibake.
const planTaskSummaryWidth = 60
