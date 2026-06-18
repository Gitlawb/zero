// task_table.go renders the full-width task table overlay.
//
// The task table is a drill-down view over every specialist the parent agent
// has spawned in the current turn: one row per specialist showing its index,
// name, task description, status (with lifecycle icon), elapsed time, token
// usage, and tool-call count. A summary bar above the rows totals the counts
// and tokens; a footer lists the key bindings. The overlay is wrapped in a
// double border so it reads as a distinct surface from the rounded specialist
// cards in the transcript.
//
// The state machine is intentionally tiny: taskTableState only owns visibility
// and the cursor index. The specialist rows themselves come from the live
// specialistTracker (see specialist_card.go), so the table always reflects the
// latest start/complete/tool/token events.
package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// taskTableContentWidth is the fixed visible width of every line inside the
// border box: a 2-space left indent plus the seven fixed-width columns
// (4 + 12 + 30 + 12 + 8 + 10 + 6 = 82). Columns are fixed so the table lays
// out predictably across the 80-120 column widths Zero targets; the double
// border adds one cell of framing on each side.
const taskTableContentWidth = 84

// taskTableColumn widths. The sum must match taskTableContentWidth minus the
// 2-space indent.
const (
	taskColIndex      = 4
	taskColSpecialist = 12
	taskColTask       = 30
	taskColStatus     = 12
	taskColTime       = 8
	taskColTokens     = 10
	taskColTools      = 6
)

// taskTableState owns the overlay's visibility and selected row. It holds no
// copy of the specialist rows — those are read from the specialistTracker on
// every render so the table never goes stale.
type taskTableState struct {
	visible bool
	cursor  int // selected row index, clamped to the row count on use
}

// toggle flips the overlay's visibility.
func (t *taskTableState) toggle() {
	t.visible = !t.visible
}

// show forces the overlay open.
func (t *taskTableState) show() {
	t.visible = true
}

// hide forces the overlay closed.
func (t *taskTableState) hide() {
	t.visible = false
}

// moveCursor advances the cursor by delta, clamping to [0, max-1]. A max of
// zero or less resets the cursor to the top of an empty table.
func (t *taskTableState) moveCursor(delta int, max int) {
	if max <= 0 {
		t.cursor = 0
		return
	}
	t.cursor += delta
	if t.cursor < 0 {
		t.cursor = 0
	}
	if t.cursor > max-1 {
		t.cursor = max - 1
	}
}

// selectedSessionID returns the childSessionID of the row under the cursor, or
// "" when the table is empty or the cursor is out of range. Callers use this to
// drill into a specialist's subchat on Enter.
func (t *taskTableState) selectedSessionID(specialists []specialistInfo) string {
	if len(specialists) == 0 {
		return ""
	}
	if t.cursor < 0 || t.cursor >= len(specialists) {
		return ""
	}
	return specialists[t.cursor].childSessionID
}

// taskTableHeight returns the total line count the overlay will occupy for the
// given specialist count: title + summary + header (3), one row per specialist,
// the footer (1), and the double border's top and bottom rules (2). It never
// reports less than the empty-overlay minimum of 6.
func taskTableHeight(specialistCount int) int {
	height := 3 + specialistCount + 1 + 2
	if height < 6 {
		height = 6
	}
	return height
}

// truncateColumn left-justifies s in a field of width runes, padding on the
// right with spaces. Strings wider than width are trimmed to show their tail
// behind a leading "…" (so a long task description keeps its distinguishing
// suffix). A non-positive width returns s unchanged so callers can disable a
// column without special casing.
func truncateColumn(s string, width int) string {
	if width <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s + strings.Repeat(" ", width-len(runes))
	}
	if width == 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(width-1):])
}

// padLeft right-justifies s in a field of width visible cells, padding on the
// left with spaces. It is rune-aware via lipgloss.Width so columns containing
// non-ASCII glyphs (status icons, the em dash placeholder) still align.
func padLeft(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

// padLineRight pads s on the right with spaces so every overlay line shares the
// same visible width, which keeps the double border's right edge straight.
func padLineRight(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// renderTaskTable renders the full-width task table overlay. With no tracked
// specialists it returns a single muted "No active tasks" line. Otherwise the
// overlay is a double-bordered box containing the title, a summary bar, the
// column header, one row per specialist, and a key-binding footer.
func (m model) renderTaskTable(width int) string {
	specialists := m.specialists.all()
	if len(specialists) == 0 {
		return zeroTheme.muted.Render("  No active tasks")
	}

	// The overlay does not reflow below the fixed column layout; width is
	// consulted only to keep the box from claiming more than the terminal.
	_ = width

	now := m.now()

	lines := make([]string, 0, len(specialists)+4)

	// Title line.
	lines = append(lines, padLineRight(zeroTheme.blue.Bold(true).Render("  TASK TABLE"), taskTableContentWidth))

	// Summary bar: counts by status, total tokens, and aggregate elapsed.
	running, completed, errors, totalTokens := 0, 0, 0, 0
	var earliestStart time.Time
	var latestDone time.Time
	for _, sp := range specialists {
		totalTokens += sp.tokenCount
		switch sp.status {
		case specialistRunning:
			running++
		case specialistCompleted:
			completed++
		case specialistError:
			errors++
		}
		if !sp.startedAt.IsZero() && (earliestStart.IsZero() || sp.startedAt.Before(earliestStart)) {
			earliestStart = sp.startedAt
		}
		if !sp.completedAt.IsZero() && sp.completedAt.After(latestDone) {
			latestDone = sp.completedAt
		}
	}
	elapsed := taskTableAggregateElapsed(specialists, now, earliestStart, latestDone)
	summary := fmt.Sprintf("  %d tasks: %d running, %d completed, %d errors · Total: %s tokens · %s",
		len(specialists), running, completed, errors, formatTokenCount(totalTokens), formatSpecialistElapsed(elapsed))
	lines = append(lines, padLineRight(zeroTheme.muted.Render(summary), taskTableContentWidth))

	// Header row. Numeric columns (Time, Tokens, Tools) are right-aligned to
	// match their row data; the rest are left-aligned.
	header := "  " +
		truncateColumn("#", taskColIndex) +
		truncateColumn("Specialist", taskColSpecialist) +
		truncateColumn("Task", taskColTask) +
		truncateColumn("Status", taskColStatus) +
		padLeft("Time", taskColTime) +
		padLeft("Tokens", taskColTokens) +
		padLeft("Tools", taskColTools)
	lines = append(lines, padLineRight(zeroTheme.blue.Render(header), taskTableContentWidth))

	// Data rows.
	for i, sp := range specialists {
		var prefix string
		if i == m.taskTable.cursor {
			prefix = zeroTheme.accent.Render("> ")
		} else {
			prefix = "  "
		}

		indexCol := truncateColumn(fmt.Sprintf("%d", i+1), taskColIndex)
		nameCol := truncateColumn(sp.name, taskColSpecialist)
		taskCol := truncateColumn(sp.description, taskColTask)
		statusCol := truncateColumn(taskTableStatusCell(m, sp), taskColStatus)
		timeCol := padLeft(taskTableTimeCell(sp, now), taskColTime)
		tokensCol := padLeft(formatTokenCount(sp.tokenCount), taskColTokens)
		toolsCol := padLeft(fmt.Sprintf("%d", sp.toolCount), taskColTools)

		row := prefix + indexCol + nameCol + taskCol + statusCol + timeCol + tokensCol + toolsCol
		lines = append(lines, padLineRight(row, taskTableContentWidth))
	}

	// Footer.
	footer := "  [Enter] drill into selected · [Ctrl+G/Esc] close · ↑/↓ navigate"
	lines = append(lines, padLineRight(zeroTheme.faint.Render(footer), taskTableContentWidth))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color(colorLine2))
	return boxStyle.Render(strings.Join(lines, "\n"))
}

// taskTableStatusCell renders the status icon plus its label, coloured by
// lifecycle: running uses the live spinner frame on amber, completed is a green
// tick, error is a red cross. Unknown statuses fall back to a muted bullet.
func taskTableStatusCell(m model, sp specialistInfo) string {
	var icon string
	switch sp.status {
	case specialistRunning:
		icon = m.spinner.View()
	case specialistCompleted:
		icon = "✓"
	case specialistError:
		icon = "✗"
	default:
		icon = "•"
	}
	text := icon + " " + specialistStatusString(sp.status)
	switch sp.status {
	case specialistRunning:
		return zeroTheme.amber.Render(text)
	case specialistCompleted:
		return zeroTheme.green.Render(text)
	case specialistError:
		return zeroTheme.red.Render(text)
	default:
		return zeroTheme.muted.Render(text)
	}
}

// taskTableTimeCell renders one row's elapsed duration. Running specialists
// tick against the current time; finished ones freeze at completedAt. A
// non-positive duration renders an em dash so an entry with no usable
// timestamps never shows "0s".
func taskTableTimeCell(sp specialistInfo, now time.Time) string {
	var elapsed time.Duration
	switch sp.status {
	case specialistRunning:
		elapsed = now.Sub(sp.startedAt)
	case specialistCompleted, specialistError:
		if !sp.completedAt.IsZero() {
			elapsed = sp.completedAt.Sub(sp.startedAt)
		}
	}
	if elapsed <= 0 {
		return "—"
	}
	return formatSpecialistElapsed(elapsed)
}

// taskTableAggregateElapsed computes the span covered by the summary bar: from
// the earliest specialist start to now while any specialist is still running,
// otherwise to the latest completion. A zero value is returned when no start
// time is known so the summary falls back to formatSpecialistElapsed's 1s
// floor rather than a misleading 0s.
func taskTableAggregateElapsed(specialists []specialistInfo, now time.Time, earliestStart time.Time, latestDone time.Time) time.Duration {
	if earliestStart.IsZero() {
		return 0
	}
	end := latestDone
	for _, sp := range specialists {
		if sp.status == specialistRunning {
			end = now
			break
		}
	}
	if end.IsZero() {
		end = now
	}
	return end.Sub(earliestStart)
}
