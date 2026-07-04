// file_view.go is the drill-in view for a touched file, opened from the FILES
// sidebar section (files_panel.go). It reuses the subchat pattern: while
// active, the chat column's body swaps to this file's content — the sidebar,
// composer, and scroll engine keep working unchanged (transcriptBodyItems is
// the single source the viewport, renderer, and hit-testers all read, so
// swapping there keeps every consumer consistent). Two modes:
//
//	diff (default) — the file's edit cards from this session, full-depth
//	full           — the file as it stands on disk, syntax highlighted, with
//	                 gutter markers on the lines this session's diffs added
//
// d/f switch modes (with an empty composer), Esc returns to the chat at the
// scroll position it was left at.
package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// fileViewMaxLines caps the full-file mode so a giant generated file can't
// freeze a render; the tail collapses into a "… N more lines" trailer.
const fileViewMaxLines = 4000

const (
	fileViewDiff = iota
	fileViewFull
)

// fileViewState manages the drill-in view for a touched file. When active, the
// transcript body swaps to the file's diff/content instead of the chat rows.
type fileViewState struct {
	active bool
	path   string // workspace-relative, as carried by changedFiles
	mode   int    // fileViewDiff | fileViewFull
	// parentScrollOffset preserves the chat scroll position so closing the view
	// returns to the same spot (mirrors subchatState).
	parentScrollOffset int
}

// openFileView activates the drill-in for path in diff mode. Opening from an
// already-open view (clicking another FILES row) keeps the original saved chat
// scroll position rather than saving the file view's own offset as "parent".
// Re-opening the file that is ALREADY being viewed is a no-op: a stray
// re-click must not bounce the user from full mode back to diff or reset
// their scroll position.
func (m model) openFileView(path string) model {
	if m.fileView.active && m.fileView.path == path {
		return m
	}
	if !m.fileView.active {
		m.fileView.parentScrollOffset = m.chatScrollOffset
	}
	m.fileView.active = true
	m.fileView.path = path
	m.fileView.mode = fileViewDiff
	// A file only the git sweep knows about (bash/subagent mutation) has no edit
	// cards to stack — open straight on the full file instead of a placeholder.
	if len(m.fileViewResultRows()) == 0 {
		m.fileView.mode = fileViewFull
	}
	m.chatScrollOffset = 0
	m = m.clearHover() // bodyY numbering differs between the file body and the chat
	return m
}

// exitFileView deactivates the view and restores the chat scroll position.
func (m model) exitFileView() model {
	if !m.fileView.active {
		return m
	}
	m.chatScrollOffset = m.fileView.parentScrollOffset
	m.fileView = fileViewState{}
	m = m.clearHover()
	return m
}

// setFileViewMode switches diff/full, resetting the scroll to the bottom-anchored
// start since the two bodies have unrelated heights.
func (m model) setFileViewMode(mode int) model {
	if !m.fileView.active || m.fileView.mode == mode {
		return m
	}
	m.fileView.mode = mode
	m.chatScrollOffset = 0
	return m
}

// fileViewNavBar renders the single-line header shown in place of the pinned
// title bar while the view is active: the path plus the key hints. One line
// exactly, so every scrollableTranscriptFrame computed against it agrees with
// the title bar's geometry.
func (m model) fileViewNavBar(width int) string {
	mode := "diff"
	other := "f full"
	if m.fileView.mode == fileViewFull {
		mode = "full"
		other = "d diff"
	}
	left := zeroTheme.accent.Render("← "+truncatePathLeft(m.fileView.path, maxInt(8, width/2))) +
		zeroTheme.faint.Render("  ·  "+mode)
	right := zeroTheme.faint.Render(other + " · esc back")
	return fitStyledLine(joinHeaderLine(left, right, width), width)
}

// fileViewBodyItems builds the body items the transcript machinery renders
// while the view is active — one pre-rendered block, so scrolling and height
// accounting flow through the exact same path as chat rows.
func (m model) fileViewBodyItems(width int) []transcriptBodyItem {
	var block string
	if m.fileView.mode == fileViewFull {
		block = m.renderFileViewFull(width)
	} else {
		block = m.renderFileViewDiff(width)
	}
	return []transcriptBodyItem{transcriptBlockBodyItem(transcriptBodyItemRow, -1, block)}
}

// fileViewResultRows returns the transcript's tool-result rows that touched the
// viewed file, in chronological order.
func (m model) fileViewResultRows() []transcriptRow {
	var rows []transcriptRow
	for _, row := range m.transcript {
		if row.kind != rowToolResult {
			continue
		}
		if slices.Contains(row.changedFiles, m.fileView.path) {
			rows = append(rows, row)
		}
	}
	return rows
}

// renderFileViewDiff renders the session's edits to the file as its full-depth
// tool cards (bodyCap 0, the detailed-view depth), stacked chronologically —
// the same cards the chat shows, so the diffs read identically in both places.
func (m model) renderFileViewDiff(width int) string {
	rows := m.fileViewResultRows()
	if len(rows) == 0 {
		return zeroTheme.faint.Render("No recorded edits for this file in this session.")
	}
	rc := buildRowContext(m.transcript)
	opts := cardRenderOptions{bodyCap: 0, cwd: m.cwd}
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if len(rows) > 1 {
			b.WriteString(zeroTheme.faint.Render(fmt.Sprintf("edit %d of %d", i+1, len(rows))))
			b.WriteString("\n")
		}
		b.WriteString(m.renderRowModeUncached(row, width, rc, opts))
	}
	return b.String()
}

// renderFileViewFull renders the file as it currently stands on disk, syntax
// highlighted, with a line-number gutter and an accent ▎ marker on the lines
// this session's diffs added (matched by exact text — an approximation that
// tolerates later drift; a stale marker just doesn't highlight).
func (m model) renderFileViewFull(width int) string {
	target := m.fileView.path
	if !filepath.IsAbs(target) {
		target = filepath.Join(m.cwd, target)
	}
	// Stream the read and stop at the cap: os.ReadFile would load a multi-GB
	// file wholesale before any truncation, which is the exact render freeze
	// fileViewMaxLines exists to prevent.
	file, err := os.Open(target)
	if err != nil {
		return zeroTheme.faint.Render("Could not read file: " + err.Error())
	}
	defer file.Close()
	var lines []string
	truncated := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if len(lines) == fileViewMaxLines {
			truncated = true
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		if len(lines) == 0 {
			return zeroTheme.faint.Render("Could not read file: " + err.Error())
		}
		truncated = true // e.g. a single over-long line mid-file: show what we have
	}

	changed := m.fileViewChangedLines()
	gutterW := len(fmt.Sprintf("%d", len(lines)))
	textBudget := maxInt(8, width-gutterW-3) // gutter + space + marker column
	// Highlight with an effectively-infinite measure so the highlighter never
	// wraps — output lines stay 1:1 with file lines and the gutter numbering
	// can't desync. Each line is then truncated to the column budget below.
	display, ok := highlightCodeForPath(lines, m.fileView.path, 1<<20, nil)
	if !ok || len(display) != len(lines) {
		display = lines // no lexer for this path: render plain
	}

	var b strings.Builder
	for i, line := range display {
		line = fitStyledLine(line, textBudget)
		if i > 0 {
			b.WriteString("\n")
		}
		marker := " "
		if changed[strings.TrimSpace(lines[i])] {
			marker = zeroTheme.accent.Render("▎")
		}
		b.WriteString(zeroTheme.faintest.Render(fmt.Sprintf("%*d ", gutterW, i+1)))
		b.WriteString(marker)
		b.WriteString(line)
	}
	if truncated {
		// No exact remaining-line count: computing one would require reading the
		// rest of the file, defeating the bounded read above.
		b.WriteString("\n")
		b.WriteString(zeroTheme.faint.Render(fmt.Sprintf("… more lines (file truncated at %d for display)", len(lines))))
	}
	return b.String()
}

// fileViewChangedLines collects the trimmed text of every line the session's
// diffs ADDED to the viewed file, for the full-mode gutter markers. Very short
// lines ("}", "return", ")") are skipped: text-matching them would mark every
// unrelated occurrence across the file, which misleads far more than a missing
// marker on a brace line ever could.
func (m model) fileViewChangedLines() map[string]bool {
	changed := map[string]bool{}
	for _, row := range m.fileViewResultRows() {
		for line := range strings.SplitSeq(row.detail, "\n") {
			if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
				continue
			}
			if text := strings.TrimSpace(strings.TrimPrefix(line, "+")); len(text) >= 4 {
				changed[text] = true
			}
		}
	}
	return changed
}
