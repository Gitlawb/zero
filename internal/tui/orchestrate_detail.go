package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// The plan detail view: the plan's phases down the left, the selected phase's
// live agent detail on the right.
//
// Opened by pressing the PLAN header, closed with Esc. It is an OVERLAY, using
// the same slot the help and picker overlays use, so it composites over the
// chat rather than replacing the view — the conversation stays visible behind
// it and nothing about the transcript, footer or composer changes.
//
// It renders NOTHING when no plan has been admitted, which is what keeps a
// posture-off session unchanged. No separate posture gate is needed: a plan
// only exists under the posture, so there is no PLAN line to press without one.

const (
	// orchestrateDetailPhaseWidth is the left column. Wide enough for a task id
	// and its status glyph, narrow enough to leave the detail room.
	orchestrateDetailPhaseWidth = 22
	// orchestrateDetailMinWidth is the total below which the two-column layout
	// stops making sense and the detail alone is shown.
	orchestrateDetailMinWidth = 56
)

// orchestrateDetailOpen reports whether the view should render.
func (m model) orchestrateDetailOpen() bool {
	return m.orchestrateDetail && !m.orchestrate.isEmpty()
}

// openOrchestrateDetail opens the view on the task most worth looking at: the
// one running, else the first that failed, else the first task.
func (m model) openOrchestrateDetail() model {
	if m.orchestrate.isEmpty() {
		return m
	}
	m.orchestrateDetail = true
	m.orchestrateSelected = m.orchestrate.defaultSelection()
	return m
}

// defaultSelection picks the task a user opening the view wants to see first.
func (s orchestratePanelState) defaultSelection() int {
	for index, task := range s.tasks {
		if task.status == orchestrateRunning {
			return index
		}
	}
	for index, task := range s.tasks {
		if task.status == orchestrateFailed {
			return index
		}
	}
	return 0
}

// moveOrchestrateSelection walks the phase list, clamped at both ends so the
// selection never wraps onto an unrelated task.
func (m model) moveOrchestrateSelection(delta int) model {
	if len(m.orchestrate.tasks) == 0 {
		return m
	}
	next := m.orchestrateSelected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(m.orchestrate.tasks) {
		next = len(m.orchestrate.tasks) - 1
	}
	m.orchestrateSelected = next
	return m
}

// renderOrchestrateDetailOverlay draws the whole view, or "" when it is closed.
func (m model) renderOrchestrateDetailOverlay(width int) string {
	if !m.orchestrateDetailOpen() {
		return ""
	}
	if width < orchestrateDetailMinWidth {
		// Too narrow for two columns: the detail is what the user came for, so
		// the phase list is what goes.
		return m.orchestrateDetailBox(width, m.detailPaneLines(width-4))
	}

	phaseWidth := orchestrateDetailPhaseWidth
	detailWidth := width - phaseWidth - 7

	phases := strings.Join(m.phasePaneLines(phaseWidth), "\n")
	detail := strings.Join(m.detailPaneLines(detailWidth), "\n")

	columns := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(phaseWidth).Render(phases),
		lipgloss.NewStyle().Width(2).Render(""),
		lipgloss.NewStyle().Width(detailWidth).Render(detail),
	)
	return m.orchestrateDetailBox(width, strings.Split(columns, "\n"))
}

// orchestrateDetailBox wraps the content in the plan's title and footer hint.
func (m model) orchestrateDetailBox(width int, body []string) string {
	state := m.orchestrate
	title := strings.TrimSpace(state.name)
	if title == "" {
		title = "plan"
	}

	var b strings.Builder
	b.WriteString(zeroTheme.accent.Render(truncateRunes(title, maxInt(1, width-2))))
	done, failed, skipped, cancelled, _ := state.counts()
	subtitle := fmt.Sprintf("%d/%d done", done, len(state.tasks))
	if failed > 0 {
		subtitle += fmt.Sprintf(" · %d failed", failed)
	}
	if skipped > 0 {
		subtitle += fmt.Sprintf(" · %d skipped", skipped)
	}
	if cancelled > 0 {
		subtitle += fmt.Sprintf(" · %d cancelled", cancelled)
	}
	if state.tokensUsed > 0 {
		subtitle += fmt.Sprintf(" · %d tokens", state.tokensUsed)
	}
	b.WriteString("\n")
	b.WriteString(zeroTheme.faint.Render(truncateRunes(subtitle, maxInt(1, width-2))))
	b.WriteString("\n\n")
	b.WriteString(strings.Join(body, "\n"))
	b.WriteString("\n\n")
	hint := "↑/↓ phase   esc close"
	if m.selectedTaskSession() != "" {
		// Only offered when the selected task HAS a child session to open. A
		// hint for a key that does nothing trains the user to ignore hints.
		hint = "↑/↓ phase   enter open agent   esc close"
	}
	b.WriteString(zeroTheme.faint.Render(hint))
	return b.String()
}

// selectedTaskSession returns the selected task's child session id, or "" when
// it has none yet. A running task is still keyed by its temporary card key,
// which is not a session and cannot be opened.
func (m model) selectedTaskSession() string {
	if m.orchestrateSelected < 0 || m.orchestrateSelected >= len(m.orchestrate.tasks) {
		return ""
	}
	key := m.orchestrate.tasks[m.orchestrateSelected].cardKey
	if strings.HasPrefix(key, "specialist_") {
		return key
	}
	return ""
}

// phasePaneLines is the left column: every task, numbered, with the selected
// one marked. Numbered rather than indented — this is a list to navigate, and
// the dependency shape is on the panel and in /plans.
func (m model) phasePaneLines(width int) []string {
	lines := []string{zeroTheme.muted.Render("Phases")}
	for index, task := range m.orchestrate.tasks {
		marker := "  "
		style := zeroTheme.faint
		if index == m.orchestrateSelected {
			marker = "❯ "
			style = zeroTheme.ink
		}
		label := fmt.Sprintf("%s%d %s", marker, index+1, task.id)
		row := style.Render(truncateRunes(label, maxInt(1, width-6)))
		glyph := orchestrateGlyph(task.status)
		if task.status == orchestrateRunning {
			// The panel spins here; a static list should still distinguish the
			// task in flight from one that has not started, which the default
			// pending dot did not.
			glyph = zeroTheme.accent.Render("▸")
		}
		lines = append(lines, row+" "+glyph)
	}
	return lines
}

// detailPaneLines is the right column: what the selected task is doing.
func (m model) detailPaneLines(width int) []string {
	if m.orchestrateSelected < 0 || m.orchestrateSelected >= len(m.orchestrate.tasks) {
		return []string{zeroTheme.faint.Render("no task selected")}
	}
	task := m.orchestrate.tasks[m.orchestrateSelected]
	now := m.orchestrateNow()

	lines := []string{zeroTheme.ink.Render(truncateRunes(task.id, maxInt(1, width)))}

	status := task.status.label()
	if elapsed := task.elapsed(now); elapsed > 0 {
		status += " · " + formatElapsedSeconds(elapsed)
	}
	if len(task.dependsOn) > 0 {
		status += " · after " + strings.Join(task.dependsOn, ", ")
	}
	lines = append(lines, zeroTheme.faint.Render(truncateRunes(status, maxInt(1, width))))

	// Live agent state, read from the specialist tracker through the task's
	// card key. Absent for a task that never started, which is the honest
	// answer rather than a row of zeroes.
	info, hasCard := m.specialists.getBySessionID(task.cardKey)
	if hasCard {
		meta := fmt.Sprintf("%d tool calls", info.toolCount)
		if info.toolCount == 1 {
			meta = "1 tool call"
		}
		if info.tokenCount > 0 {
			meta += fmt.Sprintf(" · %s tokens", formatTokenCount(info.tokenCount))
		}
		lines = append(lines, zeroTheme.faint.Render(truncateRunes(meta, maxInt(1, width))))
	}

	if summary := strings.TrimSpace(task.summary); summary != "" {
		lines = append(lines, "", zeroTheme.muted.Render("Prompt"))
		lines = append(lines, wrapToWidth(summary, width, 3)...)
	}

	if hasCard && strings.TrimSpace(info.currentTool) != "" {
		lines = append(lines, "", zeroTheme.muted.Render("Activity"))
		activity := info.currentTool
		if detail := strings.TrimSpace(info.currentDetail); detail != "" {
			activity += "(" + detail + ")"
		}
		lines = append(lines, "  "+zeroTheme.faint.Render(truncateRunes(activity, maxInt(1, width-2))))
	}

	lines = append(lines, "", zeroTheme.muted.Render("Outcome"))
	lines = append(lines, "  "+zeroTheme.faint.Render(truncateRunes(m.orchestrateOutcomeLine(task, hasCard, info), maxInt(1, width-2))))
	return lines
}

// orchestrateOutcomeLine states where the task ended up, in one line.
func (m model) orchestrateOutcomeLine(task orchestrateTask, hasCard bool, info specialistInfo) string {
	switch task.status {
	case orchestrateRunning:
		return "still running…"
	case orchestratePending:
		return "waiting on " + strings.Join(task.dependsOn, ", ")
	case orchestrateDone:
		return "completed"
	}
	// Failed, skipped or cancelled: the reason is what matters, and the card's
	// error text is more specific than the status word.
	if hasCard && strings.TrimSpace(info.errorMsg) != "" {
		return firstLineOf(info.errorMsg)
	}
	return task.status.label()
}

func firstLineOf(text string) string {
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		return strings.TrimSpace(text[:index])
	}
	return strings.TrimSpace(text)
}

// wrapToWidth breaks text onto at most maxLines lines of the given width,
// cutting on RUNE boundaries and saying when it truncated.
func wrapToWidth(text string, width, maxLines int) []string {
	width = maxInt(4, width-2)
	runes := []rune(strings.Join(strings.Fields(text), " "))
	var lines []string
	for len(runes) > 0 && len(lines) < maxLines {
		take := width
		if take > len(runes) {
			take = len(runes)
		}
		lines = append(lines, "  "+zeroTheme.faint.Render(string(runes[:take])))
		runes = runes[take:]
	}
	if len(runes) > 0 && len(lines) > 0 {
		// Say that it was cut. A silently truncated prompt reads as the whole
		// prompt, and the full text is in the tool output either way.
		lines = append(lines, "  "+zeroTheme.faint.Render("…"))
	}
	return lines
}
