// sidebar.go renders the right-hand context sidebar for the two-column chat
// layout (alt-screen managed mode only). The sidebar surfaces three sections —
// the spawned AGENTS and their live working detail, the live PLAN (the same data
// the pinned plan panel reads), and a token/context readout at the bottom — so
// the chat column stays focused on the conversation. It is a set of pure
// helpers: the layout in
// transcriptView renders the chat at a reduced width via the existing scroll
// engine, builds a sidebar block of the same height here, and joins the two
// columns row-by-row through joinColumns.
package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// sidebar geometry. The sidebar takes ~30% of the width, clamped so it never
// crowds the chat on a narrow terminal nor sprawls on a wide one. A 1-cell
// divider sits between the two columns.
const (
	sidebarMinWidth  = 26
	sidebarMaxWidth  = 40
	sidebarMinColumn = 60 // below this total width the sidebar is suppressed
)

// sidebarWidth returns the sidebar column width for a given total width, or 0
// when the terminal is too narrow to justify a second column (the caller then
// renders the single-column chat at full width).
func sidebarWidth(total int) int {
	if total < sidebarMinColumn {
		return 0
	}
	return clamp(total*30/100, sidebarMinWidth, sidebarMaxWidth)
}

// sidebarActive reports whether the two-column layout should render: only in
// alt-screen managed mode, with a measured height, on a wide-enough terminal.
// The subchat drill-in keeps its own single-column view, so the sidebar is
// suppressed there.
func (m model) sidebarActive() bool {
	if !m.altScreen || m.height <= 0 || m.subchat.active {
		return false
	}
	if sidebarWidth(m.width) <= 0 {
		return false
	}
	// Full-screen overlays (setup, wizards, pickers, the empty-state suggestion
	// list) take over the chat column and render at full width; suppress the
	// second column while any is active so their geometry and mouse hit-testing
	// stay full-width as before.
	if m.setup.visible || m.providerWizard != nil || m.mcpAddWizard != nil ||
		m.mcpManager != nil || m.picker != nil || m.suggestionsActive() {
		return false
	}
	// Home/welcome screen: stay single-column until there's real conversation, so
	// the empty home screen isn't split by an (empty) sidebar.
	if m.transcriptEmpty() {
		return false
	}
	return true
}

// chatColumnWidth is the chat's render width: the full chat width normally, and
// the reduced left-column width when the two-column layout is active (total
// minus the sidebar and the 1-cell divider). All frame/geometry callers route
// through this so the rendered chat, the scroll engine, and mouse hit-testing
// agree on where the chat column ends.
func (m model) chatColumnWidth() int {
	if sw := m.sidebarWidthForLayout(); sw > 0 {
		return chatWidth(m.width - sw - 1)
	}
	return chatWidth(m.width)
}

// sidebarWidthForLayout returns the active sidebar column width, or 0 when the
// two-column layout is not active.
func (m model) sidebarWidthForLayout() int {
	if !m.sidebarActive() {
		return 0
	}
	return sidebarWidth(m.width)
}

// sidebarAgentHeader renders the AGENTS section header with a running/total
// count of the subagents spawned this turn.
func (m model) sidebarAgentHeader(width int) string {
	agents := m.specialists.all()
	if len(agents) == 0 {
		return sidebarHeader("AGENTS", width)
	}
	running := 0
	for _, a := range agents {
		if a.status == specialistRunning {
			running++
		}
	}
	return sidebarHeaderWithCount("AGENTS", fmt.Sprintf("%d/%d", running, len(agents)), width)
}

// sidebarAgentLines renders one line per spawned subagent — a status glyph
// (• running, ✓ done, ✗ error) and its name — plus a "↳ <tool> <detail>"
// working line for each running agent so the live subagent activity is visible.
// Returns nil when none are spawned (the caller then shows a placeholder).
func (m model) sidebarAgentLines(width int) []string {
	agents := m.specialists.all()
	if len(agents) == 0 {
		return nil
	}
	room := maxInt(4, width-3)
	var lines []string
	for _, a := range agents {
		var icon string
		switch a.status {
		case specialistRunning:
			icon = zeroTheme.accent.Render("•")
		case specialistError:
			icon = zeroTheme.red.Render("✗")
		default: // completed
			icon = zeroTheme.green.Render("✓")
		}
		name := strings.TrimSpace(a.name)
		if name == "" {
			name = "agent"
		}
		lines = append(lines, " "+icon+" "+zeroTheme.ink.Render(truncateStep(name, room)))
		if a.status != specialistRunning {
			continue
		}
		// Live working detail for a running subagent: current tool + arg hint,
		// falling back to the running tool count.
		detail := strings.TrimSpace(a.currentTool)
		if d := strings.TrimSpace(a.currentDetail); d != "" {
			if detail != "" {
				detail += " " + d
			} else {
				detail = d
			}
		}
		if detail == "" && a.toolCount > 0 {
			detail = fmt.Sprintf("%d tools", a.toolCount)
		}
		if detail != "" {
			lines = append(lines, "   "+zeroTheme.faint.Render("↳ "+truncateStep(detail, maxInt(2, room-2))))
		}
	}
	return lines
}

// renderContextSidebar builds the sidebar block: exactly height lines, each
// exactly width cells (after fitStyledLine + padding). Sections render top to
// bottom — FILES, PLAN — with the token readout pinned to the bottom line. Each
// section header is a faint uppercase label; items use ink/muted. Empty
// sections render a quiet placeholder rather than vanishing so the layout stays
// stable.
func (m model) renderContextSidebar(width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}

	var lines []string
	add := func(s string) { lines = append(lines, s) }

	// AGENTS section — spawned subagents and their live working detail.
	add(m.sidebarAgentHeader(width))
	agentLines := m.sidebarAgentLines(width)
	if len(agentLines) == 0 {
		add(sidebarPlaceholder("no agents spawned", width))
	} else {
		lines = append(lines, agentLines...)
	}

	// PLAN section.
	add("")
	add(m.sidebarPlanHeader(width))
	planLines := m.sidebarPlanLines(width)
	if len(planLines) == 0 {
		add(sidebarPlaceholder("no active plan", width))
	} else {
		lines = append(lines, planLines...)
	}

	// Token readout pinned to the bottom.
	tokenLine := m.sidebarTokenLine(width)
	// Reserve the bottom row for tokens; pad the gap so it sits at the floor.
	for len(lines) < height-1 {
		add("")
	}
	if len(lines) > height-1 {
		lines = lines[:height-1]
	}
	add(tokenLine)

	// Normalize every row to exactly width cells.
	for i := range lines {
		lines[i] = padStyledLine(lines[i], width)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// sidebarHeader renders an uppercase faint section header with an optional
// right-aligned suffix (used for the PLAN N/M count).
func sidebarHeader(label string, width int) string {
	return zeroTheme.faint.Render(strings.ToUpper(label))
}

// sidebarHeaderWithCount renders a section header with a right-aligned count
// (e.g. "PLAN   2/5"), padded to width.
func sidebarHeaderWithCount(label, count string, width int) string {
	left := zeroTheme.faint.Render(strings.ToUpper(label))
	right := zeroTheme.faint.Render(count)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// sidebarPlaceholder renders a quiet placeholder line for an empty section.
func sidebarPlaceholder(text string, width int) string {
	return " " + zeroTheme.faint.Render(truncateRunes(text, maxInt(1, width-1)))
}

// sidebarPlanHeader renders the PLAN section header with the done/total count.
func (m model) sidebarPlanHeader(width int) string {
	state := m.plan
	if state.isEmpty() {
		return sidebarHeader("PLAN", width)
	}
	total := len(state.steps)
	done := 0
	for _, step := range state.steps {
		if step.status == "completed" || step.status == "failed" {
			done++
		}
	}
	return sidebarHeaderWithCount("PLAN", fmt.Sprintf("%d/%d", done, total), width)
}

// sidebarPlanLines renders the plan step list for the sidebar using the same
// status glyphs as the pinned panel (✓ done, • in-progress, ○ pending, ✗
// failed), reading m.plan directly so it stays in sync. Returns nil for an
// empty plan (the caller then shows a placeholder).
func (m model) sidebarPlanLines(width int) []string {
	state := m.plan
	if state.isEmpty() {
		return nil
	}
	room := maxInt(4, width-3)
	lines := make([]string, 0, len(state.steps))
	for _, step := range state.steps {
		var icon, body string
		switch step.status {
		case "completed":
			icon = zeroTheme.green.Render("✓")
			body = zeroTheme.muted.Render(truncateStep(step.content, room))
		case "in_progress":
			icon = zeroTheme.accent.Render("•")
			body = zeroTheme.ink.Render(truncateStep(step.content, room))
		case "failed":
			icon = zeroTheme.red.Render("✗")
			body = zeroTheme.muted.Render(truncateStep(step.content, room))
		default: // pending
			icon = zeroTheme.faint.Render("○")
			body = zeroTheme.faint.Render(truncateStep(step.content, room))
		}
		lines = append(lines, " "+icon+" "+body)
	}
	return lines
}

// sidebarTokenLine renders the bottom token/context readout. It prefers the
// live context-fill figure (last request's input tokens) and falls back to the
// session's cumulative token count.
func (m model) sidebarTokenLine(width int) string {
	label := m.sidebarTokenText()
	if label == "" {
		label = "0 tokens"
	}
	return " " + zeroTheme.faint.Render(truncateRunes(label, maxInt(1, width-1)))
}

// sidebarTokenText computes the token figure shown at the sidebar floor: the
// last request's context usage when known, else the cumulative session tokens.
func (m model) sidebarTokenText() string {
	if m.usageTracker == nil {
		return ""
	}
	summary := m.usageTracker.Summary()
	if summary.LastRecord != nil {
		used := summary.LastRecord.Usage.InputTokens
		if used > 0 {
			if window := modelContextWindow(m.modelName); window > 0 {
				return fmt.Sprintf("%s / %s tokens", humanCount(used), humanCount(window))
			}
			return humanCount(used) + " tokens"
		}
	}
	if summary.RecordCount > 0 {
		return humanCount(summary.InputTokens+summary.OutputTokens) + " tokens"
	}
	if m.unpricedRequests > 0 {
		return humanCount(m.unpricedTokens) + " tokens"
	}
	return ""
}

// joinColumns splices a chat block and a sidebar block side-by-side, one
// divider cell between them, into total-width rows. Both blocks are normalized
// to their column widths and to the same row count first, so every joined row
// is exactly chatWidth + 1 + sidebarWidth cells and the columns stay aligned.
func joinColumns(chat []string, sidebar []string, chatW, sidebarW int) []string {
	rows := len(chat)
	if len(sidebar) > rows {
		rows = len(sidebar)
	}
	divider := zeroTheme.line.Render("│")
	out := make([]string, rows)
	for i := 0; i < rows; i++ {
		left := ""
		if i < len(chat) {
			left = chat[i]
		}
		right := ""
		if i < len(sidebar) {
			right = sidebar[i]
		}
		left = padStyledLine(left, chatW)
		right = padStyledLine(right, sidebarW)
		out[i] = left + divider + right
	}
	return out
}
