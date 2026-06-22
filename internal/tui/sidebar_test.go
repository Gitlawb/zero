package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func sidebarTestModel() model {
	m := newModel(context.Background(), Options{ProviderName: "test-provider", ModelName: "test-model"})
	m.width = 100
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true
	// Real conversation content so the home-screen gate doesn't suppress the
	// sidebar (it stays single-column until the transcript has non-welcome rows).
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file", detail: "main.go"})
	return m
}

func TestSidebarWidthClampsAndSuppresses(t *testing.T) {
	if got := sidebarWidth(40); got != 0 {
		t.Fatalf("sidebarWidth(40) = %d, want 0 (too narrow for a second column)", got)
	}
	if got := sidebarWidth(100); got < sidebarMinWidth || got > sidebarMaxWidth {
		t.Fatalf("sidebarWidth(100) = %d, want within [%d,%d]", got, sidebarMinWidth, sidebarMaxWidth)
	}
	if got := sidebarWidth(400); got != sidebarMaxWidth {
		t.Fatalf("sidebarWidth(400) = %d, want clamped to %d", got, sidebarMaxWidth)
	}
}

func TestSidebarActiveGating(t *testing.T) {
	m := sidebarTestModel()
	if !m.sidebarActive() {
		t.Fatalf("expected sidebar active for wide alt-screen model")
	}

	// Home/welcome screen (no real conversation yet): single column.
	home := m
	home.transcript = nil
	if home.sidebarActive() {
		t.Fatalf("sidebar should be inactive on the empty home screen")
	}

	// Too narrow: single column only.
	narrow := m
	narrow.width = 50
	if narrow.sidebarActive() {
		t.Fatalf("sidebar should be inactive on a narrow terminal")
	}

	// Inline (non-alt-screen) mode keeps the legacy single-column layout.
	inline := m
	inline.altScreen = false
	if inline.sidebarActive() {
		t.Fatalf("sidebar should be inactive in inline mode")
	}

	// Subchat drill-in owns the full width.
	sub := m
	sub.subchat.active = true
	if sub.sidebarActive() {
		t.Fatalf("sidebar should be inactive during subchat drill-in")
	}
}

func TestChatColumnWidthLeavesRoomForSidebar(t *testing.T) {
	m := sidebarTestModel()
	chatW := m.chatColumnWidth()
	sidebarW := sidebarWidth(m.width)
	if chatW+1+sidebarW != m.width {
		t.Fatalf("chat(%d) + divider(1) + sidebar(%d) = %d, want total width %d",
			chatW, sidebarW, chatW+1+sidebarW, m.width)
	}

	// When the sidebar is inactive, chat width is the full chat width.
	narrow := m
	narrow.width = 50
	if got := narrow.chatColumnWidth(); got != chatWidth(narrow.width) {
		t.Fatalf("narrow chatColumnWidth = %d, want full chatWidth %d", got, chatWidth(narrow.width))
	}
}

func TestRenderContextSidebarDimensions(t *testing.T) {
	m := sidebarTestModel()
	width := sidebarWidth(m.width)
	const height = 20
	lines := m.renderContextSidebar(width, height)
	if len(lines) != height {
		t.Fatalf("sidebar produced %d lines, want exactly %d", len(lines), height)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != width {
			t.Fatalf("sidebar line %d width = %d, want exactly %d", i, w, width)
		}
	}
	// Section headers and the token floor should be present.
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "AGENTS") {
		t.Fatalf("sidebar missing AGENTS header:\n%s", plain)
	}
	if !strings.Contains(plain, "PLAN") {
		t.Fatalf("sidebar missing PLAN header:\n%s", plain)
	}
	if !strings.Contains(plain, "tokens") {
		t.Fatalf("sidebar missing token floor:\n%s", plain)
	}
}

func TestSidebarShowsSpawnedAgents(t *testing.T) {
	m := sidebarTestModel()
	now := time.Now()
	// One running subagent with live tool activity, one completed.
	m.specialists.start("explorer", "map the codebase", "sess-1", now)
	m.specialists.setCurrentTool("sess-1", "grep", "auth")
	m.specialists.incrementToolCount("sess-1")
	m.specialists.start("reviewer", "review diff", "sess-2", now)
	m.specialists.complete("sess-2", specialistCompleted, 0, "", now)

	width := sidebarWidth(m.width)
	plain := stripSidebar(m.sidebarAgentLines(width))
	if !strings.Contains(plain, "explorer") {
		t.Fatalf("running subagent name missing:\n%s", plain)
	}
	if !strings.Contains(plain, "reviewer") {
		t.Fatalf("completed subagent name missing:\n%s", plain)
	}
	// The running subagent surfaces its live working detail (current tool).
	if !strings.Contains(plain, "grep") {
		t.Fatalf("running subagent working detail missing:\n%s", plain)
	}
	// Header shows running/total.
	hdr := stripSidebar([]string{m.sidebarAgentHeader(width)})
	if !strings.Contains(hdr, "AGENTS") || !strings.Contains(hdr, "1/2") {
		t.Fatalf("agent header should show AGENTS 1/2, got: %s", hdr)
	}
}

func TestSidebarPlanReflectsState(t *testing.T) {
	m := sidebarTestModel()
	m.plan.steps = []planStep{
		{content: "read code", status: "completed"},
		{content: "refactor auth", status: "in_progress"},
		{content: "run tests", status: "pending"},
	}
	header := plainRender(t, m.sidebarPlanHeader(40))
	if !strings.Contains(header, "PLAN") || !strings.Contains(header, "1/3") {
		t.Fatalf("plan header = %q, want PLAN with 1/3 count", header)
	}
	lines := m.sidebarPlanLines(40)
	if len(lines) != 3 {
		t.Fatalf("plan lines = %d, want 3", len(lines))
	}
	joined := stripSidebar(lines)
	if !strings.Contains(joined, "✓") || !strings.Contains(joined, "•") || !strings.Contains(joined, "○") {
		t.Fatalf("plan lines missing status glyphs:\n%s", joined)
	}
}

func TestJoinColumnsAligns(t *testing.T) {
	chat := []string{"hello", "world", "third row that is longer"}
	sidebar := []string{"A", "B"}
	const chatW, sidebarW = 12, 6
	rows := joinColumns(chat, sidebar, chatW, sidebarW)
	if len(rows) != 3 {
		t.Fatalf("joined %d rows, want max(3,2)=3", len(rows))
	}
	want := chatW + 1 + sidebarW
	for i, row := range rows {
		if w := lipgloss.Width(row); w != want {
			t.Fatalf("row %d width = %d, want %d", i, w, want)
		}
	}
}

func TestTwoColumnTranscriptViewWidth(t *testing.T) {
	m := sidebarTestModel()
	out := m.twoColumnTranscriptView()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("two-column view = %d lines, want terminal height %d", len(lines), m.height)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != m.width {
			t.Fatalf("two-column row %d width = %d, want full width %d", i, w, m.width)
		}
	}
}

// stripSidebar joins sidebar lines and strips ANSI for content assertions.
func stripSidebar(lines []string) string {
	return ansiPattern.ReplaceAllString(strings.Join(lines, "\n"), "")
}
