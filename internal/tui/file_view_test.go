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

func TestReadFileViewBounded_SourceByteBudgetCountsDelimiters(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		maxLines      int
		maxLineBytes  int
		maxTotalBytes int
		wantLines     []string
		wantTrunc     bool
		wantOmitted   bool
		wantTrailer   string
	}{
		{
			name:          "empty file",
			content:       "",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 1,
			wantLines:     nil,
		},
		{
			name:          "budget 1 two LF empty lines",
			content:       "\n\n",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 1,
			wantLines:     []string{""},
			wantTrunc:     true,
			wantOmitted:   true,
			wantTrailer:   "more lines",
		},
		{
			name:          "single LF exact budget",
			content:       "\n",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 1,
			wantLines:     []string{""},
		},
		{
			name:          "LF content line exact budget",
			content:       "ab\n",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 3,
			wantLines:     []string{"ab"},
		},
		{
			name:          "LF second line one byte over",
			content:       "ab\ncd\n",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 3,
			wantLines:     []string{"ab"},
			wantTrunc:     true,
			wantOmitted:   true,
			wantTrailer:   "more lines",
		},
		{
			name:          "CRLF two empty lines budget 2",
			content:       "\r\n\r\n",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 2,
			wantLines:     []string{""},
			wantTrunc:     true,
			wantOmitted:   true,
			wantTrailer:   "more lines",
		},
		{
			name:          "CRLF exact budget",
			content:       "ab\r\n",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 4,
			wantLines:     []string{"ab"},
		},
		{
			name:          "CRLF one byte over",
			content:       "ab\r\ncd",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 3,
			wantLines:     []string{"ab"},
			wantTrunc:     true,
			wantOmitted:   true,
			wantTrailer:   "more lines",
		},
		{
			name:          "exact budget unterminated last line",
			content:       "abc",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 3,
			wantLines:     []string{"abc"},
		},
		{
			name:          "unterminated last line one byte over",
			content:       "abcd",
			maxLines:      4000,
			maxLineBytes:  4096,
			maxTotalBytes: 3,
			wantLines:     []string{"abc"},
			wantTrunc:     true,
			wantOmitted:   true,
			wantTrailer:   "more lines",
		},
		{
			name:          "overlong physical line clipped then next line shown",
			content:       strings.Repeat("x", 20) + "\nnext\n",
			maxLines:      4000,
			maxLineBytes:  8,
			maxTotalBytes: 100,
			wantLines:     []string{strings.Repeat("x", 8), "next"},
			wantTrunc:     true,
			wantTrailer:   "line content truncated",
		},
		{
			name:          "overlong physical line newline counts against byte budget",
			content:       strings.Repeat("x", 10) + "\ny\n",
			maxLines:      4000,
			maxLineBytes:  4,
			maxTotalBytes: 11,
			wantLines:     []string{strings.Repeat("x", 4)},
			wantTrunc:     true,
			wantOmitted:   true,
			wantTrailer:   "more lines",
		},
		{
			name:          "line count cap with leftover",
			content:       "a\nb\nc\nd\n",
			maxLines:      2,
			maxLineBytes:  4096,
			maxTotalBytes: 100,
			wantLines:     []string{"a", "b"},
			wantTrunc:     true,
			wantOmitted:   true,
			wantTrailer:   "more lines",
		},
		{
			name:          "line count cap exact file",
			content:       "a\nb\n",
			maxLines:      2,
			maxLineBytes:  4096,
			maxTotalBytes: 100,
			wantLines:     []string{"a", "b"},
		},
		{
			name:          "byte budget binds before line count cap on empty LF lines",
			content:       "\n\n\n\n",
			maxLines:      10,
			maxLineBytes:  4096,
			maxTotalBytes: 2,
			wantLines:     []string{"", ""},
			wantTrunc:     true,
			wantOmitted:   true,
			wantTrailer:   "more lines",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.txt")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}

			res := readFileViewBounded(path, tc.maxLines, tc.maxLineBytes, tc.maxTotalBytes)
			if res.err != nil {
				t.Fatalf("unexpected read error: %v", res.err)
			}
			if res.truncated != tc.wantTrunc {
				t.Fatalf("truncated=%v, want %v (lines=%q)", res.truncated, tc.wantTrunc, res.lines)
			}
			if res.omittedLines != tc.wantOmitted {
				t.Fatalf("omittedLines=%v, want %v (lines=%q)", res.omittedLines, tc.wantOmitted, res.lines)
			}
			if len(res.lines) != len(tc.wantLines) {
				t.Fatalf("retained %d lines %q, want %d %q", len(res.lines), res.lines, len(tc.wantLines), tc.wantLines)
			}
			for i := range tc.wantLines {
				if res.lines[i] != tc.wantLines[i] {
					t.Fatalf("line %d = %q, want %q", i, res.lines[i], tc.wantLines[i])
				}
			}

			plain := plainRender(t, formatFileViewLines(res.lines, res.lines, nil, res.truncated, res.omittedLines, 80, zeroTheme))
			if tc.wantTrailer == "" {
				if strings.Contains(plain, "truncated") {
					t.Fatalf("expected no trailer, got %q", plain)
				}
			} else if !strings.Contains(plain, tc.wantTrailer) {
				t.Fatalf("expected trailer %q in %q", tc.wantTrailer, plain)
			}
		})
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

// TestFileViewLifecycle_OpenToLoad tests the full model update flow from open to async load completion.
func TestFileViewLifecycle_OpenToLoad(t *testing.T) {
	resetFileViewCacheForTest()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.go")
	if err := os.WriteFile(filePath, []byte("package app\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir

	// Open file via model action
	m, cmd := m.openFileView("app.go")
	if !m.fileView.active || m.fileView.mode != fileViewFull {
		t.Fatal("file view should be active in full mode for new file")
	}
	if cmd == nil {
		t.Fatal("expected async load command on open")
	}

	// View displays loading placeholder before completion
	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), fileViewLoadingPlaceholder) {
		t.Fatalf("expected loading placeholder, got: %s", plainRender(t, m.renderFileViewFull(80)))
	}

	// Process load completion
	updated, _ := m.Update(cmd())
	m = updated.(model)

	rendered := plainRender(t, m.renderFileViewFull(80))
	if !strings.Contains(rendered, "package app") || !strings.Contains(rendered, "func Run()") {
		t.Fatalf("expected loaded content, got: %s", rendered)
	}
	if m.fileView.hasError {
		t.Fatal("expected no error")
	}
}

// TestFileViewLifecycle_RapidResizeCoalesced verifies that repeated resize events
// do not cause race conditions or synchronous render spikes, and the latest resize wins.
func TestFileViewLifecycle_RapidResizeCoalesced(t *testing.T) {
	resetFileViewCacheForTest()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "resize.go")
	if err := os.WriteFile(filePath, []byte("package resize\nconst BigWidth = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "resize.go")

	var cmds []tea.Cmd
	for w := 40; w <= 120; w += 10 {
		var cmd tea.Cmd
		m, cmd = m.startFileViewLoadCmd(w)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	// Deliver the latest resize command completion
	lastCmd := cmds[len(cmds)-1]
	updated, _ := m.Update(lastCmd())
	m = updated.(model)

	if m.fileView.loadedWidth != 120 {
		t.Fatalf("expected loadedWidth 120, got %d", m.fileView.loadedWidth)
	}
	if !strings.Contains(plainRender(t, m.renderFileViewFull(120)), "package resize") {
		t.Fatalf("expected content for width 120, got: %s", plainRender(t, m.renderFileViewFull(120)))
	}
}

// TestFileViewLifecycle_ThemeSwitchReloadsActiveView tests that selecting a theme
// in production immediately triggers a reload command for the active file view.
func TestFileViewLifecycle_ThemeSwitchReloadsActiveView(t *testing.T) {
	defer applyTheme(themeDark, true)
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "theme_active.go")
	if err := os.WriteFile(filePath, []byte("package theme\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "theme_active.go")

	// Trigger /theme light via command handling
	cmdAction := parsedCommand{kind: commandTheme, text: "light"}
	updated, reloadCmd := m.dispatchCommand(cmdAction)
	m = updated.(model)

	if reloadCmd == nil {
		t.Fatal("expected reload command on active file view after theme change")
	}

	// Complete the reload
	updated, _ = m.Update(reloadCmd())
	m = updated.(model)

	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), "package theme") {
		t.Fatalf("expected reloaded theme content, got: %s", plainRender(t, m.renderFileViewFull(80)))
	}
	if m.fileView.loadedGen != defaultFileViewCache.generation() {
		t.Fatalf("expected loadedGen %d, got %d", defaultFileViewCache.generation(), m.fileView.loadedGen)
	}
}

// TestFileViewLifecycle_DirectToolMutationTriggersRefresh tests that tool result
// rows from write_file or edit_file directly refresh an active file view snapshot.
func TestFileViewLifecycle_DirectToolMutationTriggersRefresh(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "live_edit.go")
	if err := os.WriteFile(filePath, []byte("version 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "live_edit.go")

	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), "version 1") {
		t.Fatal("initial load should have version 1")
	}

	// Directly modify file on disk
	if err := os.WriteFile(filePath, []byte("version 2 modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dispatch tool result for write_file affecting live_edit.go
	toolRow := transcriptRow{
		kind:         rowToolResult,
		tool:         "write_file",
		changedFiles: []string{"live_edit.go"},
		detail:       "+version 2 modified",
	}
	updated, reloadCmd := m.Update(agentRowMsg{runID: m.activeRunID, row: toolRow})
	m = updated.(model)

	if reloadCmd == nil {
		t.Fatal("expected reload command on direct tool mutation for active file")
	}

	// Execute reload
	updated, _ = m.Update(reloadCmd())
	m = updated.(model)

	rendered := plainRender(t, m.renderFileViewFull(80))
	if !strings.Contains(rendered, "version 2 modified") {
		t.Fatalf("expected version 2 after tool mutation reload, got: %s", rendered)
	}
}

// TestFileViewLifecycle_DeletionOverrulesStaleCache tests that when a file is deleted,
// the reload failure evicts the former cache entry and immediately displays the error.
func TestFileViewLifecycle_DeletionOverrulesStaleCache(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "deleted.go")
	if err := os.WriteFile(filePath, []byte("package deleted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "deleted.go")

	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), "package deleted") {
		t.Fatal("initial load failed")
	}

	// Delete the file
	if err := os.Remove(filePath); err != nil {
		t.Fatal(err)
	}

	// Force reload
	m, reloadCmd := m.startFileViewLoadCmd(80)
	if reloadCmd == nil {
		t.Fatal("expected reload command")
	}

	updated, _ := m.Update(reloadCmd())
	m = updated.(model)

	if !m.fileView.hasError {
		t.Fatal("expected hasError to be true after deletion")
	}

	rendered := plainRender(t, m.renderFileViewFull(80))
	if strings.Contains(rendered, "package deleted") {
		t.Fatalf("stale cache must not be shown after deletion, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Could not read file") {
		t.Fatalf("expected error message in view, got: %s", rendered)
	}
}

// TestFileViewLifecycle_LateCompletionAcrossReopenDiscarded tests that if a file view
// is exited and the same path is reopened, any late-arriving completion from the first
// session is discarded and cannot populate the new session.
func TestFileViewLifecycle_LateCompletionAcrossReopenDiscarded(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "reopen.go")
	if err := os.WriteFile(filePath, []byte("original content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir

	// Session 1: open and get command
	m, cmd1 := m.openFileView("reopen.go")
	if cmd1 == nil {
		t.Fatal("expected cmd1")
	}

	// Exit session 1
	m = m.exitFileView()
	if m.fileView.active {
		t.Fatal("view should be inactive")
	}

	// Modify file on disk before session 2
	if err := os.WriteFile(filePath, []byte("new session content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session 2: reopen same path
	m, cmd2 := m.openFileView("reopen.go")
	if cmd2 == nil {
		t.Fatal("expected cmd2")
	}

	// Late completion from session 1 arrives
	msg1 := cmd1()
	updated, _ := m.Update(msg1)
	m = updated.(model)

	// Session 1 message must have been discarded: view is still waiting on session 2
	if m.fileView.renderedContent != "" {
		t.Fatalf("late completion from session 1 must be discarded, got: %s", m.fileView.renderedContent)
	}

	// Session 2 completion arrives
	msg2 := cmd2()
	updated, _ = m.Update(msg2)
	m = updated.(model)

	rendered := plainRender(t, m.renderFileViewFull(80))
	if !strings.Contains(rendered, "new session content") {
		t.Fatalf("expected new session content, got: %s", rendered)
	}
}

// TestFileViewLifecycle_ReverseOrderResizeCompletions verifies that when two resize events
// schedule requests A (earlier) and B (later), and B completes before A, the subsequent
// late arrival of A is discarded and does not overwrite B's width or rendered snapshot.
func TestFileViewLifecycle_ReverseOrderResizeCompletions(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "resize_order.go")
	if err := os.WriteFile(filePath, []byte("package resize_order\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "resize_order.go")

	// Schedule Request A for width 60
	m, cmdA := m.startFileViewLoadCmd(60)
	if cmdA == nil {
		t.Fatal("expected cmdA")
	}

	// Schedule Request B for width 100
	m, cmdB := m.startFileViewLoadCmd(100)
	if cmdB == nil {
		t.Fatal("expected cmdB")
	}

	// Message B completes first
	msgB := cmdB()
	updated, _ := m.Update(msgB)
	m = updated.(model)

	if m.fileView.loadedWidth != 100 {
		t.Fatalf("expected loadedWidth 100 after B completes, got %d", m.fileView.loadedWidth)
	}
	if !strings.Contains(plainRender(t, m.renderFileViewFull(100)), "package resize_order") {
		t.Fatalf("expected width 100 content rendered, got: %s", plainRender(t, m.renderFileViewFull(100)))
	}

	// Message A arrives late (reverse-order)
	msgA := cmdA()
	updated, _ = m.Update(msgA)
	m = updated.(model)

	// State MUST remain B (width 100), not overwritten by A (width 60)
	if m.fileView.loadedWidth != 100 {
		t.Fatalf("late completion A must NOT overwrite loadedWidth, got %d (want 100)", m.fileView.loadedWidth)
	}
	if !strings.Contains(plainRender(t, m.renderFileViewFull(100)), "package resize_order") {
		t.Fatalf("expected width 100 content still visible, got: %s", plainRender(t, m.renderFileViewFull(100)))
	}
}

// TestFileViewLifecycle_ReverseOrderToolMutationCompletions verifies that when two tool
// mutations trigger requests A (version 1) and B (version 2), and B completes before A,
// the subsequent arrival of A cannot revert the visible snapshot back to version 1.
func TestFileViewLifecycle_ReverseOrderToolMutationCompletions(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "tool_order.go")
	if err := os.WriteFile(filePath, []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "tool_order.go")

	// Mutation A modifies file to v1
	if err := os.WriteFile(filePath, []byte("version 1 state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rowA := transcriptRow{
		kind:         rowToolResult,
		tool:         "write_file",
		changedFiles: []string{"tool_order.go"},
		detail:       "+version 1 state",
	}
	updated, cmdA := m.Update(agentRowMsg{runID: m.activeRunID, row: rowA})
	m = updated.(model)
	if cmdA == nil {
		t.Fatal("expected cmdA for mutation A")
	}

	// Mutation B immediately modifies file to v2 before A completes
	if err := os.WriteFile(filePath, []byte("version 2 state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rowB := transcriptRow{
		kind:         rowToolResult,
		tool:         "edit_file",
		changedFiles: []string{"tool_order.go"},
		detail:       "+version 2 state",
	}
	updated, cmdB := m.Update(agentRowMsg{runID: m.activeRunID, row: rowB})
	m = updated.(model)
	if cmdB == nil {
		t.Fatal("expected cmdB for mutation B")
	}

	// B completes first
	msgB := cmdB()
	updated, _ = m.Update(msgB)
	m = updated.(model)

	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), "version 2 state") {
		t.Fatalf("expected version 2 state after B completes, got: %s", plainRender(t, m.renderFileViewFull(80)))
	}

	// A arrives late (reverse-order)
	msgA := cmdA()
	updated, _ = m.Update(msgA)
	m = updated.(model)

	// View MUST remain version 2, never reverted by A
	rendered := plainRender(t, m.renderFileViewFull(80))
	if strings.Contains(rendered, "version 1 state") {
		t.Fatalf("stale version 1 completion must NOT overwrite version 2, got: %s", rendered)
	}
	if !strings.Contains(rendered, "version 2 state") {
		t.Fatalf("expected version 2 state still visible, got: %s", rendered)
	}
}

func TestFileViewLifecycle_ResizeRoundTripDoesNotReuseStaleCache(t *testing.T) {
	resetFileViewCacheForTest()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "roundtrip.go")
	if err := os.WriteFile(filePath, []byte("package roundtrip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "roundtrip.go")

	m, cmd80 := m.startFileViewLoadCmd(80)
	if cmd80 == nil {
		t.Fatal("expected first width-80 load")
	}
	updated, _ := m.Update(cmd80())
	m = updated.(model)
	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), "package roundtrip") {
		t.Fatal("expected committed width-80 content")
	}

	m, cmd100 := m.startFileViewLoadCmd(100)
	if cmd100 == nil {
		t.Fatal("expected width-100 load")
	}
	m, cmd80b := m.startFileViewLoadCmd(80)
	if cmd80b == nil {
		t.Fatal("expected second width-80 load")
	}

	got := plainRender(t, m.renderFileViewFull(80))
	if !strings.Contains(got, fileViewLoadingPlaceholder) {
		t.Fatalf("80→100→80 must stay on loading until desiredSeq completes, got: %s", got)
	}
	if strings.Contains(got, "package roundtrip") {
		t.Fatalf("must not reuse cached width 80 from an earlier seq, got: %s", got)
	}

	updated, _ = m.Update(cmd80b())
	m = updated.(model)
	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), "package roundtrip") {
		t.Fatalf("expected content after matching seq completes, got: %s", plainRender(t, m.renderFileViewFull(80)))
	}
}

func TestFileViewLifecycle_EmptyFileIsCompletedSnapshot(t *testing.T) {
	resetFileViewCacheForTest()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "empty.go")
	got := plainRender(t, m.renderFileViewFull(80))
	if strings.Contains(got, fileViewLoadingPlaceholder) {
		t.Fatalf("empty completed snapshot must not stay on loading, got %q", got)
	}
	if !m.fileView.snapshotReady {
		t.Fatal("snapshotReady must be set for an empty file")
	}
}

func TestFileViewLifecycle_ShellEscapeReloadsFullView(t *testing.T) {
	resetFileViewCacheForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.go")
	if err := os.WriteFile(path, []byte("package old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := filesPanelTestModel()
	m.cwd = dir
	m = testOpenFile(m, "shell.go")
	if err := os.WriteFile(path, []byte("package new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, cmd := m.Update(bashResultMsg{output: "ok"})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("bashResultMsg must schedule a file-view reload")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	got := plainRender(t, m.renderFileViewFull(80))
	if !strings.Contains(got, "package new") {
		t.Fatalf("expected reloaded content after shell escape, got %s", got)
	}
}

func TestFileViewLifecycle_SupersededResizeSkipsWork(t *testing.T) {
	resetFileViewCacheForTest()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "coalesce.go"), []byte("package coalesce\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := filesPanelTestModel()
	m.cwd = dir
	var cmd0 tea.Cmd
	m, cmd0 = m.openFileView("coalesce.go")
	if cmd0 == nil {
		t.Fatal("expected initial load")
	}
	var cmd1, cmd2 tea.Cmd
	m, cmd1 = m.startFileViewLoadCmd(60)
	m, cmd2 = m.startFileViewLoadCmd(100)
	_ = cmd0()
	_ = cmd1()
	msg := cmd2()
	updated, _ := m.Update(msg)
	m = updated.(model)
	stats := fileViewCacheStatsForTest()
	if stats.DiskReads != 1 {
		t.Fatalf("superseded loads must not re-read, DiskReads=%d", stats.DiskReads)
	}
	if !strings.Contains(plainRender(t, m.renderFileViewFull(100)), "package coalesce") {
		t.Fatal("latest width must still load")
	}
}

func TestFilesPanelSecondActivationOpensFullViewThroughUpdate(t *testing.T) {
	resetFileViewCacheForTest()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "app.js"), []byte("let a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := filesPanelTestModel()
	m.cwd = dir
	m.runDetailsOpen = true
	m, cmd := m.selectFile("web/app.js")
	if cmd != nil {
		t.Fatal("first FILES activation must only select")
	}
	if m.fileView.active {
		t.Fatal("first FILES activation must not open the file view")
	}
	if m.selectedFile != "web/app.js" {
		t.Fatalf("selectedFile = %q", m.selectedFile)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if !m.fileView.active || m.fileView.path != "web/app.js" {
		t.Fatal("Enter on the selected FILES row must call openFileView")
	}
	if m.runDetailsOpen {
		t.Fatal("drilling in must close run details")
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = updated.(model)
	if m.fileView.mode != fileViewFull {
		t.Fatal("f must switch to full mode")
	}
	if cmd == nil {
		t.Fatal("full mode must schedule an async load")
	}
	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), fileViewLoadingPlaceholder) {
		t.Fatal("expected loading before async result")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if !strings.Contains(plainRender(t, m.renderFileViewFull(80)), "let a = 1") {
		t.Fatalf("expected loaded file content, got %s", plainRender(t, m.renderFileViewFull(80)))
	}
}
