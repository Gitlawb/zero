package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/execprofile"
)

func TestApplyExecProfileFillsOnlyUnset(t *testing.T) {
	// Unset effort: the profile fills it.
	options := execOptions{execProfile: "fast"}
	profile, err := applyExecProfile(&options)
	if err != nil {
		t.Fatalf("applyExecProfile: %v", err)
	}
	if profile.Name != "fast" {
		t.Fatalf("profile.Name = %q, want fast", profile.Name)
	}
	if options.reasoningEffort != "low" {
		t.Fatalf("reasoningEffort = %q, want the profile's low", options.reasoningEffort)
	}

	// Set effort (explicit flag or a mode's fill): the profile backs off.
	options = execOptions{execProfile: "fast", reasoningEffort: "high"}
	if _, err := applyExecProfile(&options); err != nil {
		t.Fatalf("applyExecProfile: %v", err)
	}
	if options.reasoningEffort != "high" {
		t.Fatalf("reasoningEffort = %q, an explicit value must win over the profile", options.reasoningEffort)
	}
}

func TestApplyExecProfileThoroughArmsSelfCorrect(t *testing.T) {
	options := execOptions{execProfile: "thorough"}
	if _, err := applyExecProfile(&options); err != nil {
		t.Fatalf("applyExecProfile: %v", err)
	}
	if !options.selfCorrect {
		t.Fatal("thorough must arm the post-edit self-correct loop")
	}
	if options.reasoningEffort != "high" {
		t.Fatalf("reasoningEffort = %q, want high", options.reasoningEffort)
	}
}

func TestApplyExecProfileUnknownIsUsageError(t *testing.T) {
	options := execOptions{execProfile: "turbo"}
	_, err := applyExecProfile(&options)
	if err == nil {
		t.Fatal("expected a usage error for an unknown profile")
	}
	if _, ok := err.(execUsageError); !ok {
		t.Fatalf("expected execUsageError, got %T: %v", err, err)
	}
	for _, name := range execprofile.Names() {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("usage error must list %q, got %q", name, err.Error())
		}
	}
}

// Precedence: explicit flag > --mode > --exec-profile. The deep mode fills
// effort=high and max-turns=160; the fast profile must not override either,
// because a mode-filled field counts as set by the time the profile runs.
func TestExecProfilePrecedenceFlagOverModeOverProfile(t *testing.T) {
	options := execOptions{mode: "deep", execProfile: "fast"}
	if err := applyExecMode(&options); err != nil {
		t.Fatalf("applyExecMode: %v", err)
	}
	profile, err := applyExecProfile(&options)
	if err != nil {
		t.Fatalf("applyExecProfile: %v", err)
	}
	if options.reasoningEffort != "high" {
		t.Fatalf("reasoningEffort = %q, the mode's high must win over the profile's low", options.reasoningEffort)
	}
	// The mode filled options.maxTurns, so the turn budget must not be displaced.
	effective, displaced := applyProfileTurnBudget(profile, options.maxTurns, options.maxTurns)
	if effective != 160 || displaced != 0 {
		t.Fatalf("turn budget = (%d, displaced %d), the mode's 160 must win with nothing displaced", effective, displaced)
	}
}

func TestApplyProfileTurnBudget(t *testing.T) {
	fast, _ := execprofile.Lookup("fast")
	balanced, _ := execprofile.Lookup("balanced")

	// No explicit flag: the profile displaces the resolved budget.
	if effective, displaced := applyProfileTurnBudget(fast, 0, 80); effective != 30 || displaced != 80 {
		t.Fatalf("fast over resolved 80 = (%d, %d), want (30, 80)", effective, displaced)
	}
	// Explicit --max-turns pins the budget; the profile backs off entirely.
	if effective, displaced := applyProfileTurnBudget(fast, 50, 50); effective != 50 || displaced != 0 {
		t.Fatalf("fast with explicit 50 = (%d, %d), want (50, 0)", effective, displaced)
	}
	// Balanced never displaces anything.
	if effective, displaced := applyProfileTurnBudget(balanced, 0, 80); effective != 80 || displaced != 0 {
		t.Fatalf("balanced over resolved 80 = (%d, %d), want (80, 0)", effective, displaced)
	}
}

// The no-regression invariant at the options level: selecting balanced leaves
// the options byte-identical to not selecting a profile at all, and produces
// no loop policy.
func TestExecProfileBalancedLeavesOptionsUntouched(t *testing.T) {
	options := execOptions{execProfile: "balanced"}
	reference := options
	profile, err := applyExecProfile(&options)
	if err != nil {
		t.Fatalf("applyExecProfile: %v", err)
	}
	if !reflect.DeepEqual(options, reference) {
		t.Fatalf("balanced changed the options: %+v vs %+v", options, reference)
	}
	if policy := profile.Policy(80); policy != nil {
		t.Fatalf("balanced must produce a nil policy, got %+v", policy)
	}
}

// Full echo-provider run: the selected profile must be stamped into the
// per-turn trace so benchmark attribution can group runs by posture.
func TestRunExecTraceRecordsSelectedProfile(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.ndjson")
	exitCode, _, stderr := runExecWithEcho(t, []string{
		"exec", "--exec-profile", "fast", "--trace", tracePath, "hello",
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr)
	}
	raw, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if !strings.Contains(string(raw), `"profile":"fast"`) {
		t.Fatalf("trace must record the selected profile, got %s", raw)
	}
}

func TestRunExecUnknownExecProfileIsUsageError(t *testing.T) {
	exitCode, _, stderr := runExecWithEcho(t, []string{
		"exec", "--exec-profile", "turbo", "hello",
	})
	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr, "unknown execution profile") {
		t.Fatalf("expected an unknown-profile usage error, got %q", stderr)
	}
}
