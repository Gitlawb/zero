package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/tools"
)

func sizedTestModel(width int) model {
	m := newModel(context.Background(), Options{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	// Ack the one-time header print so later settles aren't queued behind it.
	updated, _ = updated.(model).Update(flushedMsg{})
	return updated.(model)
}

func TestSettledRowsAdvanceFrontierAndLeaveLiveView(t *testing.T) {
	m := sizedTestModel(80)
	m.transcript = appendRow(m.transcript, rowUser, "hello there")
	m.transcript = appendRow(m.transcript, rowSystem, "noted")

	next, cmd := m.settleTranscript()
	if next.flushed != len(next.transcript) {
		t.Fatalf("expected frontier at %d, got %d", len(next.transcript), next.flushed)
	}
	if cmd == nil {
		t.Fatal("expected a scrollback print command for the settled rows")
	}
	view := next.View()
	if strings.Contains(view, "hello there") || strings.Contains(view, "noted") {
		t.Fatalf("flushed rows must not re-render in the live view, got %q", view)
	}
}

func TestAltScreenKeepsSettledRowsInManagedView(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(model)
	m.transcript = appendRow(m.transcript, rowUser, "hello there")
	m.transcript = appendRow(m.transcript, rowSystem, "noted")

	next, cmd := m.settleTranscript()
	if cmd != nil {
		t.Fatal("alt-screen mode should not print rows into native scrollback")
	}
	if next.flushed != 0 {
		t.Fatalf("alt-screen mode should keep the flush frontier unchanged, got %d", next.flushed)
	}
	view := next.View()
	if !strings.Contains(view, "hello there") || !strings.Contains(view, "noted") {
		t.Fatalf("settled rows should remain in the managed alt-screen view, got %q", view)
	}
}

func TestRunningToolCallBlocksFrontierUntilResult(t *testing.T) {
	m := sizedTestModel(80)
	m.pending = true
	m.activeRunID = 3
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowToolCall, id: "call_1", text: "tool call: read_file", tool: "read_file", runID: 3,
	})

	next, _ := m.settleTranscript()
	if next.flushed != 1 { // welcome row settles; the live call must block
		t.Fatalf("running tool call should block the frontier at 1, got %d", next.flushed)
	}

	next.transcript = appendTranscriptRow(next.transcript, transcriptRow{
		kind: rowToolResult, id: "call_1", text: "tool result: read_file ok", tool: "read_file",
		status: tools.StatusOK, detail: "data", runID: 3,
	})
	settled, cmd := next.settleTranscript()
	if settled.flushed != len(settled.transcript) {
		t.Fatalf("resolved call should settle through, frontier=%d rows=%d", settled.flushed, len(settled.transcript))
	}
	if cmd == nil {
		t.Fatal("expected the result card to flush to scrollback")
	}
}

func TestOrphanToolCallSettlesAfterRunEnds(t *testing.T) {
	m := sizedTestModel(80)
	m.pending = false // run is over; the unresolved call is an orphan
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowToolCall, id: "call_9", text: "tool call: bash", tool: "bash", runID: 2,
	})
	next, _ := m.settleTranscript()
	if next.flushed != len(next.transcript) {
		t.Fatalf("orphan call should settle once its run is over, frontier=%d", next.flushed)
	}
}

func TestClearResetsFlushFrontier(t *testing.T) {
	m := sizedTestModel(80)
	m.transcript = appendRow(m.transcript, rowUser, "first")
	m, _ = m.settleTranscript()
	if m.flushed == 0 {
		t.Fatal("precondition: something flushed")
	}

	m.input.SetValue("/clear")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if len(next.transcript) != 1 || next.transcript[0].kind != rowWelcome {
		t.Fatalf("expected /clear to reset transcript, got %#v", next.transcript)
	}
	if next.flushed > 1 {
		t.Fatalf("expected frontier reset after /clear, got %d", next.flushed)
	}
}

func TestEscCancellationLeavesVisibleMarker(t *testing.T) {
	m := sizedTestModel(80)
	m.pending = true
	m.activeRunID = 5
	m.runCancel = func() {}
	m.streamingText = "half an answer"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(model)
	if !transcriptContains(next.transcript, "Run cancelled.") {
		t.Fatalf("expected visible cancellation marker, got %#v", next.transcript)
	}
	if !transcriptContains(next.transcript, "half an answer") {
		t.Fatalf("expected the partial streamed answer to be preserved, got %#v", next.transcript)
	}
	if next.streamingText != "" {
		t.Fatal("streaming text should be cleared after cancel")
	}
}

func TestRepeatedProviderToolIDsKeepDistinctRows(t *testing.T) {
	rows := initialTranscript()
	// Same provider id in two different runs (Gemini-style) must NOT dedupe.
	rows = appendTranscriptRow(rows, transcriptRow{kind: rowToolCall, id: "gemini_tool_0", runID: 1, tool: "grep"})
	rows = appendTranscriptRow(rows, transcriptRow{kind: rowToolCall, id: "gemini_tool_0", runID: 2, tool: "grep"})
	if countTranscriptRows(rows, rowToolCall) != 2 {
		t.Fatalf("run-scoped dedup should keep both calls, got %#v", rows)
	}
	// And within one run, the ordinal suffix disambiguates repeats.
	if got := effectiveToolRowID("gemini_tool_0", 2); got != "gemini_tool_0#2" {
		t.Fatalf("effectiveToolRowID = %q", got)
	}
	if got := effectiveToolRowID("gemini_tool_0", 1); got != "gemini_tool_0" {
		t.Fatalf("first occurrence should keep the raw id, got %q", got)
	}
}

func TestComposerHistoryRecall(t *testing.T) {
	m := sizedTestModel(80)
	for _, prompt := range []string{"first input", "second input"} {
		m.input.SetValue(prompt)
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(model)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	if got := m.input.Value(); got != "second input" {
		t.Fatalf("first ↑ should recall the newest input, got %q", got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	if got := m.input.Value(); got != "first input" {
		t.Fatalf("second ↑ should recall the older input, got %q", got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if got := m.input.Value(); got != "second input" {
		t.Fatalf("↓ should walk back toward the newest input, got %q", got)
	}
}

func TestCutRunesNeverSplitsUTF8(t *testing.T) {
	text := "héllo wörld — ünïcode"
	for limit := 0; limit <= len(text); limit++ {
		cut := cutRunes(text, limit)
		if !strings.HasPrefix(text, cut) {
			t.Fatalf("cutRunes(%d) = %q is not a prefix", limit, cut)
		}
		for _, r := range cut {
			if r == '�' {
				t.Fatalf("cutRunes(%d) produced invalid UTF-8: %q", limit, cut)
			}
		}
	}
}

func TestWrapPlainTextPreservesIndentation(t *testing.T) {
	lines := wrapPlainText("plain\n    indented code line", 40)
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "    indented") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected indentation preserved, got %#v", lines)
	}
}

func TestLooksLikeDiffIgnoresPlainSeparators(t *testing.T) {
	if looksLikeDiff("build output\n--- summary ---\nall good") {
		t.Fatal("a '---' separator line must not trigger the diff renderer")
	}
	if !looksLikeDiff("--- a/f.go\n+++ b/f.go\n+new line") {
		t.Fatal("real unified diff headers must trigger the diff renderer")
	}
	if !looksLikeDiff("context\n@@ -1,2 +1,3 @@\n+x") {
		t.Fatal("a hunk header must trigger the diff renderer")
	}
}

func TestTruncateStyledLineClosesOpenHyperlink(t *testing.T) {
	line := hyperlink("file:///tmp/a.go", "averyveryverylongclickablepathsegment")
	cut := truncateStyledLine(line, 10)
	if !strings.Contains(cut, "\x1b]8;;\x1b\\") {
		t.Fatalf("truncated line must close its hyperlink, got %q", cut)
	}
}
