package agent

// The zeromaxing execution posture's model-facing reminders.
//
// WHERE THESE GO, AND WHY IT IS NOT NEGOTIABLE
//
// Every string here is appended to the CONVERSATION as a user-role message, at
// the tail, exactly like the failure hint and the plan/progress reminders in
// the turn loop. None of it ever reaches buildSystemPromptParts.
//
// The system prompt and the tool definitions are the provider's cached prefix
// (the anthropic mapper puts its cache_control breakpoint on the last system
// block and the last tool). #760 made that prefix build ONCE per run precisely
// so it stays byte-identical across turns. A reminder that varies per turn and
// lands above the breakpoint invalidates the cache on every single turn —
// roughly doubling input cost — and nothing in the system reports it. Hence:
// tail only.
//
// The same reasoning is why ZeromaxingStillOnNotice is a fixed literal with no
// turn counter, no timestamp, and no accumulated state. It is repeated verbatim
// on every continuing turn. If it ever grew a varying part and someone later
// moved it into prompt assembly, that would be a silent cost bug rather than a
// loud test failure. TestRunPreservesRequestPrefixAcrossTurnsUnderZeromaxing is
// the tripwire.
//
// The wording is written for this project: plain, neutral, describing Zero's
// own posture in Zero's own terms. It deliberately promises no orchestration,
// no fan-out and no workflow tool — Phase 1 ships none of those, and a reminder
// that implies capabilities the run does not have is a prompt-level lie.

// Zeromaxing is where a run sits in the zeromaxing posture's lifecycle. It is
// set by whoever selected the posture (headless exec for a one-shot run, the
// TUI across a session) and read only by the turn loop.
type Zeromaxing int

const (
	// ZeromaxingOff: the posture is not active. Every reminder is suppressed and
	// the loop is byte-identical to a run that never heard of it. This is the
	// zero value, so every existing caller keeps today's behaviour.
	ZeromaxingOff Zeromaxing = iota
	// ZeromaxingEntering: this run is the first since the posture was turned on,
	// so its first turn carries the enter notice and the budget guideline.
	ZeromaxingEntering
	// ZeromaxingActive: the posture was already on before this run began, so
	// even the first turn gets the continuing notice rather than the enter one.
	ZeromaxingActive
	// ZeromaxingExiting: this run is the first since the posture was turned off.
	// Its first turn carries the exit notice and nothing after that.
	ZeromaxingExiting
)

// The four messages. Exported as constants so tests assert on the same bytes
// the model receives rather than on a paraphrase.
const (
	// ZeromaxingEnterNotice announces the flip. It fires once, on the first turn
	// of the run in which the posture was selected.
	ZeromaxingEnterNotice = "The zeromaxing execution posture is now active for this session. " +
		"You have a substantially larger tool-turn budget than usual. Use it for depth on the task that was asked: " +
		"read before changing, run the checks that would catch a mistake, and follow up on anything you noticed but could not confirm."

	// ZeromaxingBudgetNotice states the budget guideline alongside the enter
	// notice. Split from it so the two can be asserted independently, and so a
	// future budget change touches one string.
	ZeromaxingBudgetNotice = "Budget guideline under zeromaxing: the larger turn budget is for verifying one task thoroughly, not for starting extra work. " +
		"Finish what was asked, confirm it, and stop."

	// ZeromaxingOrchestrateNotice is appended to the enter notice ONLY when the
	// orchestrate tool is actually available this run. It is the behavioral
	// directive that turns zeromaxing from "more turns" into "multi-agent depth".
	// Naming a tool the run does not have would be a prompt-level lie the model
	// would try to act on, which is why this is conditional rather than folded
	// into the enter notice.
	ZeromaxingOrchestrateNotice = "You also have the orchestrate tool and background task support. " +
		"For substantive work, split independent read/analyze pieces into a planned DAG with explicit depends_on, run them through the orchestrate tool, and verify the merged findings before acting. " +
		"For long-running commands, prefer Bash with run_in_background. " +
		"Work alone only on conversational turns or trivial single-file edits."

	// ZeromaxingStillOnNotice repeats on every continuing turn.
	//
	// It is intentionally short and FIXED. Do not add a turn number, a
	// remaining-budget count, a timestamp, or anything else that differs between
	// turns — see the file comment.
	ZeromaxingStillOnNotice = "The zeromaxing execution posture is still active."

	// ZeromaxingExitNotice announces the flip back. It fires once, on the first
	// turn of the run after the posture was turned off.
	ZeromaxingExitNotice = "The zeromaxing execution posture is no longer active. " +
		"The tool-turn budget has returned to its normal value; work at the usual depth and avoid launching heavy multi-step side tasks."
)

// zeromaxingReminders returns the reminder lines to append before the given
// turn, oldest first, or nil when there is nothing to say. turn is 1-based, so
// turn == 1 is the run's first provider request.
//
// The whole state machine is this function: it is pure, it depends only on
// (posture, turn), and it has no memory. That is what makes "enter exactly
// once" and "still-on never on the first turn" testable as data rather than as
// a sequence of observed side effects.
func zeromaxingReminders(posture Zeromaxing, turn int, orchestrateAvailable bool) []string {
	if turn < 1 {
		return nil
	}
	switch posture {
	case ZeromaxingEntering:
		if turn == 1 {
			notices := []string{ZeromaxingEnterNotice, ZeromaxingBudgetNotice}
			if orchestrateAvailable {
				notices = append(notices, ZeromaxingOrchestrateNotice)
			}
			return notices
		}
		return []string{ZeromaxingStillOnNotice}
	case ZeromaxingActive:
		// Already on when the run started: no enter notice, not even on turn 1.
		return []string{ZeromaxingStillOnNotice}
	case ZeromaxingExiting:
		// One notice on the way out, then silence — the posture is off, so a
		// "still active" line every turn would be a lie.
		if turn == 1 {
			return []string{ZeromaxingExitNotice}
		}
		return nil
	default:
		return nil
	}
}
