package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

// TestFileViewOpenExitRestoresScroll: opening saves the chat scroll position,
// resets it for the file body, and Esc restores it; switching files while open
// keeps the ORIGINAL saved position (not the file view's own).
func TestFileViewOpenExitRestoresScroll(t *testing.T) {
	m := filesPanelTestModel()
	m.chatScrollOffset = 12

	m = m.openFileView("web/app.js")
	if !m.fileView.active || m.fileView.mode != fileViewDiff {
		t.Fatalf("open should activate in diff mode: %+v", m.fileView)
	}
	if m.chatScrollOffset != 0 || m.fileView.parentScrollOffset != 12 {
		t.Fatalf("open should reset scroll and save the parent offset: offset=%d saved=%d", m.chatScrollOffset, m.fileView.parentScrollOffset)
	}

	m.chatScrollOffset = 5 // scrolled within the file body
	m = m.openFileView("internal/tui/sidebar.go")
	if m.fileView.parentScrollOffset != 12 {
		t.Fatalf("switching files must keep the original parent offset, got %d", m.fileView.parentScrollOffset)
	}

	m = m.exitFileView()
	if m.fileView.active || m.chatScrollOffset != 12 {
		t.Fatalf("exit should restore the chat scroll: active=%v offset=%d", m.fileView.active, m.chatScrollOffset)
	}
}

// TestFileViewEscAndModeKeys: Esc exits the view via the model's key handler;
// d/f switch modes while the composer is empty and never while typing.
func TestFileViewEscAndModeKeys(t *testing.T) {
	m := filesPanelTestModel()
	m = m.openFileView("web/app.js")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = updated.(model)
	if m.fileView.mode != fileViewFull {
		t.Fatal("f should switch to full mode")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(model)
	if m.fileView.mode != fileViewDiff {
		t.Fatal("d should switch back to diff mode")
	}

	// With text in the composer, d/f type as normal characters.
	m.input.SetValue("say")
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = updated.(model)
	if m.fileView.mode != fileViewDiff {
		t.Fatal("f while typing must not hijack the composer")
	}
	m.input.SetValue("")

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(model)
	if m.fileView.active {
		t.Fatal("Esc should exit the file view")
	}
}

// TestFileViewDiffBody: diff mode stacks the file's edit cards chronologically
// with "edit N of M" labels; a file with no recorded edits shows the quiet
// placeholder.
func TestFileViewDiffBody(t *testing.T) {
	m := filesPanelTestModel()
	m = m.openFileView("internal/tui/sidebar.go")
	body := plainRender(t, m.renderFileViewDiff(78))
	if !strings.Contains(body, "edit 1 of 2") || !strings.Contains(body, "edit 2 of 2") {
		t.Fatalf("expected chronological edit labels:\n%s", body)
	}
	if !strings.Contains(body, "added one") || !strings.Contains(body, "three") {
		t.Errorf("expected both diffs' content:\n%s", body)
	}

	m.fileView.path = "never/touched.go"
	if got := plainRender(t, m.renderFileViewDiff(78)); !strings.Contains(got, "No recorded edits") {
		t.Errorf("untouched file should show the placeholder, got:\n%s", got)
	}
}

// TestFileViewFullBody: full mode shows the on-disk content with line numbers
// and marks session-added lines with the gutter marker; a missing file degrades
// to a readable error line.
func TestFileViewFullBody(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("let a = 1\nlet untouched = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := filesPanelTestModel()
	m.cwd = dir
	m.transcript = append(m.transcript, transcriptRow{
		kind: rowToolResult, tool: "write_file", id: "w9", status: tools.StatusOK,
		detail:       "+let a = 1",
		changedFiles: []string{"app.js"},
	})
	m = m.openFileView("app.js")
	m = m.setFileViewMode(fileViewFull)

	body := m.renderFileViewFull(78)
	plain := plainRender(t, body)
	if !strings.Contains(plain, "1 ") || !strings.Contains(plain, "let untouched = 0") {
		t.Fatalf("full view should show numbered file content:\n%s", plain)
	}
	lines := strings.Split(plain, "\n")
	if len(lines) != 2 {
		t.Fatalf("one rendered line per file line, got %d:\n%s", len(lines), plain)
	}
	if !strings.Contains(lines[0], "▎") {
		t.Errorf("session-added line should carry the gutter marker: %q", lines[0])
	}
	if strings.Contains(lines[1], "▎") {
		t.Errorf("untouched line must not carry the marker: %q", lines[1])
	}

	m.fileView.path = "gone.js"
	if got := plainRender(t, m.renderFileViewFull(78)); !strings.Contains(got, "Could not read file") {
		t.Errorf("missing file should degrade to an error line, got:\n%s", got)
	}
}

// TestFileViewSwapsTranscriptBody: while active, transcriptBodyItems returns
// the file body (a single block) instead of the chat rows, and the pinned
// title bar swaps to the one-line nav bar — the geometry every frame consumer
// relies on.
func TestFileViewSwapsTranscriptBody(t *testing.T) {
	m := filesPanelTestModel()
	m = m.openFileView("internal/tui/sidebar.go")

	items := m.transcriptBodyItems(m.chatColumnWidth(), "")
	if len(items) != 1 {
		t.Fatalf("file view should swap the body to a single block item, got %d items", len(items))
	}
	nav := plainRender(t, m.pinnedTitleBar(m.chatColumnWidth()))
	if !strings.Contains(nav, "sidebar.go") || !strings.Contains(nav, "esc back") {
		t.Fatalf("nav bar should show the path and key hints: %q", nav)
	}
	if lines := len(viewLines(m.fileViewNavBar(m.chatColumnWidth()))); lines != 1 {
		t.Fatalf("nav bar must be exactly one line (title-bar geometry), got %d", lines)
	}

	// The whole view renders without panicking in both modes and shows the nav.
	if view := plainRender(t, m.transcriptView()); !strings.Contains(view, "esc back") {
		t.Fatal("transcript view should carry the file nav bar")
	}
}

// TestSubchatEntryClosesFileView: drilling into an AGENTS row while a file view
// is open closes the file view first (the subchat owns the single-column view).
func TestSubchatEntryClosesFileView(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	m := filesPanelTestModel()
	m.sessionStore = store
	m.swarmSessionMap = map[string]string{"subagent-1": "sess-1"}
	m.transcript = append(m.transcript,
		transcriptRow{kind: rowToolCall, tool: "swarm_spawn", detail: "build it", runID: 1},
		transcriptRow{kind: rowToolResult, tool: "swarm_spawn", detail: "Spawned subagent as task subagent-1 on team default.", runID: 1},
	)
	m.activeRunID = 1
	m = m.openFileView("web/app.js")

	width := sidebarWidth(m.width)
	agents := m.sidebarAgentSelectables(width)
	if len(agents) == 0 {
		t.Fatal("expected a clickable agent row")
	}
	click := testMouseClick(tea.MouseLeft, m.chatColumnWidth()+3, agents[0].lineOffset)
	updated, _, handled := m.handleTranscriptSelectionMouse(click)
	if !handled {
		t.Fatal("agent row click should be handled")
	}
	if updated.fileView.active {
		t.Fatal("entering the subchat should close the file view")
	}
	if !updated.subchat.active {
		t.Fatal("subchat should be active")
	}
}

// TestChangedFilesRehydration: a persisted tool-result payload's changedFiles
// restores onto the rehydrated transcript row, so the FILES panel survives
// /resume.
func TestChangedFilesRehydration(t *testing.T) {
	events := []sessions.Event{{
		Type:    sessions.EventToolResult,
		Payload: json.RawMessage(`{"toolCallId":"t1","name":"edit_file","status":"ok","output":"+x","changedFiles":["pkg/a.go","pkg/b.go"]}`),
	}}
	rows := transcriptRowsFromSessionEvents(events)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0].changedFiles
	if len(got) != 2 || got[0] != "pkg/a.go" || got[1] != "pkg/b.go" {
		t.Fatalf("changedFiles not rehydrated: %v", got)
	}
}
