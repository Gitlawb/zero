package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

const sampleEditDiff = `--- a/app.js
+++ b/app.js
@@ -1,3 +1,4 @@
 function addToCart(id) {
-  // placeholder
+  cart.push(id)
+  save()
 }`

// longEditDiff is a diff with more than cardBodyMaxLines content rows, so the
// inline card collapses when the Code card is showing it.
func longEditDiff() string {
	var b strings.Builder
	b.WriteString("--- a/big.js\n+++ b/big.js\n@@ -1,1 +1,30 @@\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "+line %d\n", i)
	}
	return strings.TrimRight(b.String(), "\n")
}

// editingRunModel returns an alt-screen model mid-run whose active run has
// produced a file edit, so the Code card is active.
func editingRunModel(t *testing.T, diff string) model {
	t.Helper()
	m := mouseTestModel() // alt-screen, width 100, height 30
	m.pending = true
	m.activeRunID = 7
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowToolResult, tool: "edit_file", id: "e1", runID: 7,
		status: tools.StatusOK, detail: diff,
	})
	return m
}

func TestDiffPathAndCounts(t *testing.T) {
	if got := diffPath(sampleEditDiff); got != "app.js" {
		t.Fatalf("diffPath = %q, want app.js", got)
	}
	if got := diffPath("no headers here"); got != "" {
		t.Fatalf("diffPath with no headers = %q, want empty", got)
	}
	adds, dels := diffCounts(sampleEditDiff)
	if adds != 2 || dels != 1 {
		t.Fatalf("diffCounts = (+%d, −%d), want (+2, −1)", adds, dels)
	}
}

func TestCurrentEditDiffFindsLatestEditInActiveRun(t *testing.T) {
	m := editingRunModel(t, sampleEditDiff)
	path, diff := m.currentEditDiff()
	if path != "app.js" || diff == "" {
		t.Fatalf("currentEditDiff = (%q, %d bytes), want app.js with a diff", path, len(diff))
	}

	// A later edit in the same run wins over the earlier one.
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowToolResult, tool: "write_file", id: "e2", runID: 7,
		status: tools.StatusOK, detail: "--- a/style.css\n+++ b/style.css\n@@ -0,0 +1,1 @@\n+body{}",
	})
	if path, _ := m.currentEditDiff(); path != "style.css" {
		t.Fatalf("currentEditDiff after second edit = %q, want style.css", path)
	}

	// Edits from another run, and failed edits, are ignored.
	other := editingRunModel(t, sampleEditDiff)
	other.activeRunID = 99 // the only edit row belongs to run 7
	if _, diff := other.currentEditDiff(); diff != "" {
		t.Fatal("an edit from a different run must not feed the Code card")
	}
	failed := editingRunModel(t, sampleEditDiff)
	failed.transcript[len(failed.transcript)-1].status = tools.StatusError
	if _, diff := failed.currentEditDiff(); diff != "" {
		t.Fatal("a failed edit must not feed the Code card")
	}
}

func TestCodePanelActiveGating(t *testing.T) {
	if !editingRunModel(t, sampleEditDiff).codePanelActive() {
		t.Fatal("Code card should show during a run that edited a file")
	}

	done := editingRunModel(t, sampleEditDiff)
	done.pending = false
	if done.codePanelActive() {
		t.Fatal("Code card must hide once the run finishes")
	}

	inline := editingRunModel(t, sampleEditDiff)
	inline.altScreen = false
	if inline.codePanelActive() {
		t.Fatal("no Code card in inline mode")
	}

	noEdit := mouseTestModel()
	noEdit.pending = true
	noEdit.activeRunID = 3
	noEdit.transcript = appendTranscriptRow(noEdit.transcript, transcriptRow{kind: rowToolCall, tool: "bash", id: "b1", runID: 3})
	if noEdit.codePanelActive() {
		t.Fatal("no Code card for a run that has not edited anything")
	}
}

func TestRenderCodeCardShowsPathAndColoredDiff(t *testing.T) {
	m := editingRunModel(t, sampleEditDiff)
	plain := plainRender(t, m.renderCodeCard("app.js", sampleEditDiff))

	for _, want := range []string{"Code", "app.js", "+2", "−1", "cart.push(id)", "placeholder", "╭", "╰"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("Code card missing %q, got:\n%s", want, plain)
		}
	}
}

func TestRightColumnStacksPlanThenCode(t *testing.T) {
	m := editingRunModel(t, sampleEditDiff)
	// Same run also produced a plan → both cards stack.
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolCall, tool: planToolName, id: "p1", runID: 7})

	plain := plainRender(t, m.renderRightColumn())
	planAt := strings.Index(plain, "Plan")
	codeAt := strings.Index(plain, "Code")
	if planAt < 0 || codeAt < 0 {
		t.Fatalf("right column should carry both cards, got:\n%s", plain)
	}
	if planAt > codeAt {
		t.Fatal("Plan card should stack above the Code card")
	}

	// Idle model → empty column.
	if got := mouseTestModel().renderRightColumn(); got != "" {
		t.Fatalf("idle right column = %q, want empty", got)
	}
}

func TestRightColumnNeverBuriesTheWholeChat(t *testing.T) {
	m := editingRunModel(t, longEditDiff())
	// A long plan on top of the long diff makes the column taller than the chat.
	for i := 0; i < 14; i++ {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolCall, tool: planToolName, id: fmt.Sprintf("p%d", i), runID: 7})
	}
	m.height = 24

	rows := make([]string, m.height)
	for i := range rows {
		rows[i] = fmt.Sprintf("row%d", i)
	}
	out := strings.Split(m.composeWithPlanPanel(strings.Join(rows, "\n")), "\n")

	// The last planPanelReserveRows lines must remain untouched transcript content
	// (no widget border overlaid), so the composer/newest lines stay visible.
	for i := len(out) - planPanelReserveRows; i < len(out); i++ {
		if strings.ContainsAny(out[i], "│╭╰") {
			t.Fatalf("reserved bottom row %d was covered by the column: %q", i, out[i])
		}
	}
}

func TestEditCardsCollapseOnlyWhileCodePanelActive(t *testing.T) {
	diff := longEditDiff()
	m := editingRunModel(t, diff)
	m.transcript[len(m.transcript)-1].detail = diff // the active edit is the long one
	row := m.transcript[len(m.transcript)-1]
	rc := buildRowContext(m.transcript)

	// Code card active → the inline edit card collapses to a one-line record.
	live := plainRender(t, m.renderRow(row, 80, rc))
	if !strings.Contains(live, "click to expand") {
		t.Fatalf("edit card should collapse while the Code card is active, got:\n%s", live)
	}
	if strings.Contains(live, "line 5") {
		t.Fatal("collapsed edit card must not show the diff body in the chat")
	}

	// Run finished (Code card gone) → the diff returns to the chat inline.
	done := m
	done.pending = false
	full := plainRender(t, done.renderRow(row, 80, buildRowContext(done.transcript)))
	if !strings.Contains(full, "line 5") {
		t.Fatalf("once the run ends the diff should render inline, got:\n%s", full)
	}
}

func TestShortEditDiffCollapsesInCompactMode(t *testing.T) {
	// A short edit diff (< cardBodyMaxLines) would normally render inline. But while
	// the live Code card shows the same diff, leaving the inline card expanded
	// duplicates it — so in compact-edit mode even a short edit collapses.
	m := editingRunModel(t, sampleEditDiff)
	row := m.transcript[len(m.transcript)-1]
	rc := buildRowContext(m.transcript)

	live := plainRender(t, m.renderRow(row, 80, rc))
	if !strings.Contains(live, "click to expand") {
		t.Fatalf("short edit card should collapse in compact-edit mode, got:\n%s", live)
	}
	if strings.Contains(live, "cart.push(id)") {
		t.Fatal("collapsed short edit card must not duplicate the diff body in the chat")
	}

	// Run finished (Code card gone) → the short diff renders inline again.
	done := m
	done.pending = false
	full := plainRender(t, done.renderRow(row, 80, buildRowContext(done.transcript)))
	if !strings.Contains(full, "cart.push(id)") {
		t.Fatalf("once the run ends the short diff should render inline, got:\n%s", full)
	}
}
