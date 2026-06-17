package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Gitlawb/zero/internal/tools"
)

const (
	// planPanelWidth is the column width of the floating plan widget.
	planPanelWidth = 40
	// planPanelMinChat is the narrowest the chat may stay beside the widget;
	// below it the widget is hidden so a small terminal isn't covered.
	planPanelMinChat = 48
	// planPanelMaxItems caps how many plan rows the widget lists before it
	// summarizes the remainder.
	planPanelMaxItems = 14
	// planPanelReserveRows is how many bottom chat rows (newest content + the
	// composer) the floating right column must never cover, so a tall column
	// cannot bury the whole conversation on a short terminal.
	planPanelReserveRows = 5
	// planToolName is the tool whose call marks a run as having a plan.
	planToolName = "update_plan"
)

// rightColumnBase is the shared visibility gate for the floating right column
// (the Plan card stacked over the Code card). It shows ONLY in full-screen mode
// while a run is in flight, and never steals so much width that the chat becomes
// unreadable. The individual cards add their own "is there anything to show"
// test on top (a live plan, a live edit) so a trivial "hi" shows nothing.
func (m model) rightColumnBase() bool {
	// Require at least one drawable row ABOVE the bottom reserve: composeWithPlanPanel
	// overlays only m.height-planPanelReserveRows rows, so on a short terminal
	// (height <= planPanelReserveRows) it would draw zero rows while activation still
	// reported visible. Gate by the same capacity so the two never diverge.
	if !m.altScreen || m.height <= planPanelReserveRows || m.setup.visible || m.transcriptDetailed {
		return false
	}
	if !m.pending {
		return false
	}
	if chatWidth(m.width)-planPanelWidth < planPanelMinChat {
		return false // keep the chat readable on small terminals
	}
	return true
}

// planPanelActive reports whether the floating plan widget should be drawn. It
// shows ONLY while a run is in flight AND that run has actually produced a plan
// (called update_plan) — so a trivial "hi" never shows a stale plan from an
// earlier task, and the widget disappears the moment the run finishes.
func (m model) planPanelActive() bool {
	return m.rightColumnBase() && m.currentRunHasPlan()
}

// chatAreaWidth is the chat content width. The plan widget FLOATS over the
// top-right corner rather than reserving a column, so this is simply the full
// chat width — the renderers and mouse hit-tests are unchanged by the widget.
func (m model) chatAreaWidth() int {
	return chatWidth(m.width)
}

// currentRunHasPlan reports whether the active run called update_plan (so the
// live plan belongs to what the agent is doing right now, not a previous task).
func (m model) currentRunHasPlan() bool {
	if m.activeRunID == 0 {
		return false
	}
	for _, row := range m.transcript {
		if row.runID == m.activeRunID && row.kind == rowToolCall && row.tool == planToolName {
			return true
		}
	}
	return false
}

// currentPlanItems returns the live update_plan steps, or nil.
func (m model) currentPlanItems() []tools.PlanItem {
	if m.registry == nil {
		return nil
	}
	tool, ok := m.registry.Get(planToolName)
	if !ok {
		return nil
	}
	reader, ok := tool.(currentPlanReader)
	if !ok {
		return nil
	}
	return reader.CurrentPlan()
}

// runningToolName returns the name of the most recent tool call that has not yet
// produced a result (the one currently executing), or "".
func (m model) runningToolName() string {
	rc := buildRowContext(m.transcript)
	for index := len(m.transcript) - 1; index >= 0; index-- {
		row := m.transcript[index]
		if row.kind == rowToolCall && row.id != "" && !rc.resolved[rcKey(row.runID, row.id)] {
			return row.tool
		}
	}
	return ""
}

// planActivityLabel is the human word for what the agent is doing right now.
// Empty when no run is in flight.
func (m model) planActivityLabel() string {
	if !m.pending {
		return ""
	}
	if strings.TrimSpace(m.streamingText) != "" {
		return "Responding"
	}
	switch toolActivityKind(m.runningToolName()) {
	case "plan":
		return "Planning"
	case "build":
		return "Building"
	case "scan":
		return "Scanning"
	case "shell":
		return "Running"
	case "search":
		return "Searching"
	default:
		return "Thinking"
	}
}

// toolActivityKind buckets a tool name into a coarse activity for the label.
func toolActivityKind(tool string) string {
	switch tool {
	case "update_plan":
		return "plan"
	case "write_file", "edit_file", "apply_patch", "create_file", "str_replace", "multi_edit":
		return "build"
	case "read_file", "list_dir", "grep", "glob", "ripgrep", "search_files", "list_files":
		return "scan"
	case "bash", "shell", "run", "exec":
		return "shell"
	case "web_search", "web_fetch", "fetch", "search":
		return "search"
	default:
		return ""
	}
}

// planStatusGlyph maps a plan step's status to a glyph + style.
func planStatusGlyph(status string) (string, lipgloss.Style) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in_progress", "in-progress", "active", "doing":
		return "◐", zeroTheme.accent
	case "done", "completed", "complete":
		return "✓", zeroTheme.accent
	case "blocked", "failed", "error":
		return "✗", zeroTheme.red
	default: // pending / todo / ""
		return "○", zeroTheme.faint
	}
}

func formatPanelElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// cutRunesEllipsis trims to limit runes, ending in "…" when it had to cut, so a
// long plan step reads as truncated rather than a hard break mid-word.
func cutRunesEllipsis(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

// renderPlanPanel draws the COMPACT widget: a "Plan" title, the live activity
// line (spinner + word + elapsed), then the plan steps with status glyphs — in a
// small box sized to its content (never the full terminal height).
func (m model) renderPlanPanel() string {
	inner := planPanelWidth - 4 // "│ " prefix + " │" suffix

	body := []string{zeroTheme.accent.Bold(true).Render("Plan")}
	if label := m.planActivityLabel(); label != "" {
		status := strings.TrimSpace(m.spinner.View()) + " " + label
		if !m.runStartedAt.IsZero() {
			status += "  " + formatPanelElapsed(m.now().Sub(m.runStartedAt))
		}
		body = append(body, zeroTheme.faint.Render(cutRunesEllipsis(status, inner)))
	}
	body = append(body, "")

	items := m.currentPlanItems()
	for index, item := range items {
		if index >= planPanelMaxItems {
			body = append(body, zeroTheme.faint.Render(fmt.Sprintf("… %d more", len(items)-planPanelMaxItems)))
			break
		}
		glyph, style := planStatusGlyph(item.Status)
		text := cutRunesEllipsis(fmt.Sprintf("%d %s", index+1, item.Content), inner-2)
		body = append(body, style.Render(glyph)+" "+zeroTheme.ink.Render(text))
	}
	return framePlanPanel(body)
}

// framePlanPanel boxes body into a compact widget exactly len(body)+2 lines tall
// and planPanelWidth wide.
func framePlanPanel(body []string) string {
	border := zeroTheme.faint
	inner := planPanelWidth - 4
	rule := strings.Repeat("─", planPanelWidth-2)
	lines := make([]string, 0, len(body)+2)
	lines = append(lines, border.Render("╭"+rule+"╮"))
	for _, row := range body {
		fitted := fitStyledLine(row, inner)
		pad := maxInt(0, inner-lipgloss.Width(fitted))
		lines = append(lines, border.Render("│ ")+fitted+strings.Repeat(" ", pad)+border.Render(" │"))
	}
	lines = append(lines, border.Render("╰"+rule+"╯"))
	return strings.Join(lines, "\n")
}

// renderRightColumn builds the floating right column for the active run: the
// Plan card (when the run has a plan) stacked over the Code card (when the run
// has edited a file). Returns "" when neither card has anything to show, which
// is what keeps the column absent for trivial, non-planning, non-editing turns.
func (m model) renderRightColumn() string {
	if !m.rightColumnBase() {
		return ""
	}
	cards := make([]string, 0, 2)
	if m.currentRunHasPlan() {
		cards = append(cards, m.renderPlanPanel())
	}
	if path, diff := m.currentEditDiff(); diff != "" {
		cards = append(cards, m.renderCodeCard(path, diff))
	}
	if len(cards) == 0 {
		return ""
	}
	return strings.Join(cards, "\n")
}

// composeWithPlanPanel floats the right column over the TOP-RIGHT corner of the
// rendered chat: only the first len(column) rows are overlaid, on their rightmost
// planPanelWidth columns, leaving the transcript full-width everywhere else. A
// no-op when the column is empty. (Name kept for the View() call site; it now
// carries both the Plan card and the live Code card.)
func (m model) composeWithPlanPanel(content string) string {
	column := m.renderRightColumn()
	if column == "" {
		return content
	}
	widget := strings.Split(column, "\n")
	if len(widget) == 0 {
		return content
	}
	fullWidth := chatWidth(m.width)
	leftWidth := fullWidth - planPanelWidth
	if leftWidth < planPanelMinChat {
		return content
	}
	lines := strings.Split(content, "\n")
	// The column floats over the OLDER (top) rows only: always leave the most
	// recent chat lines and the composer visible at the bottom, so a tall column
	// (long plan + long diff) can never bury the whole conversation.
	overlayRows := len(widget)
	if m.height > 0 {
		if maxRows := m.height - planPanelReserveRows; overlayRows > maxRows {
			overlayRows = maxRows
		}
	}
	for index := 0; index < overlayRows && index < len(lines); index++ {
		left := fitStyledLine(lines[index], leftWidth)
		left += strings.Repeat(" ", maxInt(0, leftWidth-lipgloss.Width(left)))
		lines[index] = left + widget[index]
	}
	return strings.Join(lines, "\n")
}
