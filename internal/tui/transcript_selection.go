package tui

import (
	"os"
	"strconv"
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
	// permOption marks a clickable permission-popup choice; permChoice is the
	// decision a left-click on this row resolves. These rows carry no selectable
	// text (they are buttons, not content).
	permOption bool
	permChoice permissionDecision
	// specialistCard marks a clickable specialist card row. specialistID is
	// the childSessionID to drill into on click or Enter.
	specialistCard bool
	specialistID   string
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

type transcriptBodyItemKind int

const (
	transcriptBodyItemTitle transcriptBodyItemKind = iota
	transcriptBodyItemEmpty
	transcriptBodyItemSeparator
	transcriptBodyItemRow
	transcriptBodyItemPendingPrompt
	transcriptBodyItemPendingInterim
	transcriptBodyItemSpecReview
)

type transcriptBodyItem struct {
	kind              transcriptBodyItemKind
	rowIndex          int
	heightCacheKey    string
	heightCacheStable bool
	render            func(startBodyY int) transcriptBodyRenderedItem
}

type transcriptBodyRenderedItem struct {
	lines      []string
	selectable []transcriptSelectableLine
}

type transcriptBodyItemSpan struct {
	kind     transcriptBodyItemKind
	rowIndex int
	startY   int
	height   int
}

type transcriptBodyLayout struct {
	lines      []string
	selectable []transcriptSelectableLine
	spans      []transcriptBodyItemSpan
}

func (m model) transcriptBodyLayout(width int, emptyOverlay string) transcriptBodyLayout {
	return layoutTranscriptBodyItems(m.transcriptBodyItems(width, emptyOverlay))
}

func (m model) transcriptBody(width int, emptyOverlay string) (string, []transcriptSelectableLine) {
	layout := m.transcriptBodyLayout(width, emptyOverlay)
	return layout.String(), layout.selectable
}

func (l transcriptBodyLayout) String() string {
	return strings.Join(l.lines, "\n")
}

func (l transcriptBodyLayout) totalLines() int {
	if len(l.spans) > 0 {
		last := l.spans[len(l.spans)-1]
		return last.startY + last.height
	}
	return len(l.lines)
}

func (l transcriptBodyLayout) visibleLines(window transcriptViewportWindow) []string {
	start := clampInt(window.start, 0, len(l.lines))
	end := clampInt(window.end, start, len(l.lines))
	return append([]string(nil), l.lines[start:end]...)
}

func (m model) transcriptBodyItems(width int, emptyOverlay string) []transcriptBodyItem {
	items := []transcriptBodyItem{}

	// The inline title bar prints once into scrollback on the first WindowSizeMsg;
	// until then it renders managed so the surface never appears headless.
	if m.titleBarInTranscriptBody() {
		items = append(items, transcriptBlockBodyItem(transcriptBodyItemTitle, -1, m.titleBar(width)))
	}

	if m.transcriptEmpty() && !m.pending {
		if emptyOverlay != "" {
			items = append(items, transcriptBlockBodyItem(transcriptBodyItemEmpty, -1, m.emptyStateWithOverlay(width, emptyOverlay)))
		} else {
			items = append(items, transcriptBlockBodyItem(transcriptBodyItemEmpty, -1, m.emptyState(width)))
		}
	} else {
		rc := buildRowContext(m.transcript)
		shownAny := false
		previousKind, havePreviousKind := previousVisibleTranscriptKind(m.transcript, m.flushed, rc)
		specialistSummaryEmitted := false
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
				items = append(items, transcriptBlankBodyItem())
			}
			if (shownAny || (m.flushedAny && havePreviousKind)) && previousKind == rowUser && row.kind == rowReasoning {
				items = append(items, transcriptBlankBodyItem())
			}
			// Breathing room between back-to-back tool cards in the same turn: a
			// tool result collapses its call into one card, so consecutive cards
			// would otherwise stack with no gap (the dense "wall" look). One blank
			// line between them matches the reference agents. Turn-starters are
			// separated above, so this only fires tool-card -> tool-card.
			if shownAny && havePreviousKind && isToolCardKind(previousKind) && isToolCardKind(row.kind) {
				items = append(items, transcriptBlankBodyItem())
			}
			// The plan panel is no longer injected inline here — it is pinned
			// above the composer (see footerView) so a streaming turn can't push
			// it off-screen.
			// Inject the specialist summary line once, before the first
			// specialist card in this turn's contiguous group.
			if row.kind == rowSpecialist && !specialistSummaryEmitted {
				specialistSummaryEmitted = true
				specialists := m.specialists.all()
				if len(specialists) == 0 {
					// Fall back to the specialist info carried by the
					// transcript rows themselves (covers tests and any path
					// where the tracker has been cleared).
					for j := index; j < len(m.transcript); j++ {
						r := m.transcript[j]
						if r.kind != rowSpecialist || r.specialistInfo == nil {
							break
						}
						specialists = append(specialists, *r.specialistInfo)
					}
				}
				summary := renderSpecialistSummary(specialists, m.spinner.View())
				if summary != "" {
					items = append(items, transcriptBlockBodyItem(transcriptBodyItemRow, -1, summary))
					items = append(items, transcriptBlankBodyItem())
				}
			}
			rowIndex, transcriptRow := index, row
			heightCacheKey, heightCacheStable := m.transcriptRowBodyHeightCacheKey(transcriptRow, width, rc)
			items = append(items, transcriptBodyItem{
				kind:              transcriptBodyItemRow,
				rowIndex:          rowIndex,
				heightCacheKey:    heightCacheKey,
				heightCacheStable: heightCacheStable,
				render: func(startBodyY int) transcriptBodyRenderedItem {
					rendered, selectable := m.renderTranscriptRow(rowIndex, transcriptRow, width, rc, startBodyY)
					return transcriptBodyRenderedItem{lines: viewLines(rendered), selectable: selectable}
				},
			})
			shownAny = true
			previousKind = row.kind
			havePreviousKind = true
		}
		// The plan panel is pinned above the composer (footerView), not injected
		// into the scrolling body, so there is nothing to emit here anymore.
	}

	if m.pending {
		items = append(items, transcriptBlankBodyItem())
		switch {
		case m.pendingPermission != nil:
			perm := m.pendingPermission
			items = append(items, transcriptBodyItem{
				kind:              transcriptBodyItemPendingPrompt,
				rowIndex:          -1,
				heightCacheStable: false, // the highlight changes with the cursor
				render: func(startBodyY int) transcriptBodyRenderedItem {
					block, offsets := renderFocusedPermissionPrompt(perm.request, perm.cursor, width)
					options := permissionOptions(perm.request)
					selectable := make([]transcriptSelectableLine, 0, len(offsets))
					for index, offset := range offsets {
						if index >= len(options) {
							break
						}
						selectable = append(selectable, transcriptSelectableLine{
							bodyY:      startBodyY + offset,
							rowIndex:   -1,
							permOption: true,
							permChoice: options[index].choice,
						})
					}
					return transcriptBodyRenderedItem{lines: viewLines(block), selectable: selectable}
				},
			})
		case m.pendingAskUser != nil:
			items = append(items, transcriptBlockBodyItem(transcriptBodyItemPendingPrompt, -1, renderFocusedAskUserPrompt(*m.pendingAskUser, m.input.Value(), width)))
		default:
			items = append(items, transcriptBodyItem{
				kind:     transcriptBodyItemPendingInterim,
				rowIndex: -1,
				render: func(startBodyY int) transcriptBodyRenderedItem {
					return transcriptBodyRenderedItem{
						lines:      viewLines(m.interimBlock(width)),
						selectable: m.renderSelectableStreamingReasoning(width, startBodyY),
					}
				},
			})
		}
	}
	if m.pendingSpecReview != nil {
		items = append(items, transcriptBlankBodyItem())
		items = append(items, transcriptBlockBodyItem(transcriptBodyItemSpecReview, -1, renderFocusedSpecReviewPrompt(*m.pendingSpecReview, width)))
	}

	return items
}

func transcriptBlockBodyItem(kind transcriptBodyItemKind, rowIndex int, block string) transcriptBodyItem {
	return transcriptBodyItem{
		kind:              kind,
		rowIndex:          rowIndex,
		heightCacheKey:    transcriptBlockBodyHeightCacheKey(kind, block),
		heightCacheStable: true,
		render: func(int) transcriptBodyRenderedItem {
			return transcriptBodyRenderedItem{lines: viewLines(block)}
		},
	}
}

func transcriptBlankBodyItem() transcriptBodyItem {
	return transcriptBodyItem{
		kind:              transcriptBodyItemSeparator,
		rowIndex:          -1,
		heightCacheKey:    "transcript-body-height:v1:separator",
		heightCacheStable: true,
		render: func(int) transcriptBodyRenderedItem {
			return transcriptBodyRenderedItem{lines: []string{""}}
		},
	}
}

// transcriptBodyItemsFromRows builds body items from an arbitrary set of
// transcript rows (used by the subchat drill-in view to render a child
// session's events). It mirrors the main loop's logic but operates on the
// provided rows instead of m.transcript, and skips pending/permission state.
func (m model) transcriptBodyItemsFromRows(rows []transcriptRow, width int) []transcriptBodyItem {
	items := []transcriptBodyItem{}
	if len(rows) == 0 {
		items = append(items, transcriptBlockBodyItem(transcriptBodyItemEmpty, -1, "No events in this subagent session."))
		return items
	}
	rc := buildRowContext(rows)
	shownAny := false
	var previousKind rowKind
	havePreviousKind := false
	for index, row := range rows {
		if row.kind == rowWelcome || rc.skip(row) {
			continue
		}
		if shownAny && startsTurn(row.kind) {
			items = append(items, transcriptBlankBodyItem())
		}
		if shownAny && havePreviousKind && previousKind == rowUser && row.kind == rowReasoning {
			items = append(items, transcriptBlankBodyItem())
		}
		rowIndex, transcriptRow := index, row
		items = append(items, transcriptBodyItem{
			kind:           transcriptBodyItemRow,
			rowIndex:       rowIndex,
			heightCacheKey: "subchat:" + strconv.Itoa(index),
			render: func(startBodyY int) transcriptBodyRenderedItem {
				rendered, selectable := m.renderTranscriptRow(rowIndex, transcriptRow, width, rc, startBodyY)
				return transcriptBodyRenderedItem{lines: viewLines(rendered), selectable: selectable}
			},
		})
		shownAny = true
		previousKind = row.kind
		havePreviousKind = true
	}
	return items
}

func layoutTranscriptBodyItems(items []transcriptBodyItem) transcriptBodyLayout {
	layout := transcriptBodyLayout{}
	for _, item := range items {
		startY := len(layout.lines)
		rendered := renderTranscriptBodyItem(item, startY)
		layout.lines = append(layout.lines, rendered.lines...)
		layout.selectable = append(layout.selectable, rendered.selectable...)
		layout.spans = append(layout.spans, transcriptBodyItemSpan{
			kind:     item.kind,
			rowIndex: item.rowIndex,
			startY:   startY,
			height:   len(rendered.lines),
		})
	}
	return layout
}

func measureTranscriptBodyItems(items []transcriptBodyItem, cache *transcriptBodyHeightCache) transcriptBodyLayout {
	layout := transcriptBodyLayout{}
	startY := 0
	for _, item := range items {
		height := transcriptBodyItemHeight(item, cache)
		layout.spans = append(layout.spans, transcriptBodyItemSpan{
			kind:     item.kind,
			rowIndex: item.rowIndex,
			startY:   startY,
			height:   height,
		})
		startY += height
	}
	return layout
}

func layoutVisibleTranscriptBodyItems(items []transcriptBodyItem, metrics transcriptBodyLayout, window transcriptViewportWindow) transcriptBodyLayout {
	layout := transcriptBodyLayout{spans: append([]transcriptBodyItemSpan(nil), metrics.spans...)}
	if window.end <= window.start {
		return layout
	}
	for index, item := range items {
		if index >= len(metrics.spans) {
			break
		}
		span := metrics.spans[index]
		spanEnd := span.startY + span.height
		if spanEnd <= window.start || span.startY >= window.end {
			continue
		}
		rendered := renderTranscriptBodyItem(item, span.startY)
		localStart := clampInt(window.start-span.startY, 0, len(rendered.lines))
		localEnd := clampInt(window.end-span.startY, localStart, len(rendered.lines))
		layout.lines = append(layout.lines, rendered.lines[localStart:localEnd]...)
		for _, line := range rendered.selectable {
			if line.bodyY >= window.start && line.bodyY < window.end {
				layout.selectable = append(layout.selectable, line)
			}
		}
	}
	return layout
}

func transcriptBodyItemHeight(item transcriptBodyItem, cache *transcriptBodyHeightCache) int {
	if item.heightCacheStable {
		if height, ok := cache.get(item.heightCacheKey); ok {
			return height
		}
	}
	rendered := renderTranscriptBodyItem(item, 0)
	height := len(rendered.lines)
	if item.heightCacheStable {
		cache.set(item.heightCacheKey, height)
	}
	return height
}

func renderTranscriptBodyItem(item transcriptBodyItem, startBodyY int) transcriptBodyRenderedItem {
	if item.render == nil {
		return transcriptBodyRenderedItem{}
	}
	return item.render(startBodyY)
}

func transcriptBlockBodyHeightCacheKey(kind transcriptBodyItemKind, block string) string {
	var b strings.Builder
	appendRenderCacheField(&b, "transcript-body-block-height-v1")
	appendRenderCacheField(&b, strconv.Itoa(int(kind)))
	appendRenderCacheField(&b, block)
	return b.String()
}

func (m model) transcriptRowBodyHeightCacheKey(row transcriptRow, width int, rc rowContext) (string, bool) {
	opts := cardRenderOptions{bodyCap: cardBodyMaxLines, cwd: m.cwd}
	rowKey, stable := m.renderRowCacheKey(row, width, rc, opts, false)
	if rowKey == "" {
		return "", false
	}
	var b strings.Builder
	appendRenderCacheField(&b, "transcript-body-row-height-v1")
	appendRenderCacheField(&b, rowKey)
	return b.String(), stable
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
	case rowSpecialist:
		return m.renderSelectableSpecialistRow(rowIndex, row, width, rc, startBodyY)
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

// renderSelectableSpecialistRow renders a specialist card and marks every line
// as a clickable specialistCard selectable line carrying the childSessionID.
// A left-click or Enter on any card line drills into that specialist's subchat.
func (m model) renderSelectableSpecialistRow(rowIndex int, row transcriptRow, width int, rc rowContext, startBodyY int) (string, []transcriptSelectableLine) {
	rendered := m.renderRow(row, width, rc)
	if rendered == "" || row.specialistInfo == nil {
		return "", nil
	}
	lines := viewLines(rendered)
	selectable := make([]transcriptSelectableLine, len(lines))
	for i := range lines {
		selectable[i] = transcriptSelectableLine{
			bodyY:          startBodyY + i,
			rowIndex:       rowIndex,
			specialistCard: true,
			specialistID:   row.specialistInfo.childSessionID,
		}
	}
	return rendered, selectable
}

func (m model) renderSelectableUserRow(rowIndex int, row transcriptRow, width int, startBodyY int) (string, []transcriptSelectableLine) {
	contentWidth := userPromptContentWidth(width)
	wrapped := wrapPlainText(row.text, maxInt(1, contentWidth))
	selectable := make([]transcriptSelectableLine, 0, len(wrapped))
	for index, line := range wrapped {
		meta := transcriptSelectableLine{
			bodyY:     startBodyY + index + 1,
			rowIndex:  rowIndex,
			textStart: lipgloss.Width(userPromptPrefix),
			text:      line,
		}
		selectable = append(selectable, meta)
	}
	if !m.transcriptSelection.active {
		return m.renderRow(row, width, rowContext{}), selectable
	}
	lines := make([]string, 0, len(wrapped)+2)
	lines = append(lines, renderUserPromptPaddingLine(width))
	for _, meta := range selectable {
		lines = append(lines, renderUserPromptStyledLine(m.renderTranscriptSelectableText(meta, zeroTheme.onUserPrompt(zeroTheme.ink.Bold(true))), contentWidth))
	}
	lines = append(lines, renderUserPromptPaddingLine(width))
	return strings.Join(lines, "\n"), selectable
}

func (m model) renderSelectableAssistantRow(rowIndex int, row transcriptRow, width int, startBodyY int) (string, []transcriptSelectableLine) {
	tableMeasure := width
	wrapped := renderAssistantMarkdownText(row.text, assistantMeasure(width), tableMeasure)
	selectable := make([]transcriptSelectableLine, 0, len(wrapped))
	for index, line := range wrapped {
		plainLine := stripMarkdownRenderControls(line)
		meta := transcriptSelectableLine{
			bodyY:     startBodyY + index,
			rowIndex:  rowIndex,
			textStart: 0,
			text:      plainLine,
		}
		selectable = append(selectable, meta)
	}
	if !m.transcriptSelection.active {
		return m.renderRow(row, width, rowContext{}), selectable
	}
	lines := make([]string, 0, len(wrapped)+1)
	textStyle := zeroTheme.sayText
	if row.final {
		textStyle = zeroTheme.ink
	}
	for index, line := range wrapped {
		meta := selectable[index]
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
	if !m.altScreen || m.height <= 0 || m.setup.visible || m.providerWizard != nil || m.mcpAddWizard != nil || m.mcpManager != nil || m.picker != nil || m.suggestionsActive() {
		return transcriptSelectableLine{}, false
	}
	width := chatWidth(m.width)
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	items := m.transcriptBodyItems(width, "")
	metrics := measureTranscriptBodyItems(items, m.transcriptBodyHeights)
	window := transcriptViewportForLayout(metrics, frame, m.chatScrollOffset).window()
	layout := layoutVisibleTranscriptBodyItems(items, metrics, window)
	_, localY, ok := frame.bodyRect.local(mouseX(msg), mouseY(msg))
	if !ok {
		return transcriptSelectableLine{}, false
	}
	bodyY := window.start + localY
	for _, line := range layout.selectable {
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

func (m model) transcriptViewportStart(body string, width int) (int, int, int) {
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	return transcriptViewportStartForFrame(body, frame, m.chatScrollOffset)
}

func transcriptViewportStartForLayout(layout transcriptBodyLayout, frame transcriptFrameLayout, scrollOffset int) (int, int, int) {
	window := transcriptViewportForLayout(layout, frame, scrollOffset).window()
	return window.start, window.height, frame.bodyRect.y
}

func transcriptViewportStartForFrame(body string, frame transcriptFrameLayout, scrollOffset int) (int, int, int) {
	window := transcriptViewportForBody(body, frame, scrollOffset).window()
	return window.start, window.height, frame.bodyRect.y
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
		if line.permOption {
			// A left-click on a permission-popup option resolves it directly.
			next, cmd := m.resolvePermission(line.permChoice)
			return next.(model), cmd, true
		}
		if line.specialistCard {
			// Click on a specialist card drills into its child session.
			title := m.specialistTitleFor(line.specialistID)
			if errMsg := m.subchat.enter(m.sessionStore, line.specialistID, title, m.chatScrollOffset); errMsg != "" {
				m = m.appendSystemNotice(errMsg)
			}
			m.chatScrollOffset = 0
			return m, nil, true
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
	layout := m.transcriptBodyLayout(width, "")
	parts := []string{}
	for _, line := range layout.selectable {
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
