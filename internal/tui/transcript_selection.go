package tui

import (
	"os"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type transcriptSelectionPoint struct {
	bodyY int
	x     int
}

type transcriptSelectionState struct {
	active bool
	anchor transcriptSelectionPoint
	cursor transcriptSelectionPoint
}

type transcriptSelectableLine struct {
	bodyY     int
	rowIndex  int
	textStart int
	text      string
}

type transcriptCopiedMsg struct {
	chars int
}

type transcriptCopyStatusExpiredMsg struct {
	seq int
}

func (m model) transcriptBody(width int, emptyOverlay string) (string, []transcriptSelectableLine) {
	lines := []string{}
	selectable := []transcriptSelectableLine{}
	appendBlock := func(block string) int {
		start := len(lines)
		lines = append(lines, viewLines(block)...)
		return start
	}
	appendBlank := func() {
		lines = append(lines, "")
	}

	// The title bar prints once into scrollback on the first WindowSizeMsg;
	// until then it renders managed so the surface never appears headless.
	if !m.headerPrinted {
		appendBlock(m.titleBar(width))
	}

	if m.transcriptEmpty() && !m.pending {
		if emptyOverlay != "" {
			appendBlock(m.emptyStateWithOverlay(width, emptyOverlay))
		} else {
			appendBlock(m.emptyState(width))
		}
	} else {
		rc := buildRowContext(m.transcript)
		shownAny := false
		for index := m.flushed; index < len(m.transcript); index++ {
			row := m.transcript[index]
			// A welcome row carries no Lime visual (the empty state replaced it)
			// and a resolved tool call collapses into its result's card.
			if row.kind == rowWelcome || rc.skip(row) {
				continue
			}
			// Blank-line separation before turns, including between flushed
			// history and the first live row.
			if (shownAny || m.flushedAny) && startsTurn(row.kind) {
				appendBlank()
			}
			start := len(lines)
			rendered, rowSelectable := m.renderTranscriptRow(index, row, width, rc, start)
			appendBlock(rendered)
			selectable = append(selectable, rowSelectable...)
			shownAny = true
		}
	}

	if m.pending {
		appendBlank()
		switch {
		case m.pendingPermission != nil:
			appendBlock(renderFocusedPermissionPrompt(m.pendingPermission.request, width))
		case m.pendingAskUser != nil:
			appendBlock(renderFocusedAskUserPrompt(*m.pendingAskUser, m.input.Value(), width))
		default:
			appendBlock(m.interimBlock(width))
		}
	}
	if m.pendingSpecReview != nil {
		appendBlank()
		appendBlock(renderFocusedSpecReviewPrompt(*m.pendingSpecReview, width))
	}

	return strings.Join(lines, "\n"), selectable
}

func (m model) renderTranscriptRow(rowIndex int, row transcriptRow, width int, rc rowContext, startBodyY int) (string, []transcriptSelectableLine) {
	switch row.kind {
	case rowUser:
		return m.renderSelectableUserRow(rowIndex, row, width, startBodyY)
	case rowAssistant:
		return m.renderSelectableAssistantRow(rowIndex, row, width, startBodyY)
	default:
		return m.renderRow(row, width, rc), nil
	}
}

func (m model) renderSelectableUserRow(rowIndex int, row transcriptRow, width int, startBodyY int) (string, []transcriptSelectableLine) {
	wrapped := wrapPlainText(row.text, sayMeasure(width))
	lines := make([]string, 0, len(wrapped))
	selectable := make([]transcriptSelectableLine, 0, len(wrapped))
	for index, line := range wrapped {
		prefix := "  "
		if index == 0 {
			prefix = "❯ "
		}
		meta := transcriptSelectableLine{
			bodyY:     startBodyY + index,
			rowIndex:  rowIndex,
			textStart: lipgloss.Width(prefix),
			text:      line,
		}
		selectable = append(selectable, meta)
		lines = append(lines, zeroTheme.userPrompt.Render(prefix)+m.renderTranscriptSelectableText(meta, zeroTheme.ink))
	}
	return strings.Join(lines, "\n"), selectable
}

func (m model) renderSelectableAssistantRow(rowIndex int, row transcriptRow, width int, startBodyY int) (string, []transcriptSelectableLine) {
	wrapped := wrapPlainText(row.text, sayMeasure(width))
	lines := make([]string, 0, len(wrapped)+1)
	selectable := make([]transcriptSelectableLine, 0, len(wrapped))
	prefix := ""
	textStyle := zeroTheme.sayText
	if row.final {
		prefix = "│ "
		textStyle = zeroTheme.ink
	}
	for index, line := range wrapped {
		meta := transcriptSelectableLine{
			bodyY:     startBodyY + index,
			rowIndex:  rowIndex,
			textStart: lipgloss.Width(prefix),
			text:      line,
		}
		selectable = append(selectable, meta)
		rendered := m.renderTranscriptSelectableText(meta, textStyle)
		if row.final {
			rendered = zeroTheme.finalRail.Render(prefix) + rendered
		}
		lines = append(lines, rendered)
	}
	if row.final {
		lines = append(lines, doneLine(row, false))
	}
	return strings.Join(lines, "\n"), selectable
}

func (m model) renderTranscriptSelectableText(line transcriptSelectableLine, base lipgloss.Style) string {
	start, end, ok := m.selectedColumnsForTranscriptLine(line)
	if !ok {
		return base.Render(line.text)
	}
	before, rest := splitPlainAtDisplayWidth(line.text, start)
	middle, after := splitPlainAtDisplayWidth(rest, end-start)
	return base.Render(before) + zeroTheme.selection.Render(middle) + base.Render(after)
}

func (m model) selectedColumnsForTranscriptLine(line transcriptSelectableLine) (int, int, bool) {
	if !m.transcriptSelection.active {
		return 0, 0, false
	}
	startPoint, endPoint := orderedTranscriptSelectionPoints(m.transcriptSelection.anchor, m.transcriptSelection.cursor)
	if line.bodyY < startPoint.bodyY || line.bodyY > endPoint.bodyY {
		return 0, 0, false
	}
	lineStart := line.textStart
	lineEnd := line.textStart + lipgloss.Width(line.text)
	start := lineStart
	end := lineEnd
	if line.bodyY == startPoint.bodyY {
		start = clampInt(startPoint.x, lineStart, lineEnd)
	}
	if line.bodyY == endPoint.bodyY {
		end = clampInt(endPoint.x, lineStart, lineEnd)
	}
	if end <= start {
		return 0, 0, false
	}
	return start - line.textStart, end - line.textStart, true
}

func orderedTranscriptSelectionPoints(a transcriptSelectionPoint, b transcriptSelectionPoint) (transcriptSelectionPoint, transcriptSelectionPoint) {
	if a.bodyY < b.bodyY || a.bodyY == b.bodyY && a.x <= b.x {
		return a, b
	}
	return b, a
}

func splitPlainAtDisplayWidth(text string, width int) (string, string) {
	if width <= 0 {
		return "", text
	}
	used := 0
	for index, glyph := range text {
		glyphWidth := lipgloss.Width(string(glyph))
		if used+glyphWidth > width {
			return text[:index], text[index:]
		}
		used += glyphWidth
	}
	return text, ""
}

func (m model) transcriptLineAtMouse(msg tea.MouseMsg) (transcriptSelectableLine, bool) {
	if !m.altScreen || m.height <= 0 || m.setup.visible || m.providerWizard != nil || m.picker != nil || m.suggestionsActive() {
		return transcriptSelectableLine{}, false
	}
	width := chatWidth(m.width)
	body, selectable := m.transcriptBody(width, "")
	start, available := m.transcriptViewportStart(body, width)
	if msg.Y < 0 || msg.Y >= available {
		return transcriptSelectableLine{}, false
	}
	bodyY := start + msg.Y
	for _, line := range selectable {
		if line.bodyY != bodyY {
			continue
		}
		if msg.X < 0 {
			return transcriptSelectableLine{}, false
		}
		return line, true
	}
	return transcriptSelectableLine{}, false
}

func (m model) transcriptViewportStart(body string, width int) (int, int) {
	bodyLines := viewLines(body)
	footerLines := viewLines(m.footerView(width))
	available := m.height - len(footerLines)
	if available < 1 {
		available = 1
	}
	maxOffset := maxInt(0, len(bodyLines)-available)
	offset := clamp(m.chatScrollOffset, 0, maxOffset)
	start := maxInt(0, len(bodyLines)-available-offset)
	return start, available
}

func transcriptSelectionPointForMouse(line transcriptSelectableLine, x int) transcriptSelectionPoint {
	lineEnd := line.textStart + lipgloss.Width(line.text)
	return transcriptSelectionPoint{
		bodyY: line.bodyY,
		x:     clampInt(x, line.textStart, lineEnd),
	}
}

func (m model) handleTranscriptSelectionMouse(msg tea.MouseMsg) (model, tea.Cmd, bool) {
	switch {
	case mouseLeftPress(msg):
		line, ok := m.transcriptLineAtMouse(msg)
		if !ok {
			if m.transcriptSelection.active {
				m.transcriptSelection = transcriptSelectionState{}
				return m, nil, true
			}
			return m, nil, false
		}
		point := transcriptSelectionPointForMouse(line, msg.X)
		m.copyStatus = ""
		m.transcriptSelection = transcriptSelectionState{active: true, anchor: point, cursor: point}
		return m, nil, true
	case mouseMotion(msg):
		if !m.transcriptSelection.active {
			return m, nil, false
		}
		line, ok := m.transcriptLineAtMouse(msg)
		if ok {
			m.transcriptSelection.cursor = transcriptSelectionPointForMouse(line, msg.X)
		}
		return m, nil, true
	case mouseRelease(msg):
		if !m.transcriptSelection.active {
			return m, nil, false
		}
		if line, ok := m.transcriptLineAtMouse(msg); ok {
			m.transcriptSelection.cursor = transcriptSelectionPointForMouse(line, msg.X)
		}
		text := m.selectedTranscriptText()
		if strings.TrimSpace(text) == "" {
			m.transcriptSelection = transcriptSelectionState{}
			return m, nil, true
		}
		return m, copyTranscriptSelectionCmd(text), true
	default:
		return m, nil, false
	}
}

func (m model) selectedTranscriptText() string {
	width := chatWidth(m.width)
	_, selectable := m.transcriptBody(width, "")
	parts := []string{}
	for _, line := range selectable {
		start, end, ok := m.selectedColumnsForTranscriptLine(line)
		if !ok {
			continue
		}
		before, rest := splitPlainAtDisplayWidth(line.text, start)
		_ = before
		selected, _ := splitPlainAtDisplayWidth(rest, end-start)
		parts = append(parts, selected)
	}
	return strings.Join(parts, "\n")
}

func copyTranscriptSelectionCmd(text string) tea.Cmd {
	return func() tea.Msg {
		_, _ = os.Stdout.WriteString(ansi.SetSystemClipboard(text))
		return transcriptCopiedMsg{chars: utf8.RuneCountInString(text)}
	}
}
