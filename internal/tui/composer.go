package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

type composerState struct {
	text   string
	cursor int
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
	m.input.SetValue("")
}

func (m *model) resetComposerFromInput() {
	m.composer = composerState{}
	m.composerActive = false
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
		m.setComposerState(insertComposerText(state, "\n"))
	case msg.Type == tea.KeyCtrlJ:
		m.setComposerState(insertComposerText(state, "\n"))
	case msg.Type == tea.KeyRunes && msg.Alt && string(msg.Runes) == "d":
		m.setComposerState(deleteComposerWordAfter(state))
	case msg.Type == tea.KeySpace:
		m.setComposerState(insertComposerText(state, " "))
	case msg.Type == tea.KeyRunes && !msg.Alt:
		text := string(msg.Runes)
		if msg.Paste {
			text = sanitizeComposerPaste(text)
		} else {
			text = sanitizeComposerInput(text)
		}
		m.setComposerState(insertComposerText(state, text))
	case msg.Type == tea.KeyLeft || msg.Type == tea.KeyCtrlB:
		state.cursor--
		m.setComposerState(state)
	case msg.Type == tea.KeyRight || msg.Type == tea.KeyCtrlF:
		state.cursor++
		m.setComposerState(state)
	case msg.Type == tea.KeyHome || msg.Type == tea.KeyCtrlA:
		state.cursor = composerLineStart(state)
		m.setComposerState(state)
	case msg.Type == tea.KeyEnd || msg.Type == tea.KeyCtrlE:
		state.cursor = composerLineEnd(state)
		m.setComposerState(state)
	case msg.Type == tea.KeyCtrlU:
		m.setComposerState(deleteComposerLineBefore(state))
	case msg.Type == tea.KeyCtrlK:
		m.setComposerState(deleteComposerLineAfter(state))
	case msg.Type == tea.KeyCtrlW || (msg.Alt && (msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH)):
		m.setComposerState(deleteComposerWordBefore(state))
	case msg.Alt && msg.Type == tea.KeyDelete:
		m.setComposerState(deleteComposerWordAfter(state))
	case msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH:
		m.setComposerState(deleteComposerRange(state, state.cursor-1, state.cursor))
	case msg.Type == tea.KeyDelete || msg.Type == tea.KeyCtrlD:
		m.setComposerState(deleteComposerRange(state, state.cursor, state.cursor+1))
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

func renderComposerState(state composerState, prompt string, width int) string {
	state = normalizeComposerState(state)
	lines := strings.Split(state.text, "\n")
	for index, line := range lines {
		prefix := "  "
		if index == 0 {
			prefix = prompt
		}
		lines[index] = fitStyledLine(prefix+line, width)
	}
	return strings.Join(lines, "\n")
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
