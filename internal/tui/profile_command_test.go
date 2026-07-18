package tui

import (
	"strings"
	"testing"
)

func TestProfileCommandStatus(t *testing.T) {
	_, out := model{}.handleProfileCommand("")
	if !strings.Contains(out, "balanced (default)") {
		t.Fatalf("bare /profile must report the balanced default, got %q", out)
	}
	_, out = model{}.handleProfileCommand("status")
	if !strings.Contains(out, "balanced (default)") {
		t.Fatalf("/profile status must report the balanced default, got %q", out)
	}
}

func TestProfileCommandUnknownValue(t *testing.T) {
	got, out := model{}.handleProfileCommand("turbo")
	if !strings.Contains(out, "Usage:") {
		t.Fatalf("unknown profile must print usage, got %q", out)
	}
	if got.execProfileName != "" || got.agentOptions.Profile != nil {
		t.Fatal("unknown profile must not change any state")
	}
}

// Fast applies to the next run: the turn budget drops, the escalation policy
// is armed with the DISPLACED budget as its restore target, and effort stays
// untouched when the session has no model-supported effort ring (model{}).
func TestProfileCommandSetsFastPosture(t *testing.T) {
	m := model{}
	m.agentOptions.MaxTurns = 80

	got, out := m.handleProfileCommand("fast")
	if got.agentOptions.MaxTurns != 30 {
		t.Fatalf("MaxTurns = %d, want the fast profile's 30", got.agentOptions.MaxTurns)
	}
	if got.execProfileName != "fast" {
		t.Fatalf("execProfileName = %q, want fast", got.execProfileName)
	}
	policy := got.agentOptions.Profile
	if policy == nil || policy.Escalate == nil {
		t.Fatal("fast must arm an escalation policy on agentOptions")
	}
	if policy.Escalate.MaxTurns != 80 {
		t.Fatalf("Escalate.MaxTurns = %d, want the displaced 80", policy.Escalate.MaxTurns)
	}
	if got.reasoningEffort != "" {
		t.Fatalf("effort = %q, must stay auto when the model exposes no effort ring", got.reasoningEffort)
	}
	if !strings.Contains(out, "fast") || !strings.Contains(out, "headless-only") {
		t.Fatalf("status must name the profile and the TUI signal asymmetry, got %q", out)
	}
}

func TestProfileCommandThoroughArmsSelfCorrect(t *testing.T) {
	m := model{}
	m.agentOptions.MaxTurns = 80

	got, _ := m.handleProfileCommand("thorough")
	if got.agentOptions.MaxTurns != 160 {
		t.Fatalf("MaxTurns = %d, want thorough's 160", got.agentOptions.MaxTurns)
	}
	if !got.selfCorrectTests {
		t.Fatal("thorough must arm full self-correction")
	}
	if got.agentOptions.Profile != nil {
		t.Fatal("thorough has no escalation triggers, so the loop policy must stay nil")
	}
}

func TestProfileCommandBalancedRestoresDisplaced(t *testing.T) {
	m := model{}
	m.agentOptions.MaxTurns = 80

	m, _ = m.handleProfileCommand("fast")
	got, _ := m.handleProfileCommand("balanced")
	if got.agentOptions.MaxTurns != 80 {
		t.Fatalf("MaxTurns = %d, want the displaced 80 restored", got.agentOptions.MaxTurns)
	}
	if got.execProfileName != "" || got.agentOptions.Profile != nil {
		t.Fatal("balanced must clear the profile state and loop policy")
	}
}

// Profiles must not stack: switching fast -> thorough restores fast's
// displacement first, so thorough displaces the ORIGINAL budget and a later
// balanced lands back on it.
func TestProfileCommandSwitchDoesNotStack(t *testing.T) {
	m := model{}
	m.agentOptions.MaxTurns = 80

	m, _ = m.handleProfileCommand("fast")
	m, _ = m.handleProfileCommand("thorough")
	if m.agentOptions.MaxTurns != 160 {
		t.Fatalf("MaxTurns = %d, want thorough's 160", m.agentOptions.MaxTurns)
	}
	if m.execProfileDisplacedMaxTurns != 80 {
		t.Fatalf("displaced = %d, want the original 80 (not fast's 30)", m.execProfileDisplacedMaxTurns)
	}
	m, _ = m.handleProfileCommand("balanced")
	if m.agentOptions.MaxTurns != 80 {
		t.Fatalf("MaxTurns = %d, want the original 80 restored", m.agentOptions.MaxTurns)
	}
}

// An explicit /turns while a profile is active is a pinned budget: it must
// disarm the escalation's turn target (mirroring headless exec, where an
// explicit --max-turns leaves nothing displaced) while the other triggers
// stay armed, and it must survive a later revert.
func TestProfileCommandTurnsPinDisarmsEscalationTarget(t *testing.T) {
	m := model{}
	m.agentOptions.MaxTurns = 80

	m, _ = m.handleProfileCommand("fast")
	m, _ = m.handleTurnsCommand("20")
	if m.agentOptions.MaxTurns != 20 {
		t.Fatalf("MaxTurns = %d, want the pinned 20", m.agentOptions.MaxTurns)
	}
	policy := m.agentOptions.Profile
	if policy == nil || policy.Escalate == nil {
		t.Fatal("the profile's other escalation triggers must stay armed")
	}
	if policy.Escalate.MaxTurns != 0 {
		t.Fatalf("Escalate.MaxTurns = %d, want 0 (a pinned budget must never be raised by escalation)", policy.Escalate.MaxTurns)
	}
	if policy.Escalate.OnToolFailureStreak == 0 {
		t.Fatal("disarming the turn target must not disarm the other triggers")
	}
	m, _ = m.handleProfileCommand("balanced")
	if m.agentOptions.MaxTurns != 20 {
		t.Fatalf("MaxTurns = %d, the pinned 20 must survive the revert", m.agentOptions.MaxTurns)
	}
}

// A manual override that COINCIDES with the profile's own value must still
// survive the revert: touched beats value equality.
func TestProfileCommandRevertLeavesCoincidingOverride(t *testing.T) {
	m := model{}
	m.agentOptions.MaxTurns = 80

	m, _ = m.handleProfileCommand("fast")
	m, _ = m.handleTurnsCommand("30") // explicit, happens to equal fast's 30
	m, _ = m.handleProfileCommand("balanced")
	if m.agentOptions.MaxTurns != 30 {
		t.Fatalf("MaxTurns = %d, the explicit /turns 30 must survive even though it equals fast's value", m.agentOptions.MaxTurns)
	}

	// Same for self-correct: /selfcorrect off then on under thorough is an
	// explicit opt-in, not the profile's arming.
	m2 := model{}
	m2.agentOptions.MaxTurns = 80
	m2, _ = m2.handleProfileCommand("thorough")
	m2, _ = m2.handleSelfCorrectCommand("off")
	m2, _ = m2.handleSelfCorrectCommand("on")
	m2, _ = m2.handleProfileCommand("balanced")
	if !m2.selfCorrectTests {
		t.Fatal("an explicit /selfcorrect on must survive the revert even though thorough also armed it")
	}
}

// A manual override made after selecting a profile survives the revert: the
// profile only restores knobs that still hold the value it applied.
func TestProfileCommandRevertLeavesManualOverride(t *testing.T) {
	m := model{}
	m.agentOptions.MaxTurns = 80

	m, _ = m.handleProfileCommand("fast")
	m, _ = m.handleTurnsCommand("100")
	got, _ := m.handleProfileCommand("balanced")
	if got.agentOptions.MaxTurns != 100 {
		t.Fatalf("MaxTurns = %d, the user's explicit /turns 100 must survive the revert", got.agentOptions.MaxTurns)
	}
	// An explicit /selfcorrect choice armed BEFORE thorough must survive too.
	m2 := model{selfCorrectTests: true}
	m2.agentOptions.MaxTurns = 80
	m2, _ = m2.handleProfileCommand("thorough")
	m2, _ = m2.handleProfileCommand("balanced")
	if !m2.selfCorrectTests {
		t.Fatal("an explicit self-correct opt-in must survive profile revert")
	}
}
