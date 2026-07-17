package agent

// CompletionDecision is the deterministic outcome of evaluating a text-only
// assistant turn. Uncertain means the loop needs one bounded follow-up action
// before it can finalize the run.
type CompletionDecision string

const (
	CompletionUncertain  CompletionDecision = "uncertain"
	CompletionComplete   CompletionDecision = "complete"
	CompletionIncomplete CompletionDecision = "incomplete"
)

type completionAction string

const (
	completionActionNone          completionAction = ""
	completionActionContinue      completionAction = "continue"
	completionActionSemanticCheck completionAction = "semantic_check"
)

type completionEvaluation struct {
	Decision CompletionDecision
	Action   completionAction
	Reason   string
}

// completionPolicy owns the small amount of state needed to keep uncertain
// decisions bounded. requireSemanticCheck is set only for run profiles that
// already opted into self-correction; ordinary completion remains entirely
// local and adds no model call.
type completionPolicy struct {
	continueNudges         int
	requireSemanticCheck   bool
	semanticCheckRequested bool
}

func newCompletionPolicy(requireSemanticCheck bool) *completionPolicy {
	return &completionPolicy{requireSemanticCheck: requireSemanticCheck}
}

func (policy *completionPolicy) evaluate(text string, planPending bool) completionEvaluation {
	// A direct admission is strong local evidence and takes precedence over all
	// weaker signals, avoiding both continuation nudges and semantic checks.
	if reason := selfReportedIncompletion(text); reason != "" {
		return completionEvaluation{Decision: CompletionIncomplete, Reason: reason}
	}

	// A continuation cue is strong unfinished evidence; a pending plan is only
	// weak evidence because bookkeeping can be stale. Both get a bounded chance
	// to continue, but only a persisted cue is ultimately incomplete.
	cue := endsWithContinuationCue(text)
	if cue || planPending {
		if policy.continueNudges < maxContinueNudges {
			policy.continueNudges++
			reason := "your message ended mid-step"
			if !cue {
				reason = "pending plan items remain — finish them, or mark them complete with update_plan if you are done"
			}
			return completionEvaluation{
				Decision: CompletionUncertain,
				Action:   completionActionContinue,
				Reason:   reason,
			}
		}
		if cue {
			return completionEvaluation{
				Decision: CompletionIncomplete,
				Reason:   "your message ended mid-step",
			}
		}
	}

	// Profiles with self-correction enabled require one task-grounded semantic
	// check. It is requested at most once per run; the next locally complete turn
	// is accepted without another model call.
	if policy.requireSemanticCheck && !policy.semanticCheckRequested {
		policy.semanticCheckRequested = true
		return completionEvaluation{
			Decision: CompletionUncertain,
			Action:   completionActionSemanticCheck,
		}
	}

	return completionEvaluation{Decision: CompletionComplete, Action: completionActionNone}
}
