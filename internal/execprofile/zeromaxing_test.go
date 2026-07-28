package execprofile

import (
	"strings"
	"testing"
)

// (d) THE TRAP PIN.
//
// Zeromaxing.ReasoningEffort must stay "high". Every level above it falls
// through the providers' effort→budget mappers into their DEFAULT arm, and
// those defaults mean "no extended thinking":
//
//	anthropic thinkingBudgetForEffort default -> 0   (thinking disabled)
//	gemini    thinkingBudgetForEffort default -> 0   (thinking disabled)
//	openai    openAIReasoningEffort    default -> "" (field omitted entirely)
//
// So "raising" this to xhigh/max would silently turn reasoning OFF while the UI
// claimed a higher posture — a downgrade wearing an upgrade's name, invisible
// at runtime. Raising the real ceiling means teaching the providers those tiers
// first; until then this test is what stands in the way.
func TestZeromaxingReasoningEffortStaysHigh(t *testing.T) {
	if Zeromaxing.ReasoningEffort != "high" {
		t.Fatalf("Zeromaxing.ReasoningEffort = %q, want \"high\" — see this test's comment: "+
			"any level above \"high\" hits the providers' default arm and DISABLES thinking",
			Zeromaxing.ReasoningEffort)
	}
	// Thorough is the rung below and shares the ceiling; if these ever diverge,
	// the honest-delta text is wrong.
	if Thorough.ReasoningEffort != Zeromaxing.ReasoningEffort {
		t.Fatalf("thorough effort %q != zeromaxing effort %q — Delta claims they are equal",
			Thorough.ReasoningEffort, Zeromaxing.ReasoningEffort)
	}
}

// One name, one table. No aliases means no second spelling to keep in step.
func TestZeromaxingHasExactlyOneSpelling(t *testing.T) {
	profile, ok := Lookup(Name)
	if !ok {
		t.Fatalf("%q is not in the catalog", Name)
	}
	if profile != Zeromaxing || !profile.IsZeromaxing() {
		t.Fatalf("Lookup(%q) = %+v, want %+v", Name, profile, Zeromaxing)
	}
	// Case/whitespace insensitivity, matching the other profiles.
	if p, ok := Lookup("  ZEROMAXING "); !ok || !p.IsZeromaxing() {
		t.Fatalf("Lookup is not case/space insensitive: %+v ok=%v", p, ok)
	}
	// The rejected spellings. Every one of these resolving would mean a second
	// name for one posture, which is the drift this design exists to avoid.
	for _, alias := range []string{"max", "deep", "deepmode", "zero-maxing", "zeromax", "zm"} {
		if _, ok := Lookup(alias); ok {
			t.Fatalf("%q must NOT resolve — the posture has exactly one name, %q", alias, Name)
		}
	}
	found := false
	for _, name := range Names() {
		if name == Name {
			found = true
		}
	}
	if !found {
		t.Fatalf("Names() omits %q, so usage text and hints will not offer it: %v", Name, Names())
	}
}

// The knobs, pinned. The turn budget is the ONLY mechanical delta over
// thorough — Delta says exactly that to the user, so if these drift apart the
// user-facing claim becomes false.
func TestZeromaxingKnobsMatchTheDeltaItAdvertises(t *testing.T) {
	if Zeromaxing.MaxTurns != 320 {
		t.Fatalf("Zeromaxing.MaxTurns = %d, want 320", Zeromaxing.MaxTurns)
	}
	if Zeromaxing.MaxTurns != Thorough.MaxTurns*2 {
		t.Fatalf("budget %d is not double thorough's %d", Zeromaxing.MaxTurns, Thorough.MaxTurns)
	}
	if !Zeromaxing.SelfCorrect || !Thorough.SelfCorrect {
		t.Fatalf("Delta claims self-correction is already armed at thorough: zeromaxing=%v thorough=%v",
			Zeromaxing.SelfCorrect, Thorough.SelfCorrect)
	}
	// It arms no escalation triggers (it is the top rung — nothing to escalate
	// to), so Policy returns nil exactly like thorough and balanced.
	if policy := Zeromaxing.Policy(80, true); policy != nil {
		t.Fatalf("zeromaxing must arm no escalation policy, got %+v", policy)
	}
	for _, want := range []string{"320", "160", "sub-agents"} {
		if !strings.Contains(Delta, want) {
			t.Fatalf("Delta must state %q so the user sees the real delta: %q", want, Delta)
		}
	}
}

// (m)+(n) at the rule level: SelectionRefusal is the single authority both
// selection paths consult. Its callers are asserted separately (CLI and TUI).
func TestSelectionRefusalDisablesOnlyZeromaxingAndOnlyWhenDisabled(t *testing.T) {
	if refusal := SelectionRefusal(Zeromaxing, true); refusal == "" {
		t.Fatal("a disabled workspace must refuse zeromaxing")
	} else if !strings.Contains(refusal, "disableZeromaxing") {
		t.Fatalf("the refusal must name the setting so the user can act on it: %q", refusal)
	}
	if refusal := SelectionRefusal(Zeromaxing, false); refusal != "" {
		t.Fatalf("zeromaxing must be selectable when not disabled, got %q", refusal)
	}
	// The disable flag is posture-scoped: it must not silently take out the
	// other profiles, which have no cost-multiplier justification for gating.
	for _, other := range []Profile{Balanced, Fast, Thorough} {
		if refusal := SelectionRefusal(other, true); refusal != "" {
			t.Fatalf("disableZeromaxing must not refuse %q: %q", other.Name, refusal)
		}
	}
}
