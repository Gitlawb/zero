package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

// TestSidebarPanelShowsRealSessionsAndChangedFiles asserts the rail renders the
// real resumable session list (active row marked) and the files this session has
// changed (read from the transcript's edit tool calls), filled to the body height.
func TestSidebarPanelShowsRealSessionsAndChangedFiles(t *testing.T) {
	m := newModel(context.Background(), Options{Cwd: "/workspace/zero"})
	m.altScreen = true
	m.sidebarVisible = true
	m.activeSession = sessions.Metadata{SessionID: "s1"}
	m.sidebarSessions = []sessions.Metadata{
		{SessionID: "s1", Title: "auth middleware refactor"},
		{SessionID: "s2", Title: "fix websocket backoff"},
	}
	m.transcript = []transcriptRow{
		{kind: rowToolCall, id: "c1", tool: "edit_file", detail: "internal/auth/middleware.go"},
		{kind: rowToolResult, id: "c1", tool: "edit_file", status: tools.StatusOK, detail: "+++ b/internal/auth/middleware.go\n@@ -1 +1 @@\n+x"},
	}

	const height = 20
	panel := m.sidebarPanelLines(height)
	if len(panel) != height {
		t.Fatalf("sidebar panel height = %d, want %d (filled rail)", len(panel), height)
	}
	got := plainRender(t, strings.Join(panel, "\n"))
	for _, want := range []string{"SESSIONS", "auth middleware refactor", "fix websocket backoff", "● ", "CHANGED FILES", "middleware.go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sidebar = missing %q in:\n%s", want, got)
		}
	}
}

// TestSidebarHiddenLeavesBodyUnchanged guards the default: a hidden sidebar never
// touches the transcript body (byte-identical), so the chat is unaffected.
func TestSidebarHiddenLeavesBodyUnchanged(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.altScreen = true
	m.sidebarVisible = false
	body := []string{"alpha line", "beta line", "gamma line"}
	original := append([]string(nil), body...)
	out := m.applySidebar(body, 120)
	for i := range original {
		if out[i] != original[i] {
			t.Fatalf("hidden sidebar mutated body line %d: %q -> %q", i, original[i], out[i])
		}
	}
}

// TestSidebarTogglePicksUpSessions checks ctrl+g flips visibility (alt-screen) and
// that a narrow terminal does not dock the rail.
func TestSidebarToggleVisibility(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.altScreen = true
	if m.sidebarVisible {
		t.Fatal("sidebar should start hidden")
	}
	m = m.toggleSidebar()
	if !m.sidebarVisible {
		t.Fatal("toggleSidebar should show the rail in alt-screen mode")
	}
	if sidebarFits(40) {
		t.Fatal("a 40-col terminal is too narrow to dock the sidebar")
	}
	if !sidebarFits(120) {
		t.Fatal("a 120-col terminal should fit the sidebar")
	}
}

// TestWordmarkIsZeroBrand pins the inline wordmark text (ZER + O).
func TestWordmarkIsZeroBrand(t *testing.T) {
	if got := plainRender(t, wordmark()); got != "ZERO" {
		t.Fatalf("wordmark = %q, want ZERO", got)
	}
}
