package sandbox

import (
	"strings"
	"testing"
)

// Only DEGRADED says anything. A disabled sandbox is the user's own choice, and
// the unelevated Windows tier still enforces the write jail and reports what it
// lacks through `zero sandbox policy`; a notice on every session for either
// would stop meaning anything.
func TestDegradedNoticeSpeaksOnlyForADegradedPlan(t *testing.T) {
	for _, level := range []EnforcementLevel{EnforcementNative, EnforcementUnelevated, EnforcementDisabled, ""} {
		plan := BackendPlan{EnforcementLevel: level, DowngradeReason: "some reason"}
		if notice := plan.DegradedNotice(); notice != "" {
			t.Errorf("level %q produced a notice: %q", level, notice)
		}
	}

	plan := BackendPlan{EnforcementLevel: EnforcementDegraded, DowngradeReason: "Linux sandbox helper is not available"}
	want := "Sandbox enforcement is degraded: Linux sandbox helper is not available. See `zero sandbox policy --effective`."
	if got := plan.DegradedNotice(); got != want {
		t.Errorf("DegradedNotice() = %q, want %q", got, want)
	}
}

// A reason that already ends a sentence keeps one period, and a missing reason
// still says what is wrong.
func TestDegradedNoticeWordsItsReasonOnce(t *testing.T) {
	withPeriod := BackendPlan{EnforcementLevel: EnforcementDegraded, DowngradeReason: "Run `zero sandbox setup` first."}
	if got := withPeriod.DegradedNotice(); strings.Contains(got, "..") {
		t.Errorf("the reason's own period was doubled: %q", got)
	}
	empty := BackendPlan{EnforcementLevel: EnforcementDegraded}
	if got := empty.DegradedNotice(); !strings.Contains(got, "no native sandbox is available") {
		t.Errorf("a degraded plan with no reason gave no reason: %q", got)
	}
}

// THE SESSION'S NOTICE AND `zero sandbox policy` DESCRIBE ONE SANDBOX. The engine
// builds its notice from its own backend and policy through BuildPlan, the
// function the policy command prints from, rather than deciding "degraded" a
// second way.
func TestEngineDegradedNoticeComesFromThePolicyCommandsPlan(t *testing.T) {
	root := t.TempDir()
	unavailable := Backend{Name: BackendUnavailable, Platform: "linux", Fallback: true, Message: "Linux sandbox helper is not available"}

	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: unavailable})
	want := unavailable.BuildPlan(root, DefaultPolicy()).DegradedNotice()
	if want == "" {
		t.Fatal("SETUP INVALID: an unavailable backend under the default policy is not degraded")
	}
	if got := engine.DegradedNotice(); got != want {
		t.Errorf("engine notice = %q, want the policy command's %q", got, want)
	}

	disabled := DefaultPolicy()
	disabled.Mode = ModeDisabled
	off := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: disabled, Backend: unavailable})
	if got := off.DegradedNotice(); got != "" {
		t.Errorf("a sandbox the user turned off still produced a notice: %q", got)
	}

	var none *Engine
	if got := none.DegradedNotice(); got != "" {
		t.Errorf("a nil engine produced a notice: %q", got)
	}
}
