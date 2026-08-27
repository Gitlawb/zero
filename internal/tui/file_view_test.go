package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

func testOpenFile(m model, path string) model {
	next, cmd := m.openFileView(path)
	if cmd != nil {
		msg := cmd()
		updated, _ := next.Update(msg)
		return updated.(model)
	}
	return next
}

func testSetMode(m model, mode int) model {
	next, cmd := m.setFileViewMode(mode)
	if cmd != nil {
		msg := cmd()
		updated, _ := next.Update(msg)
		return updated.(model)
	}
	return next
}

// TestFileViewOpenExitRestoresScroll: opening saves the chat scroll position,
// resets it for the file body, and Esc restores it; switching files while open
// keeps the ORIGINAL saved position (not the file view's own).
func TestFileViewOpenExitRestoresScroll(t *testing.T) {
	m := filesPanelTestModel()
	m.chatScrollOffset = 12

	m, _ = m.openFileView("web/app.js")
	if !m.fileView.active || m.fileView.mode != fileViewDiff {
		t.Fatalf("open should activate in diff mode: %+v", m.fileView)
	}
	if m.chatScrollOffset != 0 || m.fileView.parentScrollOffset != 12 {
		t.Fatalf("open should reset scroll and save the parent offset: offset=%d saved=%d", m.chatScrollOffset, m.fileView.parentScrollOffset)
	}

	m.chatScrollOffset = 5 // scrolled within the file body
	m, _ = m.openFileView("internal/tui/sidebar.go")
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
	m, _ = m.openFileView("web/app.js")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = updated.(model)
	if cmd != nil {
		updated, _ = m.Update(cmd())
		m = updated.(model)
	}
	if m.fileView.mode != fileViewFull {
		t.Fatal("f should switch to full mode")
	}
	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(model)
	if cmd != nil {
		updated, _ = m.Update(cmd())
		m = updated.(model)
	}
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
	m, _ = m.openFileView("internal/tui/sidebar.go")
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
	m = testOpenFile(m, "app.js")
	m = testSetMode(m, fileViewFull)

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
	m, cmd := m.startFileViewLoadCmd(78)
	if cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(model)
	}
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
	m, _ = m.openFileView("internal/tui/sidebar.go")

	items := m.transcriptBodyItems(m.chatColumnWidth(), "", false)
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

// TestSidebarAgentClickIsIgnoredWithoutRail verifies invisible legacy hit
// coordinates cannot unexpectedly replace the full-width conversation view.
func TestSidebarAgentClickIsIgnoredWithoutRail(t *testing.T) {
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
	m, _ = m.openFileView("web/app.js")

	width := sidebarWidth(m.width)
	agents := m.sidebarAgentSelectables(width)
	if len(agents) == 0 {
		t.Fatal("expected a clickable agent row")
	}
	click := testMouseClick(tea.MouseLeft, m.chatColumnWidth()+3, agents[0].lineOffset)
	updated, _, handled := m.handleTranscriptSelectionMouse(click)
	if handled {
		t.Fatal("invisible rail coordinates must not handle clicks")
	}
	if !updated.fileView.active {
		t.Fatal("an ignored rail click must leave the active file view alone")
	}
	if updated.subchat.active {
		t.Fatal("an ignored rail click must not enter a subchat")
	}
}

// TestChangedFilesRehydration: a persisted tool-result payload's changedFiles
// restores onto the rehydrated transcript row, so the FILES panel survives
// /resume.
func TestChangedFilesRehydration(t *testing.T) {
	events := []sessions.Event{{
		Type:    sessions.EventToolResult,
		Payload: json.RawMessage(`{"toolCallId":"t1","name":"edit_file","status":"ok","output":"+x","changedFiles":["pkg/a.go","pkg/b.go"],"changeSummaries":[{"path":"node_modules/","kind":"created","aggregated":true}]}`),
	}}
	rows := transcriptRowsFromSessionEvents(events)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0].changedFiles
	if len(got) != 2 || got[0] != "pkg/a.go" || got[1] != "pkg/b.go" {
		t.Fatalf("changedFiles not rehydrated: %v", got)
	}
	if summaries := rows[0].changeSummaries; len(summaries) != 1 || summaries[0].Path != "node_modules/" || !summaries[0].Aggregated {
		t.Fatalf("changeSummaries not rehydrated: %#v", summaries)
	}
}

// TestResumedFileEditUsesPersistedDisplayPreview keeps the reviewable diff a
// user saw while the run was live. The provider-facing output is intentionally
// a short confirmation, so it cannot substitute for the card-only preview.
func TestResumedFileEditUsesPersistedDisplayPreview(t *testing.T) {
	preview := "--- a/calculator.go\n+++ b/calculator.go\n@@ -4,1 +4,1 @@\n-oldValue := 1\n+newValue := 2"
	events := []sessions.Event{{
		Type:    sessions.EventToolResult,
		Payload: json.RawMessage(`{"toolCallId":"edit-1","name":"edit_file","status":"ok","output":"Successfully edited calculator.go (replaced 1 occurrence).","displayPreview":` + strconv.Quote(preview) + `}`),
	}}

	rows := transcriptRowsFromSessionEvents(events)
	if len(rows) != 1 {
		t.Fatalf("expected one restored row, got %d", len(rows))
	}
	if rows[0].detail != preview {
		t.Fatalf("resume should restore the saved diff preview, got %q", rows[0].detail)
	}
	card := renderToolResultCard(rows[0], 80, rowContext{}, cardRenderOptions{})
	if plain := plainRender(t, card); !strings.Contains(plain, "newValue := 2") {
		t.Fatalf("resumed edit should render its diff preview, got %q", plain)
	}
}

// TestOpenFileViewSamePathIsNoOp: re-clicking the FILES row of the file already
// being viewed must not clobber the user's mode or scroll — the old
// unconditional openFileView bounced full mode back to diff and reset scroll.
func TestOpenFileViewSamePathIsNoOp(t *testing.T) {
	m := filesPanelTestModel()
	m = testOpenFile(m, "web/app.js")
	m = testSetMode(m, fileViewFull)
	m.chatScrollOffset = 7 // scrolled within the file body

	m, _ = m.openFileView("web/app.js")
	if m.fileView.mode != fileViewFull {
		t.Fatal("re-opening the same file must keep full mode")
	}
	if m.chatScrollOffset != 7 {
		t.Fatalf("re-opening the same file must keep the scroll, got %d", m.chatScrollOffset)
	}
	// A DIFFERENT file still switches (and resets to diff mode as documented).
	m, _ = m.openFileView("internal/tui/sidebar.go")
	if m.fileView.path != "internal/tui/sidebar.go" || m.fileView.mode != fileViewDiff {
		t.Fatalf("opening another file should switch views: %+v", m.fileView)
	}
}

// TestFileViewFullBodyTruncatesLongFile: the full view stops reading at
// fileViewMaxLines (bounded read — a giant file must not be loaded wholesale)
// and appends the truncation trailer.
func TestFileViewFullBodyTruncatesLongFile(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < fileViewMaxLines+50; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	m := filesPanelTestModel()
	m.cwd = dir
	m.gitTouched = []gitSweepFile{{path: "big.txt"}}
	m = testOpenFile(m, "big.txt")

	plain := plainRender(t, m.renderFileViewFull(80))
	lines := strings.Split(plain, "\n")
	if len(lines) != fileViewMaxLines+1 { // capped content + the trailer line
		t.Fatalf("rendered %d lines, want %d content lines + 1 trailer", len(lines), fileViewMaxLines)
	}
	if !strings.Contains(lines[len(lines)-1], "truncated") {
		t.Fatalf("expected the truncation trailer, got %q", lines[len(lines)-1])
	}
}

// TestDetailedTranscriptClosesFileView: entering detailed transcript mode
// closes an active file drill-in so the full transcript body replaces the file
// content.
func TestDetailedTranscriptClosesFileView(t *testing.T) {
	m := filesPanelTestModel()
	m.altScreen = true
	m, _ = m.openFileView("web/app.js")

	if !m.fileView.active {
		t.Fatal("sanity check: openFileView should activate the file view")
	}

	updated, _ := m.Update(testKeyCtrl('o'))
	m = updated.(model)

	if m.fileView.active {
		t.Fatal("detailed transcript should close the file drill-in")
	}
	if !m.transcriptDetailed {
		t.Fatal("Ctrl+O should enter detailed mode")
	}

	items := m.transcriptBodyItems(m.chatColumnWidth(), "", true)
	if len(items) <= 1 {
		t.Fatalf("detailed transcript should show multiple body items, got %d", len(items))
	}
}

// TestDetailedTranscriptStaysClosedOnSecondToggle: toggling out and back into
// detailed mode does not re-open the closed file view.
func TestDetailedTranscriptStaysClosedOnSecondToggle(t *testing.T) {
	m := filesPanelTestModel()
	m.altScreen = true
	m, _ = m.openFileView("web/app.js")

	updated, _ := m.Update(testKeyCtrl('o'))
	m = updated.(model)

	updated, _ = m.Update(testKeyCtrl('o'))
	m = updated.(model)

	if m.fileView.active {
		t.Fatal("exiting detailed mode must not re-open the file drill-in")
	}
	if m.transcriptDetailed {
		t.Fatal("second Ctrl+O should exit detailed mode")
	}
}

// TestFileViewKeysDeferToBlockingModal: with a permission prompt up, Esc and
// the d/f shortcuts belong to the prompt — the drill-in must not swallow them
// (Esc exiting the view instead of reaching the prompt's deny handling).
func TestFileViewKeysDeferToBlockingModal(t *testing.T) {
	m := filesPanelTestModel()
	m, _ = m.openFileView("web/app.js")
	m.pendingPermission = &pendingPermissionPrompt{
		request: agent.PermissionRequest{ToolName: "write_file"},
		decide:  func(agent.PermissionDecision) {},
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = updated.(model)
	if m.fileView.mode != fileViewDiff {
		t.Fatal("f with a permission prompt up must not switch file-view modes")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(model)
	if !m.fileView.active {
		t.Fatal("Esc with a permission prompt up must not exit the file view")
	}
}

// TestFileViewRepeatedViewNoDiskIOOrHighlighting proves that repeated calls
// to render the full file view do not perform disk I/O or Chroma syntax
// highlighting after the initial load, and that modifying the file on disk
// properly invalidates and triggers a reload.
func TestFileViewRepeatedViewNoDiskIOOrHighlighting(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	content := "package main\n\nfunc main() {\n\tprintln(\"hello world\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "sample.go")
	m = testSetMode(m, fileViewFull)

	// First render: Misses cache during testSetMode cmd execution, performed 1 disk read and 1 highlight call
	firstRender := m.renderFileViewFull(80)
	if !strings.Contains(firstRender, "hello world") {
		t.Fatalf("first render missing content: %s", firstRender)
	}

	statsAfterFirst := fileViewCacheStatsForTest()
	if statsAfterFirst.DiskReads != 1 {
		t.Fatalf("expected 1 disk read on initial view, got %d", statsAfterFirst.DiskReads)
	}
	if statsAfterFirst.HighlightCalls != 1 {
		t.Fatalf("expected 1 highlight call on initial view, got %d", statsAfterFirst.HighlightCalls)
	}

	// Repeated renders (e.g. 10 frames during typing/scrolling/resize)
	for i := 0; i < 10; i++ {
		rendered := m.renderFileViewFull(80)
		if rendered != firstRender {
			t.Fatalf("subsequent render %d mismatch", i)
		}
	}

	statsAfterRepeated := fileViewCacheStatsForTest()
	if statsAfterRepeated.DiskReads != 1 {
		t.Fatalf("repeated View calls must not trigger disk reads, got %d", statsAfterRepeated.DiskReads)
	}
	if statsAfterRepeated.HighlightCalls != 1 {
		t.Fatalf("repeated View calls must not trigger Chroma highlighting, got %d", statsAfterRepeated.HighlightCalls)
	}
	if statsAfterRepeated.CacheHits != statsAfterFirst.CacheHits+10 {
		t.Fatalf("expected 10 additional cache hits, got %d (before: %d)", statsAfterRepeated.CacheHits, statsAfterFirst.CacheHits)
	}

	// Same byte length as `content`, so only mtime can invalidate the entry.
	newContent := "package main\n\nfunc main() {\n\tprintln(\"HELLO WORLD\")\n}\n"
	if len(newContent) != len(content) {
		t.Fatalf("test setup: newContent length %d must match original length %d", len(newContent), len(content))
	}
	if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filePath, future, future); err != nil {
		t.Fatal(err)
	}

	// Re-trigger load command after file update
	m, cmd := m.startFileViewLoadCmd(80)
	if cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(model)
	}

	updatedRender := m.renderFileViewFull(80)
	if !strings.Contains(updatedRender, "HELLO WORLD") {
		t.Fatalf("expected updated content after disk mutation, got: %s", updatedRender)
	}

	statsAfterUpdate := fileViewCacheStatsForTest()
	if statsAfterUpdate.DiskReads != 2 {
		t.Fatalf("expected 2 disk reads after file change, got %d", statsAfterUpdate.DiskReads)
	}
	if statsAfterUpdate.HighlightCalls != 2 {
		t.Fatalf("expected 2 highlight calls after file change, got %d", statsAfterUpdate.HighlightCalls)
	}
}

// TestFileViewMaxBytesBudgetTruncation verifies that files exceeding the
// total byte budget (fileViewMaxBytes) are truncated and display the trailer.
func TestFileViewMaxBytesBudgetTruncation(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "giant_bytes.txt")

	// Generate a file with total size ~1.5 MB (> fileViewMaxBytes of 1 MiB)
	line := strings.Repeat("a", 500) + "\n"
	numLines := (fileViewMaxBytes / 500) + 100
	var sb strings.Builder
	for i := 0; i < numLines; i++ {
		sb.WriteString(line)
	}
	if err := os.WriteFile(filePath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "giant_bytes.txt")
	m = testSetMode(m, fileViewFull)

	body := m.renderFileViewFull(80)
	plain := plainRender(t, body)

	if !strings.Contains(plain, "truncated") {
		t.Fatalf("expected truncation trailer for file exceeding byte budget, got:\n%s", plain)
	}

	renderedLines := strings.Split(plain, "\n")
	if len(renderedLines) >= numLines {
		t.Fatalf("rendered line count %d should be strictly less than total file lines %d", len(renderedLines), numLines)
	}
}

// TestFileViewMaxLineBytesBudgetTruncation verifies that single overlong lines
// exceeding fileViewMaxLineBytes are clamped without crashing or unbounded memory.
func TestFileViewMaxLineBytesBudgetTruncation(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "giant_line.js")

	// Generate a single overlong line of 50,000 bytes (> fileViewMaxLineBytes of 4096)
	giantLine := "let data = \"" + strings.Repeat("x", 50000) + "\";\nlet next = 1;\n"
	if err := os.WriteFile(filePath, []byte(giantLine), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "giant_line.js")
	m = testSetMode(m, fileViewFull)

	body := m.renderFileViewFull(80)
	plain := plainRender(t, body)

	if !strings.Contains(plain, "let next = 1") {
		t.Fatalf("expected next line to be readable after overlong line truncation, got:\n%s", plain)
	}
	if !strings.Contains(plain, "truncated") {
		t.Fatalf("expected truncation trailer for overlong line, got:\n%s", plain)
	}
}

// TestFileViewCacheEviction verifies that the LRU cache caps entry count to
// defaultFileViewCacheMaxEntries.
func TestFileViewCacheEviction(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	numFiles := defaultFileViewCacheMaxEntries + 10
	for i := 0; i < numFiles; i++ {
		fname := fmt.Sprintf("file_%d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, fname), []byte(fmt.Sprintf("content %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := filesPanelTestModel()
	m.cwd = dir
	for i := 0; i < numFiles; i++ {
		fname := fmt.Sprintf("file_%d.txt", i)
		m = testOpenFile(m, fname)
		m = testSetMode(m, fileViewFull)
		_ = m.renderFileViewFull(80)
	}

	defaultFileViewCache.mu.Lock()
	cachedCount := len(defaultFileViewCache.items)
	defaultFileViewCache.mu.Unlock()

	if cachedCount > defaultFileViewCacheMaxEntries {
		t.Fatalf("cache size %d exceeded maxEntries %d", cachedCount, defaultFileViewCacheMaxEntries)
	}
}

// TestFileViewClearOnThemeChange verifies that switching themes clears the
// file view cache so updated palette styles are applied.
func TestFileViewClearOnThemeChange(t *testing.T) {
	defer applyTheme(themeDark, true)
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	if err := os.WriteFile(filePath, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "code.go")
	m = testSetMode(m, fileViewFull)
	_ = m.renderFileViewFull(80)

	defaultFileViewCache.mu.Lock()
	entriesBefore := len(defaultFileViewCache.items)
	defaultFileViewCache.mu.Unlock()

	if entriesBefore == 0 {
		t.Fatal("expected cached entries before theme switch")
	}

	applyTheme(themeLight, false)

	defaultFileViewCache.mu.Lock()
	entriesAfter := len(defaultFileViewCache.items)
	defaultFileViewCache.mu.Unlock()

	if entriesAfter != 0 {
		t.Fatalf("expected cache to be cleared after theme change, got %d entries", entriesAfter)
	}
}

// TestReadFileViewBounded_GiantSingleLineStopsAtBudget verifies that reading a
// multi-megabyte physical line without newlines stops immediately at maxTotalBytes
// rather than reading through to EOF or loading the entire line into memory.
func TestReadFileViewBounded_GiantSingleLineStopsAtBudget(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "giant_single_line.txt")

	// 5 MiB single line without any newlines
	totalSize := 5 * 1024 * 1024
	giantContent := strings.Repeat("A", totalSize)
	if err := os.WriteFile(filePath, []byte(giantContent), 0o644); err != nil {
		t.Fatal(err)
	}

	maxTotalBytes := 1 << 20 // 1 MiB budget
	maxLineBytes := 4096     // 4 KiB line cap
	maxLines := 4000

	res := readFileViewBounded(filePath, maxLines, maxLineBytes, maxTotalBytes)
	if res.err != nil {
		t.Fatalf("unexpected read error: %v", res.err)
	}

	if !res.truncated {
		t.Fatal("expected truncated=true when reading 5 MiB single line with 1 MiB budget")
	}

	if len(res.lines) != 1 {
		t.Fatalf("expected exactly 1 truncated line, got %d", len(res.lines))
	}

	if len(res.lines[0]) > maxLineBytes {
		t.Fatalf("retained line length %d exceeded per-line cap %d", len(res.lines[0]), maxLineBytes)
	}
}

// TestReadFileViewBounded_ExactMaxBytesNotTruncated verifies that a file exactly
// equal in size to maxTotalBytes without extra unread bytes is read completely
// without an erroneous truncation flag or trailer.
func TestReadFileViewBounded_ExactMaxBytesNotTruncated(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "exact_budget.txt")

	maxTotalBytes := 1024 * 1024 // 1 MiB
	maxLineBytes := 4096
	maxLines := 4000

	// Construct exactly 1024 * 1024 bytes with 512 lines of 2048 bytes (2047 chars + '\n')
	lineLen := 2048
	numLines := maxTotalBytes / lineLen
	remainder := maxTotalBytes % lineLen

	var sb strings.Builder
	for i := 0; i < numLines; i++ {
		sb.WriteString(strings.Repeat("B", lineLen-1) + "\n")
	}
	if remainder > 0 {
		sb.WriteString(strings.Repeat("C", remainder))
	}

	content := []byte(sb.String())
	if len(content) != maxTotalBytes {
		t.Fatalf("generated content size %d != %d", len(content), maxTotalBytes)
	}

	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	res := readFileViewBounded(filePath, maxLines, maxLineBytes, maxTotalBytes)
	if res.err != nil {
		t.Fatalf("unexpected read error: %v", res.err)
	}

	if res.truncated {
		t.Fatal("expected truncated=false for file exactly matching maxTotalBytes with no trailing data")
	}

	totalReadLen := 0
	for _, l := range res.lines {
		totalReadLen += len(l)
	}
	if totalReadLen == 0 {
		t.Fatal("expected lines to be populated")
	}
}

// TestReadFileViewBounded_ExactMaxBytesUnterminatedLineTruncated verifies that a single
// unterminated physical line of exactly maxTotalBytes is correctly marked truncated=true
// when the line length exceeds maxLineBytes.
func TestReadFileViewBounded_ExactMaxBytesUnterminatedLineTruncated(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "exact_single_line_unterminated.txt")

	maxTotalBytes := 1024 * 1024 // 1 MiB
	maxLineBytes := 4096
	maxLines := 4000

	// 1 MiB continuous single line without any newlines
	content := []byte(strings.Repeat("X", maxTotalBytes))
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	res := readFileViewBounded(filePath, maxLines, maxLineBytes, maxTotalBytes)
	if res.err != nil {
		t.Fatalf("unexpected read error: %v", res.err)
	}

	if !res.truncated {
		t.Fatal("expected truncated=true for 1 MiB single unterminated line clipped at maxLineBytes")
	}

	if len(res.lines) != 1 {
		t.Fatalf("expected exactly 1 line, got %d", len(res.lines))
	}
	if len(res.lines[0]) > maxLineBytes {
		t.Fatalf("retained line length %d exceeded maxLineBytes %d", len(res.lines[0]), maxLineBytes)
	}
}

// TestFileViewCache_RenderVariantsBoundedUnderResize verifies that varying width
// and changed-line fingerprints cannot grow an entry's renders map beyond
// fileViewMaxRenderVariants, even under concurrent access from multiple goroutines.
func TestFileViewCache_RenderVariantsBoundedUnderResize(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "resize_test.go")
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "resize_test.go")
	m = testSetMode(m, fileViewFull)

	// Execute mixed-width getOrRender calls concurrently from multiple goroutines
	var wg sync.WaitGroup
	workers := 8
	callsPerWorker := 20

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < callsPerWorker; i++ {
				width := 40 + ((workerID*callsPerWorker + i) % 50)
				changed := map[string]bool{}
				if width%2 == 0 {
					changed[fmt.Sprintf("marker_%d_%d", workerID, width)] = true
				}
				_ = defaultFileViewCache.getOrRender(filePath, "resize_test.go", width, changed)
			}
		}(w)
	}
	wg.Wait()

	defaultFileViewCache.mu.Lock()
	elem, ok := defaultFileViewCache.items[filePath]
	defaultFileViewCache.mu.Unlock()

	if !ok || elem == nil {
		t.Fatal("expected cached entry for file")
	}

	entry := elem.Value.(*fileViewCachedEntry)
	entry.rendersMu.RLock()
	variantCount := len(entry.renders)
	keyCount := len(entry.renderKeys)
	entry.rendersMu.RUnlock()

	if variantCount > fileViewMaxRenderVariants {
		t.Fatalf("variant count %d exceeded maximum limit %d", variantCount, fileViewMaxRenderVariants)
	}
	if keyCount > fileViewMaxRenderVariants {
		t.Fatalf("renderKeys count %d exceeded maximum limit %d", keyCount, fileViewMaxRenderVariants)
	}
}

// TestFileViewAsyncCacheMissLifecycle exercises the cache-miss lifecycle through
// the actual View/Update boundary:
//  1. Initial full-mode activation returns a command while View() renders the
//     loading placeholder without performing disk I/O or Chroma work.
//  2. The command executes asynchronously and returns fileViewLoadedMsg.
//  3. Update() applies the message to the model.
//  4. View() renders the loaded, formatted content.
func TestFileViewAsyncCacheMissLifecycle(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "async_sample.go")
	content := "package main\n\nfunc AsyncWork() string {\n\treturn \"done\"\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir

	// Step 1: Open file with no edit rows (opens in full mode directly with async cmd)
	m, cmd := m.openFileView("async_sample.go")
	if cmd == nil {
		t.Fatal("expected async load command on cache miss")
	}

	// View before cmd completes must render loading placeholder with 0 disk I/O or highlighting
	initialView := m.renderFileViewFull(80)
	if !strings.Contains(initialView, "Loading…") {
		t.Fatalf("expected loading placeholder before command completes, got:\n%s", initialView)
	}
	statsBefore := fileViewCacheStatsForTest()
	if statsBefore.DiskReads != 0 || statsBefore.HighlightCalls != 0 {
		t.Fatalf("View() must not stat/read/highlight directly: %+v", statsBefore)
	}

	// Step 2: Execute command asynchronously
	msg := cmd()
	loadedMsg, ok := msg.(fileViewLoadedMsg)
	if !ok {
		t.Fatalf("expected fileViewLoadedMsg, got %T", msg)
	}
	if loadedMsg.err != nil {
		t.Fatalf("unexpected load error: %v", loadedMsg.err)
	}

	// Step 3: Update model with loaded message
	updated, _ := m.Update(loadedMsg)
	m = updated.(model)

	// Step 4: View now renders the loaded content
	loadedView := m.renderFileViewFull(80)
	if !strings.Contains(loadedView, "AsyncWork") {
		t.Fatalf("expected loaded content in view, got:\n%s", loadedView)
	}
	statsAfter := fileViewCacheStatsForTest()
	if statsAfter.DiskReads != 1 || statsAfter.HighlightCalls != 1 {
		t.Fatalf("expected exactly 1 disk read and 1 highlight call, got: %+v", statsAfter)
	}

	// Also verify switching from diff mode to full mode triggers the cmd
	appFile := filepath.Join(dir, "web", "app.js")
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appFile, []byte("let webApp = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mDiff, cmdDiff := m.openFileView("web/app.js")
	if cmdDiff != nil || mDiff.fileView.mode != fileViewDiff {
		t.Fatalf("file with edit cards must open in diff mode with nil cmd: mode=%d, cmd=%v", mDiff.fileView.mode, cmdDiff)
	}
	mFull, cmdFull := mDiff.setFileViewMode(fileViewFull)
	if cmdFull == nil || mFull.fileView.mode != fileViewFull {
		t.Fatal("switching to full mode must return load command")
	}
	updated, _ = mFull.Update(cmdFull())
	mFull = updated.(model)
	if !strings.Contains(mFull.renderFileViewFull(80), "webApp") {
		t.Fatalf("expected loaded webApp content, got: %s", mFull.renderFileViewFull(80))
	}
}

// TestFileViewAsyncDiscardSupersededResult verifies that if a user switches files
// or exits the view while an async load is in flight, the completed message from
// the old file is safely discarded and does not overwrite the active view.
func TestFileViewAsyncDiscardSupersededResult(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	fileA := filepath.Join(dir, "fileA.txt")
	fileB := filepath.Join(dir, "fileB.txt")
	if err := os.WriteFile(fileA, []byte("Content of File A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("Content of File B\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir

	// Start loading File A
	m, cmdA := m.openFileView("fileA.txt")
	if cmdA == nil {
		t.Fatal("expected cmd for fileA")
	}

	// User switches to File B before cmdA returns
	m, cmdB := m.openFileView("fileB.txt")
	if cmdB == nil {
		t.Fatal("expected cmd for fileB")
	}

	// Now cmdA completes and its message is dispatched
	msgA := cmdA()
	updated, _ := m.Update(msgA)
	m = updated.(model)

	// File A's result must be discarded because active file is File B
	viewWhileB := m.renderFileViewFull(80)
	if strings.Contains(viewWhileB, "Content of File A") {
		t.Fatalf("stale File A result must not paint over File B: %s", viewWhileB)
	}

	// Now cmdB completes and is dispatched
	msgB := cmdB()
	updated, _ = m.Update(msgB)
	m = updated.(model)

	viewFinal := m.renderFileViewFull(80)
	if !strings.Contains(viewFinal, "Content of File B") {
		t.Fatalf("expected File B content, got: %s", viewFinal)
	}
}

// TestFileViewAsyncDiscardOnModeSwitchOrExit verifies that if a view exits or
// switches back to diff mode, completed async loads are safely ignored.
func TestFileViewAsyncDiscardOnModeSwitchOrExit(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	fileA := filepath.Join(dir, "discard_mode.txt")
	if err := os.WriteFile(fileA, []byte("Some content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir

	m, cmdA := m.openFileView("discard_mode.txt")
	if cmdA == nil {
		t.Fatal("expected cmd")
	}

	// Exit file view before command returns
	m = m.exitFileView()
	if m.fileView.active {
		t.Fatal("view should be inactive")
	}

	// Now deliver the message
	msgA := cmdA()
	updated, _ := m.Update(msgA)
	m = updated.(model)

	if m.fileView.active {
		t.Fatal("discarded message must not re-activate file view")
	}
}

// TestFileViewAsyncDiscardOnThemeInvalidation verifies that if a theme switch occurs
// while an async load is in flight, the old theme's completion message is discarded
// and a fresh load command for the new theme generation is triggered.
func TestFileViewAsyncDiscardOnThemeInvalidation(t *testing.T) {
	defer applyTheme(themeDark, true)
	resetFileViewCacheForTest()

	dir := t.TempDir()
	fileA := filepath.Join(dir, "theme_test.go")
	if err := os.WriteFile(fileA, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir

	m, cmd := m.openFileView("theme_test.go")
	if cmd == nil {
		t.Fatal("expected cmd")
	}

	// Invalidate cache by switching theme before cmd completes
	applyTheme(themeLight, false)

	// Now old cmd completes with stale generation
	msg := cmd()
	updated, retryCmd := m.Update(msg)
	m = updated.(model)

	// The message must have been rejected due to generation mismatch
	if m.fileView.renderedContent != "" {
		t.Fatalf("expected empty renderedContent after invalidation, got %q", m.fileView.renderedContent)
	}
	if retryCmd == nil {
		t.Fatal("expected retry command for new generation after invalidation")
	}

	// Executing the retry command loads the file under the new generation
	retryMsg := retryCmd()
	updated, _ = m.Update(retryMsg)
	m = updated.(model)

	if !strings.Contains(m.renderFileViewFull(80), "package") {
		t.Fatalf("expected file content loaded after retry, got: %s", m.renderFileViewFull(80))
	}
	if m.fileView.loadedGen != defaultFileViewCache.generation() {
		t.Fatalf("loadedGen %d != cache generation %d", m.fileView.loadedGen, defaultFileViewCache.generation())
	}
}

// TestFileViewThemeSwitchWhileLoaded verifies that switching themes invalidates
// the loaded generation and allows immediate reloading for the new palette.
func TestFileViewThemeSwitchWhileLoaded(t *testing.T) {
	defer applyTheme(themeDark, true)
	resetFileViewCacheForTest()

	dir := t.TempDir()
	fileA := filepath.Join(dir, "switch_test.go")
	if err := os.WriteFile(fileA, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir

	m = testOpenFile(m, "switch_test.go")
	if !strings.Contains(m.renderFileViewFull(80), "package") {
		t.Fatal("file should be loaded initially")
	}

	// Switch theme: generation advances, cache is cleared
	applyTheme(themeLight, false)

	// renderFileViewFull must not return the stale dark-theme content
	staleCheck := m.renderFileViewFull(80)
	if strings.Contains(staleCheck, "package") {
		t.Fatalf("stale renderedContent must not be rendered after generation increment: %s", staleCheck)
	}

	// Starting a new load command re-populates for the new theme
	m, reloadCmd := m.startFileViewLoadCmd(80)
	if reloadCmd == nil {
		t.Fatal("expected reload command")
	}
	updated, _ := m.Update(reloadCmd())
	m = updated.(model)

	if !strings.Contains(m.renderFileViewFull(80), "package") {
		t.Fatalf("expected reloaded content for new theme, got: %s", m.renderFileViewFull(80))
	}
}
