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

// padTranscriptBodyLines left-indents transcript body rows by gutter cells (the
// reading-column margin). Horizontal only — it never changes the line count, so
// the width-keyed height cache stays valid. Two-column mode right-pads the chat
// block to the column width in joinColumns; single-column leaves the right blank.
func padTranscriptBodyLines(lines []string, gutter int) []string {
	if gutter <= 0 {
		return lines
	}
	pad := strings.Repeat(" ", gutter)
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return lines
}

// shiftSelectableX bumps each selectable line's textStart by the gutter so
// click-to-select and the highlight stay aligned with the indented glyphs (the
// text now begins gutter cells further right on screen).
func shiftSelectableX(lines []transcriptSelectableLine, gutter int) []transcriptSelectableLine {
	if gutter <= 0 {
		return lines
	}
	for i := range lines {
		lines[i].textStart += gutter
	}
	return lines
}

// finalizeTranscriptBodyRow indents a rendered row by the reading-column gutter,
// shifts its selectable spans to match, and THEN paints the selection highlight —
// so the highlight is computed in the same shifted coordinate the mouse maps to
// and lands exactly where the user selected (instead of gutter cells off).
func (m model) finalizeTranscriptBodyRow(rendered string, selectable []transcriptSelectableLine, gutter int, startBodyY int) transcriptBodyRenderedItem {
	lines := padTranscriptBodyLines(viewLines(rendered), gutter)
	shifted := shiftSelectableX(selectable, gutter)
	if m.transcriptSelection.active {
		lines = viewLines(m.renderRenderedSelection(strings.Join(lines, "\n"), shifted, startBodyY))
	}
	return transcriptBodyRenderedItem{lines: lines, selectable: shifted}
}

func (m model) transcriptBodyItems(width int, emptyOverlay string) []transcriptBodyItem {
	items := []transcriptBodyItem{}
	// Transcript ROWS render in a reading column: wrapped to contentWidth and
	// indented by gutter, so wide terminals don't run text edge-to-edge. Block
	// items (title bar, empty state, prompts) keep the full column width below.
	contentWidth := transcriptContentWidth(width)
	gutter := transcriptGutter(width)

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
			// A "Thought for X" reasoning header opens the next think→act group, so
			// give it a blank above when it follows a tool card. Reasoning interleaves
			// the tool cards (tool → thought → tool …), which breaks the
			// tool-card→tool-card rule above; without this the groups pack into a wall.
			if shownAny && havePreviousKind && row.kind == rowReasoning && isToolCardKind(previousKind) {
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
				summary := renderSpecialistSummary(specialists, m.spinnerGlyph())
				if summary != "" {
					items = append(items, transcriptBlockBodyItem(transcriptBodyItemRow, -1, summary))
					items = append(items, transcriptBlankBodyItem())
				}
			}
			rowIndex, transcriptRow := index, row
			// Key the height cache on contentWidth (the wrap width drives line count);
			// the gutter is a horizontal-only post-pad and never changes height.
			heightCacheKey, heightCacheStable := m.transcriptRowBodyHeightCacheKey(transcriptRow, contentWidth, rc)
			items = append(items, transcriptBodyItem{
				kind:              transcriptBodyItemRow,
				rowIndex:          rowIndex,
				heightCacheKey:    heightCacheKey,
				heightCacheStable: heightCacheStable,
				render: func(startBodyY int) transcriptBodyRenderedItem {
					rendered, selectable := m.renderTranscriptRow(rowIndex, transcriptRow, contentWidth, rc, startBodyY)
					return m.finalizeTranscriptBodyRow(rendered, selectable, gutter, startBodyY)
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
			items = append(items, transcriptBlockBodyItem(transcriptBodyItemPendingPrompt, -1, renderFocusedAskUserPrompt(*m.pendingAskUser, width)))
		default:
			items = append(items, transcriptBodyItem{
				kind:     transcriptBodyItemPendingInterim,
				rowIndex: -1,
				render: func(startBodyY int) transcriptBodyRenderedItem {
					// Streaming text shares the reading column so it doesn't snap
					// width when the turn finalizes into a row.
					return m.finalizeTranscriptBodyRow(m.interimBlock(contentWidth), m.renderSelectableStreamingReasoning(contentWidth, startBodyY), gutter, startBodyY)
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
	contentWidth := transcriptContentWidth(width)
	gutter := transcriptGutter(width)
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
			heightCacheKey: "subchat:" + strconv.Itoa(index) + ":" + strconv.Itoa(contentWidth),
			render: func(startBodyY int) transcriptBodyRenderedItem {
				rendered, selectable := m.renderTranscriptRow(rowIndex, transcriptRow, contentWidth, rc, startBodyY)
				return m.finalizeTranscriptBodyRow(rendered, selectable, gutter, startBodyY)
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
	case rowSystem, rowError, rowToolCall, rowPermission, rowAskUser:
		return m.renderSelectableRenderedRow(rowIndex, row, width, rc, startBodyY)
	case rowToolResult:
		return m.renderSelectableToolResultRow(rowIndex, row, width, rc, startBodyY)
	case rowSpecialist:
		return m.renderSelectableSpecialistRow(rowIndex, row, width, rc, startBodyY)
	default:
		return m.renderRow(row, width, rc), nil
	}
}

// renderSelectableToolResultRow renders the tool result card and marks its head
// (first line) as a clickable collapse/expand toggle. Body/footer text remains
// selectable so copying a visible transcript range includes command output.
func (m model) renderSelectableToolResultRow(rowIndex int, row transcriptRow, width int, rc rowContext, startBodyY int) (string, []transcriptSelectableLine) {
	rendered := m.renderRow(row, width, rc)
	if rendered == "" {
		return "", nil
	}
	// The first rendered line is the clickable toggle header; carry its text so a
	// selection dragged through it copies the label too (the toggle flag still
	// expands/collapses on a direct click, resolved on press before selection).
	allLines := viewLines(rendered)
	header := transcriptSelectableLine{bodyY: startBodyY, rowIndex: rowIndex, toggle: true}
	if len(allLines) > 0 {
		if meta, ok := selectableLineFromRenderedLine(rowIndex, startBodyY, allLines[0], false); ok {
			header.text = meta.text
			header.textStart = meta.textStart
		}
	}
	selectable := []transcriptSelectableLine{header}
	selectable = append(selectable, selectableLinesFromRendered(rowIndex, rendered, startBodyY, 1)...)
	// The selection highlight is painted once, at the body-item level, AFTER the
	// reading-column gutter shift (finalizeTranscriptBodyRow) — in the same shifted
	// coordinate the mouse maps to. Painting it here (unshifted) made the highlight
	// land gutter cells off from where the user clicked.
	return rendered, selectable
}

func (m model) renderSelectableRenderedRow(rowIndex int, row transcriptRow, width int, rc rowContext, startBodyY int) (string, []transcriptSelectableLine) {
	rendered := m.renderRow(row, width, rc)
	if rendered == "" {
		return "", nil
	}
	selectable := selectableLinesFromRendered(rowIndex, rendered, startBodyY, 0)
	// The selection highlight is painted once, at the body-item level, AFTER the
	// reading-column gutter shift (finalizeTranscriptBodyRow) — in the same shifted
	// coordinate the mouse maps to. Painting it here (unshifted) made the highlight
	// land gutter cells off from where the user clicked.
	return rendered, selectable
}

func selectableLinesFromRendered(rowIndex int, rendered string, startBodyY int, skipLeading int) []transcriptSelectableLine {
	lines := viewLines(rendered)
	boxed := renderedLinesHaveBoxBorder(lines)
	selectable := make([]transcriptSelectableLine, 0, len(lines))
	for index, line := range lines {
		if index < skipLeading {
			continue
		}
		meta, ok := selectableLineFromRenderedLine(rowIndex, startBodyY+index, line, boxed)
		if ok {
			selectable = append(selectable, meta)
		}
	}
	return selectable
}

func selectableLineFromRenderedLine(rowIndex int, bodyY int, rendered string, boxed bool) (transcriptSelectableLine, bool) {
	text := ansi.Strip(rendered)
	if isTranscriptStructuralLine(text) {
		return transcriptSelectableLine{}, false
	}

	textStart := 0
	if strings.HasPrefix(text, "│ ") {
		textStart = lipgloss.Width("│ ")
		text = strings.TrimPrefix(text, "│ ")
		if boxed && strings.HasSuffix(text, " │") {
			text = strings.TrimSuffix(text, " │")
		}
	}
	text = strings.TrimRight(text, " ")
	if strings.TrimSpace(text) == "" {
		return transcriptSelectableLine{}, false
	}
	return transcriptSelectableLine{
		bodyY:     bodyY,
		rowIndex:  rowIndex,
		textStart: textStart,
		text:      text,
	}, true
}

func renderedLinesHaveBoxBorder(lines []string) bool {
	hasTop := false
	hasBottom := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(ansi.Strip(line))
		if strings.HasPrefix(trimmed, "╭") && strings.HasSuffix(trimmed, "╮") {
			hasTop = true
		}
		if strings.HasPrefix(trimmed, "╰") && strings.HasSuffix(trimmed, "╯") {
			hasBottom = true
		}
	}
	return hasTop && hasBottom
}

func isTranscriptStructuralLine(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	for _, r := range trimmed {
		switch r {
		case '╭', '╮', '╰', '╯', '┌', '┐', '└', '┘', '─', '━':
			continue
		default:
			return false
		}
	}
	return true
}

func (m model) renderRenderedSelection(rendered string, selectable []transcriptSelectableLine, startBodyY int) string {
	if len(selectable) == 0 {
		return rendered
	}
	byY := make(map[int]transcriptSelectableLine, len(selectable))
	for _, line := range selectable {
		// Toggle headers and specialist cards now carry text, so a selection
		// dragged through them highlights and copies their content. Empty lines and
		// permission buttons (no copyable content) stay excluded.
		if line.text == "" || line.permOption {
			continue
		}
		byY[line.bodyY] = line
	}
	lines := viewLines(rendered)
	for index, line := range lines {
		meta, ok := byY[startBodyY+index]
		if !ok {
			continue
		}
		lines[index] = m.renderTranscriptSelectableStyledLine(meta, line)
	}
	return strings.Join(lines, "\n")
}

func (m model) renderTranscriptSelectableStyledLine(line transcriptSelectableLine, styledLine string) string {
	start, end, ok := m.selectedColumnsForTranscriptLine(line)
	if !ok {
		return styledLine
	}
	absoluteStart := line.textStart + start
	absoluteEnd := line.textStart + end
	lineWidth := ansi.StringWidth(styledLine)
	absoluteStart = clampInt(absoluteStart, 0, lineWidth)
	absoluteEnd = clampInt(absoluteEnd, absoluteStart, lineWidth)
	prefix := ansi.Cut(styledLine, 0, absoluteStart)
	selected := ansi.Strip(ansi.Cut(styledLine, absoluteStart, absoluteEnd))
	suffix := ansi.Cut(styledLine, absoluteEnd, lineWidth)
	return prefix + zeroTheme.selection.Render(selected) + suffix
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
	boxed := renderedLinesHaveBoxBorder(lines)
	selectable := make([]transcriptSelectableLine, len(lines))
	for i := range lines {
		sl := transcriptSelectableLine{
			bodyY:          startBodyY + i,
			rowIndex:       rowIndex,
			specialistCard: true,
			specialistID:   row.specialistInfo.childSessionID,
		}
		// Carry the line's plain text + start so a selection dragged THROUGH the
		// card copies its content; the specialistCard flag still makes a direct
		// click drill into the sub-session (handled on press, before selection).
		if meta, ok := selectableLineFromRenderedLine(rowIndex, startBodyY+i, lines[i], boxed); ok {
			sl.text = meta.text
			sl.textStart = meta.textStart
		}
		selectable[i] = sl
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
	lines := make([]string, 0, len(wrapped)+1)
	lines = append(lines, "")
	for _, meta := range selectable {
		lines = append(lines, renderUserPromptStyledLine(m.renderTranscriptSelectableText(meta, zeroTheme.ink.Bold(true)), contentWidth))
	}
	return strings.Join(lines, "\n"), selectable
}

func (m model) renderSelectableAssistantRow(rowIndex int, row transcriptRow, width int, startBodyY int) (string, []transcriptSelectableLine) {
	tableMeasure := width
	// Interim narration carries a 2-col "● " gutter (see renderAssistantRow), so it
	// wraps 2 cols narrower and its selectable columns start at textStart=gutter;
	// the final answer keeps the full width and textStart=0.
	gutter := lipgloss.Width(narrationPrefix)
	measure := assistantMeasure(width)
	textStart := 0
	if !row.final {
		measure = maxInt(16, assistantMeasure(width)-gutter)
		textStart = gutter
	}
	// Committed/selectable row: highlight (the result is cached per row).
	wrapped := renderAssistantMarkdownText(row.text, measure, tableMeasure, true)
	selectable := make([]transcriptSelectableLine, 0, len(wrapped))
	for index, line := range wrapped {
		plainLine := stripMarkdownRenderControls(line)
		meta := transcriptSelectableLine{
			bodyY:     startBodyY + index,
			rowIndex:  rowIndex,
			textStart: textStart,
			text:      plainLine,
		}
		selectable = append(selectable, meta)
	}
	if !m.transcriptSelection.active {
		return m.renderRow(row, width, rowContext{}), selectable
	}
	lines := make([]string, 0, len(wrapped)+1)
	// Interim narration and the final answer both render bright (ink); only
	// reasoning bodies stay muted. Mirrors renderAssistantRow.
	textStyle := zeroTheme.ink
	for index, line := range wrapped {
		meta := selectable[index]
		rendered := m.renderTranscriptSelectableMarkdownText(meta, line, textStyle)
		if !row.final {
			prefix := "  "
			if index == 0 {
				prefix = zeroTheme.accent.Render("●") + " "
			}
			rendered = fitStyledLine(prefix+rendered, width)
		}
		lines = append(lines, rendered)
	}
	if row.final && row.turnElapsed >= longTurnBookend {
		lines = append(lines, doneLine(row))
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
		// A LIVE reasoning block is capped to ~half the screen so its toggle header
		// stays on-screen. This MUST mirror renderReasoningBlock exactly (same cap,
		// same marker line) so the selectable spans stay line-aligned with the
		// displayed lines that finalizeTranscriptBodyRow highlights.
		bodyOffset := 1
		if running {
			if bodyCap := m.liveReasoningBodyCap(); bodyCap > 0 && len(renderedLines) > bodyCap {
				hidden := len(renderedLines) - bodyCap
				selectable = append(selectable, transcriptSelectableLine{bodyY: startBodyY + 1, rowIndex: rowIndex, textStart: 2})
				lines = append(lines, fitStyledLine("  "+reasoningHiddenMarker(hidden), width))
				renderedLines = renderedLines[hidden:]
				if hidden < len(plainLines) {
					plainLines = plainLines[hidden:]
				} else {
					plainLines = nil
				}
				bodyOffset = 2
			}
		}
		for index, line := range renderedLines {
			plainLine := ""
			if index < len(plainLines) {
				plainLine = plainLines[index]
			}
			meta := transcriptSelectableLine{
				bodyY:     startBodyY + index + bodyOffset,
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
	width := m.chatColumnWidth()
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
		// A click on a clickable AGENTS sidebar row drills into that swarm member's
		// session, reusing the specialist-card subchat path. Checked before the
		// transcript hit-test since the sidebar is outside the chat column.
		if hit, ok := m.sidebarLineAtMouse(msg); ok {
			if errMsg := m.subchat.enter(m.sessionStore, hit.sessionID, hit.title, m.chatScrollOffset); errMsg != "" {
				m = m.appendSystemNotice(errMsg)
			}
			m.chatScrollOffset = 0
			return m, nil, true
		}
		// A click on a PLAN step row drops a transcript card listing the file
		// changes captured while that step was in progress.
		if stepIndex, ok := m.planStepAtMouse(msg); ok {
			var cmd tea.Cmd
			m, cmd = m.openPlanStepDetail(stepIndex)
			return m, cmd, true
		}
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
	width := m.chatColumnWidth()
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
