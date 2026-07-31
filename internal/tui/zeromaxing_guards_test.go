package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/execprofile"
)

// /effort zeromaxing delegates to handleProfileCommand, which mutates the turn
// budget, self-correct and the shared orchestrate gate. /profile refuses that
// mid-run because the budget propagates to sub-agents spawned later in the same
// run — reaching the same mutation through the effort namespace must not skip
// the guard.
func TestEffortZeromaxingRefusesMidRun(t *testing.T) {
	m := model{pending: true}
	before := m.execProfileName

	got, text := m.handleEffortCommand(execprofile.Name)

	if !strings.Contains(strings.ToLower(text), "finish or stop") {
		t.Errorf("expected a mid-run refusal, got %q", text)
	}
	if got.execProfileName != before {
		t.Errorf("the posture changed mid-run: %q -> %q", before, got.execProfileName)
	}
}

// Idle, the same command still works — the guard must not disable the feature.
func TestEffortZeromaxingWorksWhenIdle(t *testing.T) {
	m := model{}
	got, text := m.handleEffortCommand(execprofile.Name)
	if strings.Contains(strings.ToLower(text), "finish or stop") {
		t.Fatalf("refused while idle: %q", text)
	}
	if got.execProfileName != execprofile.Name {
		t.Errorf("posture = %q, want %q", got.execProfileName, execprofile.Name)
	}
}

// Every sidebar hit-tester carries its own modal guard, because sidebarActive()
// deliberately does not exclude the palette. The PLAN header was missing one, so
// clicking it while the / palette was open toggled the section behind the
// overlay.
func TestOrchestrateHeaderIgnoresClicksBehindAModal(t *testing.T) {
	base := sidebarDetailModel(t)
	sidebarW := sidebarWidth(base.width)
	agentBody := len(base.sidebarAgentLines(sidebarW))
	if agentBody == 0 {
		agentBody = 1
	}
	headerRow := 1 + agentBody + 1
	x := base.chatColumnWidth() + 4
	click := tea.MouseClickMsg{X: x, Y: headerRow, Button: tea.MouseLeft}

	// Sanity: the click lands on the header when nothing is in the way.
	if !base.orchestrateHeaderAtMouse(click) {
		t.Skip("the header is not where this test thinks it is")
	}

	// The `/` palette specifically. sidebarAvailable deliberately does NOT
	// suppress the sidebar for it — a palette must not reflow the layout — so
	// sidebarActive() stays true and every hit-tester has to refuse on its own.
	// A picker would be caught by sidebarAvailable already and proves nothing.
	withPalette := base
	withPalette.suggestions = []commandSuggestion{{Name: "/model", Desc: "Pick a model."}}
	if !withPalette.suggestionsActive() {
		t.Fatal("precondition: the palette should be active")
	}
	if !withPalette.sidebarActive() {
		t.Fatal("precondition: the palette must not collapse the sidebar")
	}
	if withPalette.orchestrateHeaderAtMouse(click) {
		t.Error("PLAN header answered a click aimed at the open / palette")
	}
}
