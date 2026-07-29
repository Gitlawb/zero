package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func detailModel(t *testing.T) model {
	t.Helper()
	m := admittedModel(t, diamondAdmitted())
	now := m.now()
	m.orchestrate.markStarted("a", "survey the packages", "plantask_1", now)
	m.orchestrate.markDone("a", "succeeded", 16470, now.Add(6*time.Second))
	m.orchestrate.linkCard("a", "specialist_aaa")
	m.specialists.start("a", "survey the packages", "specialist_aaa", now)
	m.specialists.complete("specialist_aaa", specialistCompleted, 0, "", now)
	m.specialists.setTokens("specialist_aaa", 16470)

	m.orchestrate.markStarted("b", "read the left branch", "plantask_2", now.Add(6*time.Second))
	m.specialists.start("b", "read the left branch", "plantask_2", now)
	m.specialists.incrementToolCount("plantask_2")
	m.specialists.setCurrentTool("plantask_2", "read_file", "internal/agent/loop.go")
	m.orchestrate.linkCard("b", "plantask_2")
	return m
}

// THE MOUNT-POINT INVARIANT. Closed, and with no plan at all, the view
// contributes nothing — which is what keeps a posture-off session unchanged.
func TestTheDetailViewRendersNothingWhenClosed(t *testing.T) {
	m := detailModel(t)
	if got := m.renderOrchestrateDetailOverlay(120); got != "" {
		t.Fatalf("a closed view must render nothing, got %q", got)
	}

	empty := model{now: func() time.Time { return time.Unix(1000, 0) }}
	empty.orchestrateDetail = true
	if got := empty.renderOrchestrateDetailOverlay(120); got != "" {
		t.Fatalf("with no plan there is nothing to open, got %q", got)
	}
}

// Opening lands on the task worth looking at: the one running.
func TestOpeningSelectsTheRunningTask(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail()
	if !m.orchestrateDetailOpen() {
		t.Fatal("the view did not open")
	}
	if got := m.orchestrate.tasks[m.orchestrateSelected].id; got != "b" {
		t.Fatalf("selected %q, want the running task", got)
	}
}

// ...and on the first failure when nothing is running, since that is what a
// user opens the view to read.
func TestOpeningSelectsTheFirstFailureWhenIdle(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	now := m.now()
	m.orchestrate.markStarted("a", "root", "k1", now)
	m.orchestrate.markDone("a", "succeeded", 0, now)
	m.orchestrate.markStarted("b", "left", "k2", now)
	m.orchestrate.markDone("b", "failed", 0, now)

	m = m.openOrchestrateDetail()
	if got := m.orchestrate.tasks[m.orchestrateSelected].id; got != "b" {
		t.Fatalf("selected %q, want the failed task", got)
	}
}

// Both panes render: the phase list on the left, the selected task's live
// detail on the right.
func TestTheViewShowsPhasesAndTheSelectedTasksDetail(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail()
	rendered := ansi.Strip(m.renderOrchestrateDetailOverlay(120))

	if !strings.Contains(rendered, "diamond") {
		t.Fatalf("the plan's name is missing:\n%s", rendered)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if !strings.Contains(rendered, id) {
			t.Fatalf("phase %q missing from the list:\n%s", id, rendered)
		}
	}
	// The running task's live agent state.
	for _, want := range []string{"Prompt", "read the left branch", "Activity", "read_file", "Outcome", "still running"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the detail pane does not show %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "1 tool call") {
		t.Errorf("the detail pane does not show the live tool count:\n%s", rendered)
	}
}

// Moving the selection changes what the right pane shows — the whole point of
// the two panes.
func TestMovingTheSelectionChangesTheDetail(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail()
	before := ansi.Strip(m.renderOrchestrateDetailOverlay(120))

	m = m.moveOrchestrateSelection(-1)
	after := ansi.Strip(m.renderOrchestrateDetailOverlay(120))
	if before == after {
		t.Fatal("moving the selection did not change the detail pane")
	}
	if !strings.Contains(after, "survey the packages") {
		t.Fatalf("the pane does not show the newly selected task:\n%s", after)
	}
	if !strings.Contains(after, "16,470 tokens") && !strings.Contains(after, "16470 tokens") {
		t.Fatalf("the pane does not show the selected task's spend:\n%s", after)
	}
}

// The selection clamps at both ends rather than wrapping onto an unrelated
// task.
func TestTheSelectionClampsAtBothEnds(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail()
	for range 10 {
		m = m.moveOrchestrateSelection(-1)
	}
	if m.orchestrateSelected != 0 {
		t.Fatalf("selection = %d, want it clamped at the first task", m.orchestrateSelected)
	}
	for range 20 {
		m = m.moveOrchestrateSelection(1)
	}
	if want := len(m.orchestrate.tasks) - 1; m.orchestrateSelected != want {
		t.Fatalf("selection = %d, want it clamped at %d", m.orchestrateSelected, want)
	}
}

// Esc closes it and the arrows move within it, through the real key handler —
// a keypress meant for the view must never fall through to the transcript.
func TestTheViewOwnsItsKeysWhileOpen(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail()
	m.width, m.height = 120, 40

	press := func(m model, key tea.KeyMsg) model {
		t.Helper()
		updated, _ := m.Update(key)
		return updated.(model)
	}

	selected := m.orchestrateSelected
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.orchestrateSelected == selected {
		t.Fatal("up did not move the selection")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.orchestrateSelected != selected {
		t.Fatal("down did not move the selection back")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.orchestrateDetailOpen() {
		t.Fatal("esc did not close the view")
	}
}

// A task that never started has no agent card, so the pane says what it knows
// rather than showing a row of zeroes.
func TestAPendingTaskShowsWhatItIsWaitingOn(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail()
	for m.orchestrate.tasks[m.orchestrateSelected].id != "d" {
		m = m.moveOrchestrateSelection(1)
	}
	rendered := ansi.Strip(m.renderOrchestrateDetailOverlay(120))
	if !strings.Contains(rendered, "waiting on b, c") {
		t.Fatalf("a pending task must say what it is waiting on:\n%s", rendered)
	}
	if strings.Contains(rendered, "0 tool calls") {
		t.Fatalf("a task that never started has no tool count to report:\n%s", rendered)
	}
}

// Narrow terminals drop the phase list rather than crushing both columns, and
// never cut mid-rune.
func TestTheViewSurvivesNarrowWidths(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail()
	for _, width := range []int{20, 40, 55, 56, 80, 200} {
		rendered := m.renderOrchestrateDetailOverlay(width)
		if rendered == "" {
			t.Fatalf("width %d rendered nothing", width)
		}
		if strings.Contains(rendered, "\ufffd") {
			t.Fatalf("width %d cut mid-rune:\n%s", width, rendered)
		}
	}
}

// A long prompt is summarised and says it was cut. The full text stays in the
// tool output — a display surface is never the data path.
func TestALongPromptIsTruncatedAndSaysSo(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	m.orchestrate.markStarted("a", strings.Repeat("verbose ", 200), "k", m.now())
	m = m.openOrchestrateDetail()

	rendered := ansi.Strip(m.renderOrchestrateDetailOverlay(100))
	if !strings.Contains(rendered, "…") {
		t.Fatalf("a truncated prompt must say so:\n%s", rendered)
	}
	if strings.Count(rendered, "verbose") > 60 {
		t.Fatalf("the prompt was dumped rather than summarised:\n%s", rendered)
	}
}

// Enter is only advertised when it does something. A hint for a key that is a
// no-op trains the user to ignore hints.
func TestEnterIsOnlyOfferedWhenThereIsASessionToOpen(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail() // selects "b", still running
	if got := m.selectedTaskSession(); got != "" {
		t.Fatalf("a running task has no child session yet, got %q", got)
	}
	running := ansi.Strip(m.renderOrchestrateDetailOverlay(120))
	if strings.Contains(running, "enter open agent") {
		t.Fatalf("enter is offered for a task with no session to open:\n%s", running)
	}

	m = m.moveOrchestrateSelection(-1) // "a", finished, has a real session
	if got := m.selectedTaskSession(); got != "specialist_aaa" {
		t.Fatalf("selectedTaskSession = %q, want the child's real session id", got)
	}
	finished := ansi.Strip(m.renderOrchestrateDetailOverlay(120))
	if !strings.Contains(finished, "enter open agent") {
		t.Fatalf("a task with a session must offer to open it:\n%s", finished)
	}
}

// The running task is distinguishable from one that has not started. Both fell
// into the pending glyph, so the phase list could not show what was in flight.
func TestThePhaseListMarksTheRunningTask(t *testing.T) {
	m := detailModel(t).openOrchestrateDetail()
	rendered := ansi.Strip(m.renderOrchestrateDetailOverlay(120))
	var runningRow, pendingRow string
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, " 2 b") {
			runningRow = line
		}
		if strings.Contains(line, " 3 c") {
			pendingRow = line
		}
	}
	if runningRow == "" || pendingRow == "" {
		t.Fatalf("phase rows missing:\n%s", rendered)
	}
	// Assert on the MARKER, not on the whole row: the columns are joined
	// horizontally, so every line also carries the detail pane, and comparing
	// rows let the running marker be removed without the test noticing.
	phaseOnly := func(line string) string {
		if index := strings.Index(line, "  "); index > 0 {
			return line[:index+1]
		}
		return line
	}
	if !strings.Contains(phaseOnly(runningRow), "▸") {
		t.Fatalf("the running task carries no in-flight marker:\n%s", rendered)
	}
	if strings.Contains(phaseOnly(pendingRow), "▸") {
		t.Fatalf("a pending task must not carry the in-flight marker:\n%s", rendered)
	}
}

// Pressing the PLAN header opens the view — the interaction that was asked for.
func TestClickingPlanOpensTheDetailView(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 120, 40
	m.altScreen = true
	m.activeRunID = 1
	updated, _ := m.Update(diamondAdmitted())
	m = updated.(model)

	if m.orchestrateDetailOpen() {
		t.Fatal("the view must start closed")
	}

	// Locate the header row in the rendered footer and click it, rather than
	// guessing a coordinate: the footer's height varies with the composer and
	// the pinned plan panel.
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(m.chatColumnWidth()), m.footerView(m.chatColumnWidth()))
	row := -1
	for index, line := range frame.footerLines {
		if strings.Contains(ansi.Strip(line), "PLAN diamond") {
			row = index
			break
		}
	}
	if row < 0 {
		t.Fatal("the plan header is not in the footer")
	}

	click := tea.MouseClickMsg{X: frame.footerRect.x + 2, Y: frame.footerRect.y + row, Button: tea.MouseLeft}
	updated, _ = m.Update(click)
	m = updated.(model)
	if !m.orchestrateDetailOpen() {
		t.Fatal("clicking the PLAN header did not open the detail view")
	}
}
