package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/execprofile"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// zeromaxingTestModel builds a session on a reasoning model that supports
// "high", so the posture's effort fill applies. disabled drives the config gate.
func zeromaxingTestModel(t *testing.T, disabled bool) model {
	t.Helper()
	return newModel(context.Background(), Options{
		ProviderName:       "anthropic",
		ModelName:          "claude-sonnet-4.5",
		Provider:           &fakeProvider{},
		ProviderProfile:    config.ProviderProfile{Name: "anthropic", CatalogID: "anthropic", Model: "claude-sonnet-4.5", APIKey: "k"},
		SavedProviders:     []config.ProviderProfile{{Name: "anthropic", CatalogID: "anthropic", Model: "claude-sonnet-4.5", APIKey: "k"}},
		ZeromaxingDisabled: disabled,
		NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return &fakeProvider{}, nil
		},
	})
}

// (g) THE ENTRY-POINT EQUIVALENCE. /effort zeromaxing and /profile zeromaxing
// must produce IDENTICAL resolved state. They do so by delegating to one
// implementation rather than by two parallel ones a test hopes agree — this
// asserts the result, and the delegation makes it true by construction.
func TestEffortAndProfileEntryPointsResolveIdentically(t *testing.T) {
	viaEffort, effortText := zeromaxingTestModel(t, false).handleEffortCommand(execprofile.Name)
	viaProfile, profileText := zeromaxingTestModel(t, false).handleProfileCommand(execprofile.Name)

	if viaEffort.execProfileName != viaProfile.execProfileName ||
		viaEffort.reasoningEffort != viaProfile.reasoningEffort ||
		viaEffort.agentOptions.MaxTurns != viaProfile.agentOptions.MaxTurns ||
		viaEffort.selfCorrectTests != viaProfile.selfCorrectTests ||
		viaEffort.zeromaxing != viaProfile.zeromaxing ||
		viaEffort.execProfileAppliedEffort != viaProfile.execProfileAppliedEffort ||
		viaEffort.execProfileEffortUnraised != viaProfile.execProfileEffortUnraised {
		t.Fatalf("the two entry points diverged:\n/effort:  profile=%q effort=%q turns=%d sc=%v posture=%v\n/profile: profile=%q effort=%q turns=%d sc=%v posture=%v",
			viaEffort.execProfileName, viaEffort.reasoningEffort, viaEffort.agentOptions.MaxTurns, viaEffort.selfCorrectTests, viaEffort.zeromaxing,
			viaProfile.execProfileName, viaProfile.reasoningEffort, viaProfile.agentOptions.MaxTurns, viaProfile.selfCorrectTests, viaProfile.zeromaxing)
	}
	if !reflect.DeepEqual(effortText, profileText) {
		t.Fatalf("the two entry points printed different output:\n/effort:\n%s\n/profile:\n%s", effortText, profileText)
	}
	if viaEffort.reasoningEffort != modelregistry.ReasoningEffortHigh {
		t.Fatalf("both must resolve the effort to high, got %q", viaEffort.reasoningEffort)
	}
}

// (f) THE RESERVATION. /effort max is UNCHANGED: it still parses as a raw
// provider level and still fails reasoningEffortAllowed on a model that does not
// list it. The posture must not have quietly claimed the spelling.
func TestEffortMaxReservationUnchangedTUI(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	before := m

	m, text := m.handleEffortCommand("max")
	if !strings.Contains(text, "not supported") {
		t.Fatalf("/effort max must still report an unsupported level, got:\n%s", text)
	}
	// It must not have selected the posture, changed the effort, or moved the budget.
	if m.execProfileName != before.execProfileName {
		t.Fatalf("/effort max must not select a profile, got %q", m.execProfileName)
	}
	if m.reasoningEffort != before.reasoningEffort {
		t.Fatalf("/effort max must not change the effort, got %q", m.reasoningEffort)
	}
	if m.zeromaxing != agent.ZeromaxingOff {
		t.Fatalf("/effort max must not arm the posture, got %v", m.zeromaxing)
	}
	if m.agentOptions.MaxTurns != before.agentOptions.MaxTurns {
		t.Fatalf("/effort max must not move the turn budget, got %d", m.agentOptions.MaxTurns)
	}
}

// (a) TUI: an explicit /effort <level> survives the posture.
func TestZeromaxingDoesNotOverrideExplicitEffortTUI(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	m, _ = m.handleEffortCommand("low")
	if m.reasoningEffort != "low" {
		t.Fatalf("setup: effort = %q, want low", m.reasoningEffort)
	}
	m, _ = m.handleEffortCommand(execprofile.Name)
	if m.reasoningEffort != "low" {
		t.Fatalf("effort = %q, the explicit low must survive the posture's high", m.reasoningEffort)
	}
	if m.execProfileAppliedEffort != "" {
		t.Fatalf("must not claim to have applied an effort it did not: %q", m.execProfileAppliedEffort)
	}
}

// (c) TUI: an explicit /turns pins the budget.
func TestZeromaxingDoesNotOverrideExplicitTurnsTUI(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	m, _ = m.handleEffortCommand(execprofile.Name)
	if m.agentOptions.MaxTurns != 320 {
		t.Fatalf("the posture must raise the budget to 320, got %d", m.agentOptions.MaxTurns)
	}
	m, _ = m.handleTurnsCommand("50")
	if m.agentOptions.MaxTurns != 50 {
		t.Fatalf("an explicit /turns must win, got %d", m.agentOptions.MaxTurns)
	}
	if !m.execProfileTurnsTouched {
		t.Fatal("/turns under a profile must mark the knob touched so a revert leaves it alone")
	}
}

// (p) revertExecProfile is knob-by-knob, and the posture is a FOURTH knob. All
// four must be restored — budget, effort, self-correct, and the posture moving
// to Exiting exactly once.
func TestRevertRestoresAllFourKnobs(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	baseTurns := m.agentOptions.MaxTurns
	baseEffort := m.reasoningEffort
	baseSelfCorrect := m.selfCorrectTests

	m, _ = m.handleEffortCommand(execprofile.Name)
	if m.agentOptions.MaxTurns != 320 || m.reasoningEffort != "high" || !m.selfCorrectTests {
		t.Fatalf("setup: knobs not applied: turns=%d effort=%q sc=%v",
			m.agentOptions.MaxTurns, m.reasoningEffort, m.selfCorrectTests)
	}
	if m.zeromaxing != agent.ZeromaxingEntering {
		t.Fatalf("selecting must enter the posture, got %v", m.zeromaxing)
	}

	// /effort auto is the effort namespace's off switch.
	m, _ = m.handleEffortCommand("auto")
	if m.agentOptions.MaxTurns != baseTurns {
		t.Fatalf("knob 1 (turn budget) = %d, want the displaced %d", m.agentOptions.MaxTurns, baseTurns)
	}
	if m.reasoningEffort != baseEffort {
		t.Fatalf("knob 2 (effort) = %q, want the displaced %q", m.reasoningEffort, baseEffort)
	}
	if m.selfCorrectTests != baseSelfCorrect {
		t.Fatalf("knob 3 (self-correct) = %v, want the displaced %v", m.selfCorrectTests, baseSelfCorrect)
	}
	if m.zeromaxing != agent.ZeromaxingExiting {
		t.Fatalf("knob 4 (posture) = %v, want ZeromaxingExiting so the exit notice fires once", m.zeromaxing)
	}
	if m.agentOptions.Zeromaxing != agent.ZeromaxingExiting {
		t.Fatal("the posture must reach agentOptions, or the loop never emits the exit notice")
	}
}

// /profile balanced is the other way out, and must behave identically.
func TestBothExitRoutesLeaveThePosture(t *testing.T) {
	viaEffort := zeromaxingTestModel(t, false)
	viaEffort, _ = viaEffort.handleEffortCommand(execprofile.Name)
	viaEffort, _ = viaEffort.handleEffortCommand("auto")

	viaProfile := zeromaxingTestModel(t, false)
	viaProfile, _ = viaProfile.handleProfileCommand(execprofile.Name)
	viaProfile, _ = viaProfile.handleProfileCommand("balanced")

	if viaEffort.zeromaxing != viaProfile.zeromaxing || viaEffort.zeromaxing != agent.ZeromaxingExiting {
		t.Fatalf("the two exit routes diverged: /effort auto -> %v, /profile balanced -> %v",
			viaEffort.zeromaxing, viaProfile.zeromaxing)
	}
	if viaEffort.execProfileName != "" || viaProfile.execProfileName != "" {
		t.Fatalf("both routes must clear the profile: %q / %q", viaEffort.execProfileName, viaProfile.execProfileName)
	}
}

// /effort auto under a NON-zeromaxing profile keeps its existing meaning: clear
// the effort, keep the profile. Reverting there would silently drop the profile.
func TestEffortAutoUnderOtherProfilesJustClearsTheEffort(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	m, _ = m.handleProfileCommand("thorough")
	if m.reasoningEffort != "high" || m.execProfileName != "thorough" {
		t.Fatalf("setup: thorough not applied: effort=%q profile=%q", m.reasoningEffort, m.execProfileName)
	}
	m, _ = m.handleEffortCommand("auto")
	if m.reasoningEffort != "" {
		t.Fatalf("/effort auto must clear the effort, got %q", m.reasoningEffort)
	}
	if m.execProfileName != "thorough" {
		t.Fatalf("/effort auto must NOT drop a non-zeromaxing profile, got %q", m.execProfileName)
	}
	if m.zeromaxing != agent.ZeromaxingOff {
		t.Fatalf("leaving thorough must not announce a posture exit, got %v", m.zeromaxing)
	}
}

// The posture lifecycle across runs: enter and exit each announce exactly once
// no matter how many runs follow.
func TestZeromaxingLifecycleAcrossRuns(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	m, _ = m.handleEffortCommand(execprofile.Name)
	if m.zeromaxing != agent.ZeromaxingEntering {
		t.Fatalf("after selecting: %v, want Entering", m.zeromaxing)
	}
	m = m.advanceZeromaxing()
	if m.zeromaxing != agent.ZeromaxingActive {
		t.Fatalf("after the first run: %v, want Active", m.zeromaxing)
	}
	m = m.advanceZeromaxing()
	if m.zeromaxing != agent.ZeromaxingActive {
		t.Fatalf("Active must be terminal while on, got %v", m.zeromaxing)
	}
	m, _ = m.handleEffortCommand("auto")
	if m.zeromaxing != agent.ZeromaxingExiting {
		t.Fatalf("after leaving: %v, want Exiting", m.zeromaxing)
	}
	m = m.advanceZeromaxing()
	if m.zeromaxing != agent.ZeromaxingOff {
		t.Fatalf("after the exit run: %v, want Off", m.zeromaxing)
	}
	m = m.advanceZeromaxing()
	if m.zeromaxing != agent.ZeromaxingOff {
		t.Fatalf("Off must be terminal, got %v", m.zeromaxing)
	}
}

// (m) A config that disabled the posture must refuse it, from BOTH entry
// points, and leave the active profile untouched rather than dropping to
// balanced.
func TestConfigCannotEnableZeromaxingTUI(t *testing.T) {
	for _, entry := range []struct {
		name string
		call func(model) (model, string)
	}{
		{"/effort", func(m model) (model, string) { return m.handleEffortCommand(execprofile.Name) }},
		{"/profile", func(m model) (model, string) { return m.handleProfileCommand(execprofile.Name) }},
	} {
		t.Run(entry.name, func(t *testing.T) {
			m := zeromaxingTestModel(t, true)
			m, _ = m.handleProfileCommand("thorough")
			turnsBefore := m.agentOptions.MaxTurns

			m, text := entry.call(m)
			if !strings.Contains(text, "Cannot use "+execprofile.Name) {
				t.Fatalf("a disabled workspace must refuse it, got %q", text)
			}
			if !strings.Contains(text, "disableZeromaxing") {
				t.Fatalf("the refusal must name the setting: %q", text)
			}
			if m.execProfileName != "thorough" || m.agentOptions.MaxTurns != turnsBefore {
				t.Fatalf("a refused switch must leave the active profile alone: profile=%q turns=%d",
					m.execProfileName, m.agentOptions.MaxTurns)
			}
			if m.zeromaxing != agent.ZeromaxingOff {
				t.Fatalf("a refused selection must not arm the posture, got %v", m.zeromaxing)
			}
		})
	}
}

// (n) The same session with it NOT disabled selects fine — the other half of
// the gate, so the test above cannot pass by the posture being broken outright.
func TestZeromaxingSelectableWhenNotDisabledTUI(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	m, text := m.handleEffortCommand(execprofile.Name)
	if m.execProfileName != execprofile.Name {
		t.Fatalf("must be selectable when not disabled, got %q (%s)", m.execProfileName, text)
	}
}

// Both selection paths consult the SAME rule. This is the standing-warning
// assertion: CLI and TUI apply profiles through different code with different
// state, so the one thing that must not diverge is the decision itself.
func TestSelectionRefusalAgreesAcrossPaths(t *testing.T) {
	profile, _ := execprofile.Lookup(execprofile.Name)
	for _, disabled := range []bool{true, false} {
		rule := execprofile.SelectionRefusal(profile, disabled)

		m := zeromaxingTestModel(t, disabled)
		m, text := m.handleEffortCommand(execprofile.Name)
		tuiRefused := m.execProfileName != execprofile.Name
		if tuiRefused != (rule != "") {
			t.Fatalf("disabled=%v: rule refusal=%q but TUI refused=%v (%s)", disabled, rule, tuiRefused, text)
		}
		if disabled && rule == "" {
			t.Fatal("a disabled workspace must produce a refusal for the CLI path too")
		}
	}
}

// (l) Degrade honestly. On a model with no effort ring the fill is skipped — and
// the status output must SAY so, while the rest of the posture still applies.
func TestZeromaxingOnUnsupportedModelStatesWhatItCouldNotRaise(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName:    "ollama",
		ModelName:       "kimi-k2.7-code:cloud",
		Provider:        &fakeProvider{},
		ProviderProfile: config.ProviderProfile{Name: "ollama", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "http://localhost:11434/v1", Model: "kimi-k2.7-code:cloud"},
		NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return &fakeProvider{}, nil
		},
	})
	if len(m.availableReasoningEfforts()) != 0 {
		t.Skip("this model gained an effort ring; the fixture no longer exercises the unsupported path")
	}

	m, text := m.handleEffortCommand(execprofile.Name)
	if m.execProfileName != execprofile.Name {
		t.Fatalf("must still WORK on a model that cannot take the effort, got %q", m.execProfileName)
	}
	if m.agentOptions.MaxTurns != 320 {
		t.Fatalf("the rest of the posture must still apply: turns = %d", m.agentOptions.MaxTurns)
	}
	if m.reasoningEffort != "" {
		t.Fatalf("an unsupported effort must not be applied, got %q", m.reasoningEffort)
	}
	if m.execProfileEffortUnraised != modelregistry.ReasoningEffortHigh {
		t.Fatalf("the skipped level must be recorded, got %q", m.execProfileEffortUnraised)
	}
	if !strings.Contains(text, "NOT raised") {
		t.Fatalf("the output must state what it could not raise, got %q", text)
	}
}

// (q) reconcileProfileAfterModelSwitch is the re-derive SIBLING of the fill site
// in handleProfileCommand. Both decide whether the profile's effort applies to
// the ACTIVE model, so both must record WHY when it does not.
func TestZeromaxingUnraisedEffortIsRecordedOnModelSwitch(t *testing.T) {
	m := profileSwitchModel(t)
	m, _ = m.handleEffortCommand(execprofile.Name)
	if m.reasoningEffort != modelregistry.ReasoningEffortHigh {
		t.Fatalf("setup: must fill high on a supporting model, got %q", m.reasoningEffort)
	}
	if m.execProfileEffortUnraised != "" {
		t.Fatalf("nothing was skipped, so nothing should be recorded: %q", m.execProfileEffortUnraised)
	}

	m, text, ok, _ := m.switchProviderModel("ollama", "kimi-k2.7-code:cloud")
	if !ok {
		t.Fatalf("switch to ollama failed: %q", text)
	}
	if m.reasoningEffort != "" {
		t.Fatalf("the unsupported level must be dropped, got %q", m.reasoningEffort)
	}
	if m.execProfileEffortUnraised != modelregistry.ReasoningEffortHigh {
		t.Fatalf("the dropped level must be recorded so the status can state it, got %q", m.execProfileEffortUnraised)
	}
	if _, out := m.handleProfileCommand("status"); !strings.Contains(out, "NOT raised") {
		t.Fatalf("status must state the effort it could not raise after a switch:\n%s", out)
	}

	// Switching BACK to a supporting model clears the reason, so a stale
	// "NOT raised" line never outlives the model that caused it.
	m, text, ok, _ = m.switchProviderModel("anthropic", "claude-sonnet-4.5")
	if !ok {
		t.Fatalf("switch back failed: %q", text)
	}
	if m.reasoningEffort != modelregistry.ReasoningEffortHigh {
		t.Fatalf("returning to a supporting model must refill high, got %q", m.reasoningEffort)
	}
	if m.execProfileEffortUnraised != "" {
		t.Fatalf("the reason must clear once the effort applies again, got %q", m.execProfileEffortUnraised)
	}
}

// Gate 3: BOTH status surfaces show the resolved state unambiguously — a user
// must see what actually reached the provider, not just the posture name.
func TestBothStatusSurfacesShowResolvedState(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	m, _ = m.handleEffortCommand(execprofile.Name)

	_, effortStatus := m.handleEffortCommand("")
	_, profileStatus := m.handleProfileCommand("status")

	for surface, text := range map[string]string{"/effort": effortStatus, "/profile status": profileStatus} {
		for _, want := range []string{"effort: high", "profile: " + execprofile.Name, "turns: 320"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s must show %q in its resolved state line:\n%s", surface, want, text)
			}
		}
		if !strings.Contains(text, execprofile.Delta) {
			t.Fatalf("%s must state the real delta:\n%s", surface, text)
		}
		for _, want := range []string{"320", "160", "sub-agents"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s must mention %q:\n%s", surface, want, text)
			}
		}
	}
	// Other profiles must NOT carry the posture's delta text.
	other := zeromaxingTestModel(t, false)
	_, otherText := other.handleProfileCommand("thorough")
	if strings.Contains(otherText, execprofile.Delta) {
		t.Fatalf("thorough must not claim the posture's delta:\n%s", otherText)
	}
}

// Gate 5: the footer chip is shown while the posture is on and hidden
// otherwise — including while Exiting, when the posture is already off.
func TestZeromaxingFooterChipVisibility(t *testing.T) {
	m := zeromaxingTestModel(t, false)
	if m.zeromaxingActive() {
		t.Fatal("nothing selected: the chip must be hidden")
	}
	m, _ = m.handleEffortCommand(execprofile.Name)
	if !m.zeromaxingActive() {
		t.Fatal("Entering: the chip must be shown")
	}
	if !strings.Contains(m.statusLine(120), zeromaxingChipLabel) {
		t.Fatalf("the footer must carry %q while on:\n%s", zeromaxingChipLabel, m.statusLine(120))
	}
	m = m.advanceZeromaxing()
	if !strings.Contains(m.statusLine(120), zeromaxingChipLabel) {
		t.Fatal("Active: the chip must still be shown")
	}
	m, _ = m.handleEffortCommand("auto")
	if m.zeromaxingActive() {
		t.Fatal("Exiting: the posture is already off, so the chip must be hidden")
	}
	if strings.Contains(m.statusLine(120), zeromaxingChipLabel) {
		t.Fatalf("the footer must drop the chip once off:\n%s", m.statusLine(120))
	}
}
