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
	clearNestingMarkers(t)
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

// clearNestingMarkers keeps a test from inheriting the markers when the suite
// itself runs inside a Zero sandbox.
func clearNestingMarkers(t *testing.T) {
	t.Helper()
	t.Setenv(EnvSandboxed, "")
	t.Setenv(EnvSandboxBackend, "")
}

// THE NESTING MARKERS SKIP WRAPPING, SO THEY HAVE TO BE SAID.
//
// With ZERO_SANDBOXED and ZERO_SANDBOX_BACKEND set, the runner passes every
// command through unwrapped on the word of an outer sandbox nothing verifies
// (#727). A native backend's plan knows nothing of that and reports full
// enforcement, so the session used to run every command unwrapped in silence.
func TestEngineDegradedNoticeSpeaksWhenTheNestingMarkersSkipWrapping(t *testing.T) {
	root := t.TempDir()
	native := Backend{Name: BackendLinuxBwrap, Platform: "linux", Available: true, NativeIsolation: true, CommandWrapping: true, Executable: "/usr/bin/bwrap"}

	clearNestingMarkers(t)
	if got := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: native}).DegradedNotice(); got != "" {
		t.Fatalf("SETUP INVALID: a native backend without the markers already gives a notice: %q", got)
	}

	t.Setenv(EnvSandboxed, "1")
	t.Setenv(EnvSandboxBackend, string(BackendLinuxBwrap))
	got := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: native}).DegradedNotice()
	if !strings.Contains(got, EnvSandboxed) || !strings.Contains(got, "cannot verify") {
		t.Errorf("with the nesting markers set the notice = %q, want one naming the markers and the unverified outer sandbox", got)
	}

	disabled := DefaultPolicy()
	disabled.Mode = ModeDisabled
	if got := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: disabled, Backend: native}).DegradedNotice(); got != "" {
		t.Errorf("a sandbox the user turned off still produced a notice under the markers: %q", got)
	}
}
