package tui

import (
	"os"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
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
	toggle    bool
	live      bool
}

type transcriptCopiedMsg struct {
	chars int
	// err is set when neither the native clipboard nor the OSC52 fallback landed
	// the copy, so the status line can report the failure instead of "Copied!".
	err error
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
		previousKind, havePreviousKind := previousVisibleTranscriptKind(m.transcript, m.flushed, rc)
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
			if (shownAny || (m.flushedAny && havePreviousKind)) && previousKind == rowUser && row.kind == rowReasoning {
				appendBlank()
			}
			start := len(lines)
			rendered, rowSelectable := m.renderTranscriptRow(index, row, width, rc, start)
			appendBlock(rendered)
			selectable = append(selectable, rowSelectable...)
			shownAny = true
			previousKind = row.kind
			havePreviousKind = true
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
			start := appendBlock(m.interimBlock(width))
			selectable = append(selectable, m.renderSelectableStreamingReasoning(width, start)...)
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
	case rowReasoning:
		return m.renderSelectableReasoningRow(rowIndex, row, width, startBodyY)
	case rowToolResult:
		return m.renderSelectableToolResultRow(rowIndex, row, width, rc, startBodyY)
	default:
		return m.renderRow(row, width, rc), nil
	}
}

// renderSelectableToolResultRow renders the tool result card and marks its head
// (first line) as a clickable collapse/expand toggle while the row is live.
func (m model) renderSelectableToolResultRow(rowIndex int, row transcriptRow, width int, rc rowContext, startBodyY int) (string, []transcriptSelectableLine) {
	rendered := m.renderRow(row, width, rc)
	if rendered == "" {
		return "", nil
	}
	return rendered, []transcriptSelectableLine{{bodyY: startBodyY, rowIndex: rowIndex, toggle: true}}
}

func (m model) renderSelectableUserRow(rowIndex int, row transcriptRow, width int, startBodyY int) (string, []transcriptSelectableLine) {
	contentWidth := userPromptContentWidth(width)
	wrapped := wrapPlainText(row.text, maxInt(1, contentWidth))
	lines := make([]string, 0, len(wrapped)+2)
	selectable := make([]transcriptSelectableLine, 0, len(wrapped))
	lines = append(lines, renderUserPromptPaddingLine(width))
	for index, line := range wrapped {
		meta := transcriptSelectableLine{
			bodyY:     startBodyY + index + 1,
			rowIndex:  rowIndex,
			textStart: lipgloss.Width(userPromptPrefix),
			text:      line,
		}
		selectable = append(selectable, meta)
		lines = append(lines, renderUserPromptStyledLine(m.renderTranscriptSelectableText(meta, zeroTheme.onUserPrompt(zeroTheme.ink.Bold(true))), contentWidth))
	}
	lines = append(lines, renderUserPromptPaddingLine(width))
	return strings.Join(lines, "\n"), selectable
}

func (m model) renderSelectableAssistantRow(rowIndex int, row transcriptRow, width int, startBodyY int) (string, []transcriptSelectableLine) {
	tableMeasure := width
	wrapped := renderAssistantMarkdownText(row.text, assistantMeasure(width), tableMeasure)
	lines := make([]string, 0, len(wrapped)+1)
	selectable := make([]transcriptSelectableLine, 0, len(wrapped))
	textStyle := zeroTheme.sayText
	if row.final {
		textStyle = zeroTheme.ink
	}
	for index, line := range wrapped {
		plainLine := stripMarkdownRenderControls(line)
		meta := transcriptSelectableLine{
			bodyY:     startBodyY + index,
			rowIndex:  rowIndex,
			textStart: 0,
			text:      plainLine,
		}
		selectable = append(selectable, meta)
		rendered := m.renderTranscriptSelectableMarkdownText(meta, line, textStyle)
		lines = append(lines, rendered)
	}
	if row.final {
		lines = append(lines, doneLine(row, false))
	}
	return strings.Join(lines, "\n"), selectable
}

func (m model) renderSelectableReasoningRow(rowIndex int, row transcriptRow, width int, startBodyY int) (string, []transcriptSelectableLine) {
	lines, selectable := m.renderSelectableReasoningBlock(rowIndex, row.text, row.expanded, false, row.turnElapsed, width, startBodyY)
	return strings.Join(lines, "\n"), selectable
}

func (m model) renderSelectableStreamingReasoning(width int, startBodyY int) []transcriptSelectableLine {
	_, selectable := m.renderSelectableReasoningBlock(-1, m.streamingReasoning, m.streamingReasoningExpanded, true, 0, width, startBodyY)
	for index := range selectable {
		selectable[index].live = true
	}
	return selectable
}

func (m model) renderSelectableReasoningBlock(rowIndex int, text string, expanded bool, running bool, elapsed time.Duration, width int, startBodyY int) ([]string, []transcriptSelectableLine) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	headerPlain := reasoningHeaderText(text, expanded, running, elapsed)
	header := reasoningHeaderLine(text, expanded, running, elapsed)
	headerMeta := transcriptSelectableLine{
		bodyY:     startBodyY,
		rowIndex:  rowIndex,
		textStart: 0,
		text:      headerPlain,
		toggle:    true,
	}
	headerRendered := header
	if _, _, ok := m.selectedColumnsForTranscriptLine(headerMeta); ok {
		headerRendered = m.renderTranscriptSelectableText(headerMeta, zeroTheme.faint)
	}
	lines := []string{headerRendered}
	selectable := []transcriptSelectableLine{headerMeta}
	if expanded {
		renderedLines := renderReasoningBodyLines(text, width)
		plainLines := renderAssistantMarkdownPlainText(text, maxInt(16, sayMeasure(width)-2), maxInt(16, sayMeasure(width)-2))
		for index, line := range renderedLines {
			plainLine := ""
			if index < len(plainLines) {
				plainLine = plainLines[index]
			}
			meta := transcriptSelectableLine{
				bodyY:     startBodyY + index + 1,
				rowIndex:  rowIndex,
				textStart: 2,
				text:      plainLine,
			}
			selectable = append(selectable, meta)
			rendered := styleAssistantMarkdownLine(line, zeroTheme.sayText)
			if _, _, ok := m.selectedColumnsForTranscriptLine(meta); ok {
				rendered = m.renderTranscriptSelectableText(meta, zeroTheme.sayText)
			}
			lines = append(lines, fitStyledLine("  "+rendered, width))
		}
	}
	return lines, selectable
}

func (m model) renderTranscriptSelectableMarkdownText(line transcriptSelectableLine, styledText string, base lipgloss.Style) string {
	if _, _, ok := m.selectedColumnsForTranscriptLine(line); ok {
		return m.renderTranscriptSelectableText(line, base)
	}
	return styleAssistantMarkdownLine(styledText, base)
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
	if !m.altScreen || m.height <= 0 || m.setup.visible || m.providerWizard != nil || m.mcpManager != nil || m.picker != nil || m.suggestionsActive() {
		return transcriptSelectableLine{}, false
	}
	width := chatWidth(m.width)
	body, selectable := m.transcriptBody(width, "")
	start, available := m.transcriptViewportStart(body, width)
	if mouseY(msg) < 0 || mouseY(msg) >= available {
		return transcriptSelectableLine{}, false
	}
	bodyY := start + mouseY(msg)
	for _, line := range selectable {
		if line.bodyY != bodyY {
			continue
		}
		if mouseX(msg) < 0 {
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
		if line.toggle {
			if line.live {
				m.streamingReasoningExpanded = !m.streamingReasoningExpanded
			} else {
				m = m.toggleTranscriptRow(line.rowIndex)
			}
			return m, nil, true
		}
		point := transcriptSelectionPointForMouse(line, mouseX(msg))
		m.copyStatus = ""
		m.transcriptSelection = transcriptSelectionState{active: true, anchor: point, cursor: point}
		return m, nil, true
	case mouseMotion(msg):
		if !m.transcriptSelection.active {
			return m, nil, false
		}
		line, ok := m.transcriptLineAtMouse(msg)
		if ok {
			m.transcriptSelection.cursor = transcriptSelectionPointForMouse(line, mouseX(msg))
		}
		return m, nil, true
	case mouseRelease(msg):
		if !m.transcriptSelection.active {
			return m, nil, false
		}
		if line, ok := m.transcriptLineAtMouse(msg); ok {
			m.transcriptSelection.cursor = transcriptSelectionPointForMouse(line, mouseX(msg))
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

// toggleTranscriptRow flips the collapse state of a collapsible row (a provider
// thought or a tool result card).
func (m model) toggleTranscriptRow(rowIndex int) model {
	if rowIndex < 0 || rowIndex >= len(m.transcript) {
		return m
	}
	switch m.transcript[rowIndex].kind {
	case rowReasoning, rowToolResult:
		m.transcript[rowIndex].expanded = !m.transcript[rowIndex].expanded
	}
	return m
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
		// Prefer the native OS clipboard (pbcopy / clip.exe / xclip): it works on
		// local terminals — including macOS Terminal.app, which has no OSC52 support
		// at all — so the auto-copy-on-select actually lands on the clipboard. Fall
		// back to OSC52 (forwarded by the terminal) for remote/SSH sessions where no
		// local clipboard utility is reachable.
		if err := clipboard.WriteAll(text); err != nil {
			if _, oscErr := os.Stdout.WriteString(ansi.SetSystemClipboard(text)); oscErr != nil {
				// Both paths failed; report it rather than claiming a copy that
				// never reached any clipboard.
				return transcriptCopiedMsg{err: err}
			}
		}
		return transcriptCopiedMsg{chars: utf8.RuneCountInString(text)}
	}
}
