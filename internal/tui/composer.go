package tui

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type composerState struct {
	text   string
	cursor int
}

type composerPastePreview struct {
	active bool
	start  int
	end    int
	label  string
}

func insertComposerText(state composerState, text string) composerState {
	state = normalizeComposerState(state)
	if text == "" {
		return state
	}
	runes := []rune(state.text)
	insert := []rune(text)
	out := make([]rune, 0, len(runes)+len(insert))
	out = append(out, runes[:state.cursor]...)
	out = append(out, insert...)
	out = append(out, runes[state.cursor:]...)
	return composerState{text: string(out), cursor: state.cursor + len(insert)}
}

func deleteComposerWordBefore(state composerState) composerState {
	state = normalizeComposerState(state)
	if state.cursor == 0 {
		return state
	}
	runes := []rune(state.text)
	start := state.cursor
	for start > 0 && unicode.IsSpace(runes[start-1]) {
		start--
	}
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	return deleteComposerRange(state, start, state.cursor)
}

func deleteComposerWordAfter(state composerState) composerState {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	if state.cursor >= len(runes) {
		return state
	}
	end := state.cursor
	for end < len(runes) && unicode.IsSpace(runes[end]) {
		end++
	}
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	for end < len(runes) && runes[end] != '\n' && unicode.IsSpace(runes[end]) {
		end++
	}
	return deleteComposerRange(state, state.cursor, end)
}

func deleteComposerLineBefore(state composerState) composerState {
	state = normalizeComposerState(state)
	return deleteComposerRange(state, composerLineStart(state), state.cursor)
}

func deleteComposerLineAfter(state composerState) composerState {
	state = normalizeComposerState(state)
	return deleteComposerRange(state, state.cursor, composerLineEnd(state))
}

func moveComposerWordBefore(state composerState) composerState {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	pos := state.cursor
	for pos > 0 && unicode.IsSpace(runes[pos-1]) {
		pos--
	}
	for pos > 0 && !unicode.IsSpace(runes[pos-1]) {
		pos--
	}
	state.cursor = pos
	return state
}

func moveComposerWordAfter(state composerState) composerState {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	pos := state.cursor
	for pos < len(runes) && unicode.IsSpace(runes[pos]) {
		pos++
	}
	for pos < len(runes) && !unicode.IsSpace(runes[pos]) {
		pos++
	}
	state.cursor = pos
	return state
}

func sanitizeComposerPaste(text string) string {
	var out strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '\r':
			out.WriteRune('\n')
			if i+1 < len(runes) && runes[i+1] == '\n' {
				i++
			}
		case '\n':
			out.WriteRune('\n')
		case '\t':
			out.WriteString("    ")
		default:
			if !unicode.IsControl(r) {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

func sanitizeComposerInput(text string) string {
	return sanitizeComposerPaste(strings.ReplaceAll(text, "\n", ""))
}

func (m model) composerValue() string {
	if m.composerActive {
		return m.composer.text
	}
	return m.input.Value()
}

func (m model) currentComposerState() composerState {
	if m.composerActive {
		return normalizeComposerState(m.composer)
	}
	return composerState{text: m.input.Value(), cursor: m.input.Position()}
}

func (m *model) setComposerState(state composerState) {
	m.composer = normalizeComposerState(state)
	m.composerActive = true
	m.syncInputFromComposer()
}

func (m *model) clearComposer() {
	m.composer = composerState{}
	m.composerActive = false
	m.composerPastePreviews = nil
	m.input.SetValue("")
}

func (m *model) resetComposerFromInput() {
	m.composer = composerState{}
	m.composerActive = false
	m.composerPastePreviews = nil
}

func (m *model) syncInputFromComposer() {
	display := strings.ReplaceAll(m.composer.text, "\n", " ")
	m.input.SetValue(display)
	m.input.SetCursor(composerDisplayCursor(m.composer))
}

func composerDisplayCursor(state composerState) int {
	state = normalizeComposerState(state)
	count := 0
	for range []rune(state.text)[:state.cursor] {
		count++
	}
	return count
}

func (m model) applyComposerKey(msg tea.KeyMsg) (model, bool) {
	state := m.currentComposerState()
	switch {
	case msg.Type == tea.KeyEnter && msg.Alt:
		m = m.insertComposerTextWithPastePreview(state, "\n", "")
	case msg.Type == tea.KeyCtrlJ:
		m = m.insertComposerTextWithPastePreview(state, "\n", "")
	case msg.Type == tea.KeyRunes && msg.Alt && string(msg.Runes) == "d":
		end := deleteComposerWordAfter(state).cursor
		nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, state.cursor, end)
		m.setComposerState(nextState)
		m.composerPastePreviews = nextPreviews
	case (msg.Type == tea.KeyLeft || msg.Type == tea.KeyCtrlLeft) && msg.Alt:
		m.setComposerState(moveComposerWordBefore(state))
	case (msg.Type == tea.KeyRight || msg.Type == tea.KeyCtrlRight) && msg.Alt:
		m.setComposerState(moveComposerWordAfter(state))
	case msg.Type == tea.KeyCtrlLeft:
		m.setComposerState(moveComposerWordBefore(state))
	case msg.Type == tea.KeyCtrlRight:
		m.setComposerState(moveComposerWordAfter(state))
	case msg.Type == tea.KeyRunes && msg.Alt && string(msg.Runes) == "b":
		m.setComposerState(moveComposerWordBefore(state))
	case msg.Type == tea.KeyRunes && msg.Alt && string(msg.Runes) == "f":
		m.setComposerState(moveComposerWordAfter(state))
	case msg.Type == tea.KeySpace:
		m = m.insertComposerTextWithPastePreview(state, " ", "")
	case msg.Type == tea.KeyRunes && !msg.Alt:
		text := string(msg.Runes)
		previewLabel := ""
		if msg.Paste {
			text = sanitizeComposerPaste(text)
			previewLabel, _ = composerPastePreviewLabel(text, m.composerPastePreviewWrapWidth())
		} else {
			text = sanitizeComposerInput(text)
		}
		if shouldInsertCommandArgumentSpace(state, text) {
			text = " " + text
			if previewLabel != "" {
				previewLabel = " " + previewLabel
			}
		}
		m = m.insertComposerTextWithPastePreview(state, text, previewLabel)
	case msg.Type == tea.KeyLeft || msg.Type == tea.KeyCtrlB:
		if nextState, ok := moveComposerPastePreviewBoundary(state, m.composerPastePreviews, -1); ok {
			m.setComposerState(nextState)
			break
		}
		state.cursor--
		m.setComposerState(state)
	case msg.Type == tea.KeyRight || msg.Type == tea.KeyCtrlF:
		if nextState, ok := moveComposerPastePreviewBoundary(state, m.composerPastePreviews, 1); ok {
			m.setComposerState(nextState)
			break
		}
		state.cursor++
		m.setComposerState(state)
	case msg.Type == tea.KeyHome || msg.Type == tea.KeyCtrlA:
		state.cursor = composerLineStart(state)
		m.setComposerState(state)
	case msg.Type == tea.KeyEnd || msg.Type == tea.KeyCtrlE:
		state.cursor = composerLineEnd(state)
		m.setComposerState(state)
	case msg.Type == tea.KeyCtrlU:
		nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, composerLineStart(state), state.cursor)
		m.setComposerState(nextState)
		m.composerPastePreviews = nextPreviews
	case msg.Type == tea.KeyCtrlK:
		nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, state.cursor, composerLineEnd(state))
		m.setComposerState(nextState)
		m.composerPastePreviews = nextPreviews
	case msg.Type == tea.KeyCtrlW || (msg.Alt && (msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH)):
		start := deleteComposerWordBefore(state).cursor
		nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, start, state.cursor)
		m.setComposerState(nextState)
		m.composerPastePreviews = nextPreviews
	case msg.Alt && msg.Type == tea.KeyDelete:
		end := deleteComposerWordAfter(state).cursor
		nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, state.cursor, end)
		m.setComposerState(nextState)
		m.composerPastePreviews = nextPreviews
	case msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH:
		if nextState, nextPreviews, ok := deleteComposerPastePreviewBefore(state, m.composerPastePreviews); ok && !m.suggestionsActive() {
			m.setComposerState(nextState)
			m.composerPastePreviews = nextPreviews
		} else if start, end, ok := completedFileMentionRangeBefore(state); ok && !m.suggestionsActive() {
			nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, start, end)
			m.setComposerState(nextState)
			m.composerPastePreviews = nextPreviews
		} else {
			nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, state.cursor-1, state.cursor)
			m.setComposerState(nextState)
			m.composerPastePreviews = nextPreviews
		}
	case msg.Type == tea.KeyDelete || msg.Type == tea.KeyCtrlD:
		if nextState, nextPreviews, ok := deleteComposerPastePreviewAfter(state, m.composerPastePreviews); ok {
			m.setComposerState(nextState)
			m.composerPastePreviews = nextPreviews
		} else {
			nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, state.cursor, state.cursor+1)
			m.setComposerState(nextState)
			m.composerPastePreviews = nextPreviews
		}
	default:
		return m, false
	}

	if strings.Contains(m.composer.text, "\n") {
		m.clearSuggestions()
	} else {
		m.recomputeSuggestions()
	}
	return m, true
}

func (m model) insertComposerTextWithPastePreview(state composerState, text string, previewLabel string) model {
	state = normalizeComposerState(state)
	insertStart := state.cursor
	insertedRunes := len([]rune(text))
	nextPreviews := composerPastePreviewsAfterInsert(m.composerPastePreviews, insertStart, insertedRunes)
	m.setComposerState(insertComposerText(state, text))
	if previewLabel != "" && insertedRunes > 0 {
		previewLabel = composerPastePreviewLabelWithIndex(previewLabel, len(nextPreviews)+1)
		nextPreviews = append(nextPreviews, composerPastePreview{
			active: true,
			start:  insertStart,
			end:    insertStart + insertedRunes,
			label:  previewLabel,
		})
		sortComposerPastePreviews(nextPreviews)
		m.composerPastePreviews = nextPreviews
		return m
	}
	m.composerPastePreviews = nextPreviews
	return m
}

func (m model) replaceComposerRangeWithPastePreviews(state composerState, start int, end int, replacement string) model {
	nextState, nextPreviews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, start, end)
	m.setComposerState(nextState)
	m.composerPastePreviews = nextPreviews
	if replacement == "" {
		return m
	}
	return m.insertComposerTextWithPastePreview(m.currentComposerState(), replacement, "")
}

func (m model) composerPastePreviewWrapWidth() int {
	width := chatWidth(m.width)
	if width < 8 {
		width = defaultStartupWidth
	}
	return maxInt(1, width-4-lipgloss.Width(m.input.Prompt))
}

func composerPastePreviewLabel(text string, wrapWidth int) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	runeCount := len([]rune(text))
	lineCount := composerPastePreviewVisualLineCount(text, wrapWidth)
	if lineCount < 3 && runeCount < 220 {
		return "", false
	}
	snippet := text
	if newline := strings.IndexRune(snippet, '\n'); newline >= 0 {
		snippet = snippet[:newline]
	}
	snippet = strings.Join(strings.Fields(snippet), " ")
	if snippet == "" {
		snippet = "pasted text"
	}
	snippet = truncateComposerPasteSnippet(snippet, 48)
	label := "lines"
	if lineCount == 1 {
		label = "line"
	}
	return "[" + snippet + " · " + strconv.Itoa(lineCount) + " " + label + "]", true
}

func composerPastePreviewLabelWithIndex(label string, pasteNumber int) string {
	if pasteNumber <= 1 || !strings.HasSuffix(label, "]") {
		return label
	}
	return strings.TrimSuffix(label, "]") + ", paste " + strconv.Itoa(pasteNumber) + "]"
}

func composerPastePreviewVisualLineCount(text string, wrapWidth int) int {
	wrapWidth = maxInt(1, wrapWidth)
	total := 0
	for _, line := range strings.Split(text, "\n") {
		width := lipgloss.Width(line)
		total += maxInt(1, (width+wrapWidth-1)/wrapWidth)
	}
	return total
}

func truncateComposerPasteSnippet(text string, limit int) string {
	runes := []rune(text)
	if limit <= 3 || len(runes) <= limit {
		return text
	}
	return string(runes[:limit-3]) + "..."
}

func composerPastePreviewsAfterInsert(previews []composerPastePreview, pos int, length int) []composerPastePreview {
	if length <= 0 || len(previews) == 0 {
		return previews
	}
	next := make([]composerPastePreview, 0, len(previews))
	for _, preview := range previews {
		if !preview.active {
			continue
		}
		switch {
		case pos <= preview.start:
			preview.start += length
			preview.end += length
		case pos < preview.end:
			continue
		}
		next = append(next, preview)
	}
	sortComposerPastePreviews(next)
	return next
}

func moveComposerPastePreviewBoundary(state composerState, previews []composerPastePreview, direction int) (composerState, bool) {
	state = normalizeComposerState(state)
	for _, preview := range validComposerPastePreviews(state, previews) {
		switch {
		case direction < 0 && state.cursor > preview.start && state.cursor <= preview.end:
			state.cursor = preview.start
			return state, true
		case direction > 0 && state.cursor >= preview.start && state.cursor < preview.end:
			state.cursor = preview.end
			return state, true
		}
	}
	return state, false
}

func deleteComposerPastePreviewBefore(state composerState, previews []composerPastePreview) (composerState, []composerPastePreview, bool) {
	state = normalizeComposerState(state)
	valid := validComposerPastePreviews(state, previews)
	for index, preview := range valid {
		if state.cursor != preview.end {
			continue
		}
		return deleteComposerRange(state, preview.start, preview.end), composerPastePreviewsAfterDelete(valid, index), true
	}
	return state, previews, false
}

func deleteComposerPastePreviewAfter(state composerState, previews []composerPastePreview) (composerState, []composerPastePreview, bool) {
	state = normalizeComposerState(state)
	valid := validComposerPastePreviews(state, previews)
	for index, preview := range valid {
		if state.cursor != preview.start {
			continue
		}
		return deleteComposerRange(state, preview.start, preview.end), composerPastePreviewsAfterDelete(valid, index), true
	}
	return state, previews, false
}

func deleteComposerRangeWithPastePreviews(state composerState, previews []composerPastePreview, start int, end int) (composerState, []composerPastePreview) {
	state = normalizeComposerState(state)
	runeCount := len([]rune(state.text))
	start = clamp(start, 0, runeCount)
	end = clamp(end, 0, runeCount)
	if end < start {
		start, end = end, start
	}
	if start == end {
		return state, validComposerPastePreviews(state, previews)
	}

	valid := validComposerPastePreviews(state, previews)
	deleteStart, deleteEnd := start, end
	for {
		expanded := false
		for _, preview := range valid {
			if !composerRangesOverlap(deleteStart, deleteEnd, preview.start, preview.end) {
				continue
			}
			if preview.start < deleteStart {
				deleteStart = preview.start
				expanded = true
			}
			if preview.end > deleteEnd {
				deleteEnd = preview.end
				expanded = true
			}
		}
		if !expanded {
			break
		}
	}

	nextState := deleteComposerRange(state, deleteStart, deleteEnd)
	delta := deleteEnd - deleteStart
	nextPreviews := make([]composerPastePreview, 0, len(valid))
	for _, preview := range valid {
		switch {
		case composerRangesOverlap(deleteStart, deleteEnd, preview.start, preview.end):
			continue
		case preview.start >= deleteEnd:
			preview.start -= delta
			preview.end -= delta
		}
		nextPreviews = append(nextPreviews, preview)
	}
	sortComposerPastePreviews(nextPreviews)
	return nextState, nextPreviews
}

func composerRangesOverlap(leftStart int, leftEnd int, rightStart int, rightEnd int) bool {
	return leftStart < rightEnd && rightStart < leftEnd
}

func validComposerPastePreviews(state composerState, previews []composerPastePreview) []composerPastePreview {
	state = normalizeComposerState(state)
	if len(previews) == 0 {
		return nil
	}
	runeCount := len([]rune(state.text))
	valid := make([]composerPastePreview, 0, len(previews))
	for _, preview := range previews {
		if !preview.active || preview.label == "" || preview.start < 0 || preview.start >= preview.end || preview.end > runeCount {
			continue
		}
		valid = append(valid, preview)
	}
	sortComposerPastePreviews(valid)
	out := valid[:0]
	lastEnd := -1
	for _, preview := range valid {
		if preview.start < lastEnd {
			continue
		}
		out = append(out, preview)
		lastEnd = preview.end
	}
	return out
}

func sortComposerPastePreviews(previews []composerPastePreview) {
	sort.SliceStable(previews, func(i, j int) bool {
		if previews[i].start == previews[j].start {
			return previews[i].end < previews[j].end
		}
		return previews[i].start < previews[j].start
	})
}

func composerPastePreviewsAfterDelete(previews []composerPastePreview, deleteIndex int) []composerPastePreview {
	if deleteIndex < 0 || deleteIndex >= len(previews) {
		return previews
	}
	deleted := previews[deleteIndex]
	delta := deleted.end - deleted.start
	next := make([]composerPastePreview, 0, len(previews)-1)
	for index, preview := range previews {
		if index == deleteIndex {
			continue
		}
		if preview.start >= deleted.end {
			preview.start -= delta
			preview.end -= delta
		}
		next = append(next, preview)
	}
	sortComposerPastePreviews(next)
	return next
}

func shouldInsertCommandArgumentSpace(state composerState, text string) bool {
	if text == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(text)
	if unicode.IsSpace(first) {
		return false
	}
	state = normalizeComposerState(state)
	if state.cursor != len([]rune(state.text)) {
		return false
	}
	if strings.TrimRightFunc(state.text, unicode.IsSpace) != state.text {
		return false
	}
	return commandArgumentHintForInput(state.text) != ""
}

func deleteCompletedFileMentionBefore(state composerState) (composerState, bool) {
	start, end, ok := completedFileMentionRangeBefore(state)
	if !ok {
		return state, false
	}
	return deleteComposerRange(state, start, end), true
}

func completedFileMentionRangeBefore(state composerState) (int, int, bool) {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	if state.cursor <= 0 || state.cursor > len(runes) || !isPathQueryBoundary(runes[state.cursor-1]) {
		return 0, 0, false
	}
	tokenEnd := state.cursor
	for tokenEnd > 0 && isPathQueryBoundary(runes[tokenEnd-1]) {
		tokenEnd--
	}
	tokenStart := tokenEnd
	for tokenStart > 0 && !isPathQueryBoundary(runes[tokenStart-1]) {
		tokenStart--
	}
	if tokenStart >= tokenEnd || runes[tokenStart] != '@' || tokenEnd-tokenStart <= 1 {
		return 0, 0, false
	}
	return tokenStart, state.cursor, true
}

func deleteComposerRange(state composerState, start int, end int) composerState {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	start = clamp(start, 0, len(runes))
	end = clamp(end, 0, len(runes))
	if end < start {
		start, end = end, start
	}
	if start == end {
		return state
	}
	out := make([]rune, 0, len(runes)-(end-start))
	out = append(out, runes[:start]...)
	out = append(out, runes[end:]...)
	return composerState{text: string(out), cursor: start}
}

func normalizeComposerState(state composerState) composerState {
	runes := []rune(state.text)
	state.cursor = clamp(state.cursor, 0, len(runes))
	return state
}

func composerLineStart(state composerState) int {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	pos := state.cursor
	for pos > 0 && runes[pos-1] != '\n' {
		pos--
	}
	return pos
}

func composerLineEnd(state composerState) int {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	pos := state.cursor
	for pos < len(runes) && runes[pos] != '\n' {
		pos++
	}
	return pos
}
