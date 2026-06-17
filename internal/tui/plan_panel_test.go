package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

// plannedRunModel returns an alt-screen model mid-run whose active run has called
// update_plan (so the plan widget is active).
func plannedRunModel(t *testing.T) model {
	t.Helper()
	m := mouseTestModel() // alt-screen, width 100, height 30
	m.pending = true
	m.activeRunID = 7
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolCall, tool: planToolName, id: "p1", runID: 7})
	return m
}

func TestPlanPanelShowsOnlyDuringAPlannedRun(t *testing.T) {
	// Idle, no plan → hidden.
	if mouseTestModel().planPanelActive() {
		t.Fatal("panel must be hidden when idle")
	}

	// Pending but the run never called update_plan (e.g. "hi", or a delete task) →
	// hidden, even if an OLD plan still lingers in the tool's state.
	trivial := mouseTestModel()
	trivial.pending = true
	trivial.activeRunID = 9
	trivial.transcript = appendTranscriptRow(trivial.transcript, transcriptRow{kind: rowToolCall, tool: "bash", id: "b1", runID: 9})
	if trivial.planPanelActive() {
		t.Fatal("panel must stay hidden for a run that produced no plan")
	}

	// Pending and this run called update_plan → shown.
	if !plannedRunModel(t).planPanelActive() {
		t.Fatal("panel should show during a run that called update_plan")
	}
}

func TestPlanPanelHiddenWhenTerminalTooShort(t *testing.T) {
	// height <= planPanelReserveRows leaves zero drawable rows above the bottom
	// reserve, so composeWithPlanPanel overlays nothing. Activation must agree.
	short := plannedRunModel(t)
	short.height = planPanelReserveRows
	if short.rightColumnBase() {
		t.Fatal("right column must hide when height <= planPanelReserveRows")
	}
	if short.planPanelActive() {
		t.Fatal("plan panel must hide when height <= planPanelReserveRows")
	}
	// One more row → there is capacity for an overlay, so it shows again.
	short.height = planPanelReserveRows + 1
	if !short.rightColumnBase() {
		t.Fatal("right column should show once a drawable row exists above the reserve")
	}
}

func TestPlanPanelHiddenWhenNotApplicable(t *testing.T) {
	narrow := plannedRunModel(t)
	narrow.width = 60 // 60 - 40 < 48 → keep chat readable
	if narrow.planPanelActive() {
		t.Fatal("panel must hide on a narrow terminal")
	}

	done := plannedRunModel(t)
	done.pending = false
	if done.planPanelActive() {
		t.Fatal("panel must hide once the run finishes (not pending)")
	}

	inSetup := plannedRunModel(t)
	inSetup.setup.visible = true
	if inSetup.planPanelActive() {
		t.Fatal("no panel during setup")
	}

	inline := plannedRunModel(t)
	inline.altScreen = false
	if inline.planPanelActive() {
		t.Fatal("no panel in inline mode")
	}
}

func TestComposeWithPlanPanelFloatsTopRightFullWidth(t *testing.T) {
	m := plannedRunModel(t)
	content := strings.Join([]string{"row0", "row1", "row2", "row3", "row4", "row5", "row6", "row7", "row8", "row9"}, "\n")

	out := m.composeWithPlanPanel(content)
	lines := strings.Split(out, "\n")
	plain := plainRender(t, out)

	if !strings.Contains(plain, "Plan") || !strings.Contains(plain, "╭") || !strings.Contains(plain, "╰") {
		t.Fatalf("expected the floating plan widget, got:\n%s", plain)
	}
	// The widget is compact (well under the content height); only its rows are
	// overlaid (full width = chat + widget), and the rows below stay untouched.
	full := chatWidth(m.width)
	widgetHeight := len(strings.Split(m.renderPlanPanel(), "\n"))
	if widgetHeight >= len(lines) {
		t.Fatalf("widget height %d should be compact (under content height)", widgetHeight)
	}
	for index := 0; index < widgetHeight; index++ {
		if w := lipgloss.Width(lines[index]); w != full {
			t.Fatalf("overlaid line %d width = %d, want full chat width %d", index, w, full)
		}
	}
	if got := plainRender(t, lines[len(lines)-1]); got != "row9" {
		t.Fatalf("last row should be untouched transcript content, got %q", got)
	}

	// Inactive → no-op.
	if got := mouseTestModel().composeWithPlanPanel(content); got != content {
		t.Fatal("an inactive widget must not alter the content")
	}
}

func TestPlanActivityLabel(t *testing.T) {
	m := mouseTestModel()
	if m.planActivityLabel() != "" {
		t.Fatal("no activity label when idle")
	}
	m.pending = true
	if got := m.planActivityLabel(); got != "Thinking" {
		t.Fatalf("pending with no tool = %q, want Thinking", got)
	}
	m.streamingText = "writing the answer"
	if got := m.planActivityLabel(); got != "Responding" {
		t.Fatalf("streaming = %q, want Responding", got)
	}
}

func TestPlanActivityFromRunningTool(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolCall, id: "c1", tool: "write_file", runID: 1})
	if got := m.planActivityLabel(); got != "Building" {
		t.Fatalf("running write_file = %q, want Building", got)
	}
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolResult, id: "c1", tool: "write_file", runID: 1})
	if got := m.planActivityLabel(); got != "Thinking" {
		t.Fatalf("resolved tool = %q, want Thinking", got)
	}
}

func TestToolActivityKindAndStatusGlyphs(t *testing.T) {
	for tool, want := range map[string]string{
		"update_plan": "plan", "write_file": "build", "edit_file": "build",
		"read_file": "scan", "grep": "scan", "bash": "shell", "web_search": "search", "mystery": "",
	} {
		if got := toolActivityKind(tool); got != want {
			t.Fatalf("toolActivityKind(%q) = %q, want %q", tool, got, want)
		}
	}
	for status, want := range map[string]string{"in_progress": "◐", "done": "✓", "pending": "○", "": "○", "failed": "✗"} {
		if got, _ := planStatusGlyph(status); got != want {
			t.Fatalf("planStatusGlyph(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestCutRunesEllipsis(t *testing.T) {
	if got := cutRunesEllipsis("short", 10); got != "short" {
		t.Fatalf("no-truncation = %q", got)
	}
	if got := cutRunesEllipsis("truncate me here", 8); got != "truncat…" {
		t.Fatalf("truncation = %q, want %q", got, "truncat…")
	}
}

func TestFormatPanelElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0:00", 12 * time.Second: "0:12", 83 * time.Second: "1:23", 605 * time.Second: "10:05"} {
		if got := formatPanelElapsed(d); got != want {
			t.Fatalf("formatPanelElapsed(%s) = %q, want %q", d, got, want)
		}
	}
}
