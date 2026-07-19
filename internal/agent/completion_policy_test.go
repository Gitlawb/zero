package agent

import (
	"strings"
	"testing"
)

func TestCompletionPolicyLocalEvidenceDecidesWithoutSemanticCheck(t *testing.T) {
	policy := newCompletionPolicy(false)

	complete := policy.evaluate("Done. All required checks pass.", completionContext{})
	if complete.Decision != CompletionComplete {
		t.Fatalf("confident completion decision = %q, want %q", complete.Decision, CompletionComplete)
	}

	incomplete := policy.evaluate("I couldn't verify the result, so this is my best guess.", completionContext{})
	if incomplete.Decision != CompletionIncomplete {
		t.Fatalf("admitted failure decision = %q, want %q", incomplete.Decision, CompletionIncomplete)
	}
}

func TestCompletionPolicyPreservesBoundedPlanStallProtection(t *testing.T) {
	policy := newCompletionPolicy(false)
	for attempt := 0; attempt < maxContinueNudges; attempt++ {
		got := policy.evaluate("Let me inspect the remaining configuration:", completionContext{PlanPending: true})
		if got.Decision != CompletionUncertain || got.Action != completionActionContinue {
			t.Fatalf("attempt %d = (%q, %q), want uncertain continue", attempt+1, got.Decision, got.Action)
		}
	}

	got := policy.evaluate("Let me inspect the remaining configuration:", completionContext{PlanPending: true})
	if got.Decision != CompletionIncomplete {
		t.Fatalf("decision after nudge budget = %q, want %q", got.Decision, CompletionIncomplete)
	}
}

func TestCompletionPolicyTreatsPendingPlanAsWeakEvidence(t *testing.T) {
	policy := newCompletionPolicy(false)
	for attempt := 0; attempt < maxContinueNudges; attempt++ {
		got := policy.evaluate("All set.", completionContext{PlanPending: true})
		if got.Decision != CompletionUncertain || got.Action != completionActionContinue {
			t.Fatalf("attempt %d = (%q, %q), want uncertain continue", attempt+1, got.Decision, got.Action)
		}
	}

	got := policy.evaluate("All set.", completionContext{PlanPending: true})
	if got.Decision != CompletionComplete {
		t.Fatalf("stale plan decision after nudge budget = %q, want %q", got.Decision, CompletionComplete)
	}
}

func TestCompletionPolicyAllowsExactlyOneRequiredSemanticCheck(t *testing.T) {
	policy := newCompletionPolicy(true)

	first := policy.evaluate("Implemented and tested.", completionContext{})
	if first.Decision != CompletionUncertain || first.Action != completionActionSemanticCheck {
		t.Fatalf("first decision = (%q, %q), want uncertain semantic check", first.Decision, first.Action)
	}

	second := policy.evaluate("PASS. The result meets the task criterion.", completionContext{})
	if second.Decision != CompletionComplete {
		t.Fatalf("post-check decision = %q, want %q", second.Decision, CompletionComplete)
	}
	if second.Action == completionActionSemanticCheck {
		t.Fatal("semantic check was requested more than once")
	}
}

func TestAcceptanceVerificationNudgeIncludesBoundedObjective(t *testing.T) {
	objective := "make the requested behavior observable"
	nudge := acceptanceVerificationNudge(objective)
	if !strings.Contains(nudge, "Task objective: "+objective) {
		t.Fatalf("semantic check was not grounded in the objective: %q", nudge)
	}
}
