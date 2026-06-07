package agent

import (
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Guardrail thresholds for the agent loop. These keep a runaway model from
// burning turns/tokens and nudge it toward keeping the plan current. They are
// deliberately conservative so trivial single-step tasks never trip them.
const (
	// maxEmptyTurns stops the run after this many consecutive turns that
	// produced no visible text AND no tool calls. A turn that produces either
	// resets the counter. Dropped-tool-call turns are handled by the existing
	// retry path and are not counted here.
	maxEmptyTurns = 3

	// staleToolCallThreshold injects a one-shot reminder once this many tool
	// calls have executed since the last update_plan call.
	staleToolCallThreshold = 10

	// planReminderTurn is the turn (1-based) by the end of which a multi-step
	// task should have called update_plan; if it hasn't, a one-time reminder is
	// injected. Set to 3 (not 2) so short, legitimate two-step tasks finish
	// without a spurious planning nag.
	planReminderTurn = 3

	// planToolName is the planning tool the loop watches for by name.
	planToolName = "update_plan"

	// toolFailureHintAt injects a one-shot corrective hint (the tool's schema +
	// the exact error) after a tool fails this many times in a row with the same
	// error, so the model self-corrects instead of repeating the mistake.
	toolFailureHintAt = 2
	// toolFailureStopAt halts the run after a tool fails this many times in a row
	// with the same error, so NO model (weak or strong) burns turns looping on a
	// bad call.
	toolFailureStopAt = 4
)

// toolFailureHintMarker is a stable substring for tests.
const toolFailureHintMarker = "kept failing with the same error"

type toolFailureRecord struct {
	count     int
	errSig    string
	hintShown bool
}

type toolFailureOutcome struct {
	InjectHint bool
	Stop       bool
	Count      int
}

// errorSignature normalizes a tool error to a short, comparable signature so
// repeated identical failures are detected while a genuinely different error
// resets the streak.
func errorSignature(output string) string {
	s := strings.ToLower(strings.Join(strings.Fields(output), " "))
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// toolFailureHint tells the model exactly how a tool's arguments must look after
// it has repeated the same failing call. Injected at most once per failure streak.
func toolFailureHint(toolName, schemaJSON, errOutput string) string {
	return "Your calls to the `" + toolName + "` tool " + toolFailureHintMarker + ":\n" +
		strings.TrimSpace(errOutput) +
		"\n\nThe `" + toolName + "` tool expects arguments matching this schema — match it exactly:\n" +
		strings.TrimSpace(schemaJSON) +
		"\n\nFix the arguments and try once more, or take a different approach."
}

// toolFailureStopAnswer is the final answer when the repeated-failure guard halts
// a run.
func toolFailureStopAnswer(toolName string, count int) string {
	return "Agent stopped: the `" + toolName + "` tool failed " + strconv.Itoa(count) +
		" times in a row with the same error, so I halted instead of looping further. " +
		"Please check the request or adjust the tool arguments."
}

// noOutputStopAnswer is the final answer returned when the no-output guard
// stops the run. The turn count is interpolated at the call site.
func noOutputStopAnswer(turns int) string {
	return "Agent stopped after " + strconv.Itoa(turns) +
		" turns with no output (no visible text and no tool calls) to avoid consuming tokens without making progress."
}

// Reminder markers are stable substrings used both to build the reminder text
// and to assert in tests that the right reminder was injected exactly once.
const (
	planNotCalledReminderMarker = "you have not called update_plan"
	planStaleReminderMarker     = "haven't updated the plan via update_plan"
)

// planNotCalledReminder nudges the model to track a multi-step task with
// update_plan. Injected at most once per run.
func planNotCalledReminder() string {
	return "Reminder: this looks like a multi-step task and " + planNotCalledReminderMarker +
		". Use the update_plan tool to record the steps and keep progress visible. " +
		"Continue with your work after updating the plan."
}

// planStaleReminder nudges the model to refresh the plan after a stretch of
// tool calls without a plan update. Injected at most once per stale interval.
func planStaleReminder(callsSinceUpdate int) string {
	return "Reminder: you've made " + strconv.Itoa(callsSinceUpdate) +
		" tool calls but " + planStaleReminderMarker +
		" in a while. Update the plan to reflect completed and remaining steps, then continue."
}

// guardState tracks the per-run signals the guardrails need. It is observable
// purely from tool-call names and per-turn output, matching what the loop holds.
type guardState struct {
	emptyTurns               int
	totalToolCalls           int
	toolCallsSincePlanUpdate int
	planEverCalled           bool
	notCalledReminderSent    bool
	// staleReminderSent records whether the stale reminder has already fired for
	// the current stale interval. It is cleared when a plan update opens a new
	// interval, making the reminder one-shot per interval rather than per turn.
	staleReminderSent bool
	// toolFailures tracks consecutive same-error failures per tool, keyed by tool
	// name, so the loop can hint then halt instead of looping forever.
	toolFailures map[string]*toolFailureRecord
}

func newGuardState() *guardState {
	return &guardState{toolFailures: map[string]*toolFailureRecord{}}
}

// observeToolResult tracks repeated identical failures of a tool. A successful
// result clears that tool's failure streak. Returns whether to inject a one-shot
// corrective hint and/or stop the run.
func (state *guardState) observeToolResult(name string, failed bool, output string) toolFailureOutcome {
	if state.toolFailures == nil {
		state.toolFailures = map[string]*toolFailureRecord{}
	}
	if !failed {
		delete(state.toolFailures, name) // success resets the streak
		return toolFailureOutcome{}
	}
	sig := errorSignature(output)
	record := state.toolFailures[name]
	if record == nil || record.errSig != sig {
		record = &toolFailureRecord{count: 1, errSig: sig}
		state.toolFailures[name] = record
	} else {
		record.count++
	}
	outcome := toolFailureOutcome{Count: record.count}
	if record.count >= toolFailureStopAt {
		outcome.Stop = true
		return outcome
	}
	if record.count >= toolFailureHintAt && !record.hintShown {
		record.hintShown = true
		outcome.InjectHint = true
	}
	return outcome
}

// observeTurn updates counters from a turn's collected stream. It returns
// whether the no-output guard should stop the run.
//
// Callers must NOT invoke this for turns handled by the dropped-tool-call retry
// path; those are not "empty" in the runaway sense and are handled separately.
func (state *guardState) observeTurn(collected zeroruntime.CollectedStream) (stop bool) {
	hasToolCalls := len(collected.ToolCalls) > 0
	hasVisibleText := strings.TrimSpace(collected.Text) != ""

	if hasToolCalls || hasVisibleText {
		state.emptyTurns = 0
	} else {
		state.emptyTurns++
	}

	for _, call := range collected.ToolCalls {
		state.totalToolCalls++
		if call.Name == planToolName {
			state.planEverCalled = true
			state.toolCallsSincePlanUpdate = 0
			// A fresh plan update opens a new stale interval.
			state.staleReminderSent = false
		} else {
			state.toolCallsSincePlanUpdate++
		}
	}

	return state.emptyTurns >= maxEmptyTurns
}

// planReminder returns a one-shot reminder message to inject before the next
// turn, or an empty string when no reminder applies. `turn` is 1-based (the
// number of turns completed so far).
func (state *guardState) planReminder(turn int) string {
	// STALE reminder takes priority: a long run without a plan update is the
	// stronger signal. One-shot per stale interval.
	if state.planEverCalled &&
		!state.staleReminderSent &&
		state.toolCallsSincePlanUpdate >= staleToolCallThreshold {
		state.staleReminderSent = true
		return planStaleReminder(state.toolCallsSincePlanUpdate)
	}

	// NOT-CALLED reminder: by the end of planReminderTurn the model should have
	// called update_plan if it's doing a multi-step task (>=1 other tool call).
	// One-shot for the whole run.
	if !state.notCalledReminderSent &&
		!state.planEverCalled &&
		turn >= planReminderTurn &&
		state.totalToolCalls >= 1 {
		state.notCalledReminderSent = true
		return planNotCalledReminder()
	}

	return ""
}
