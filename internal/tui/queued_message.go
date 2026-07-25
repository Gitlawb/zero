package tui

import "strings"

// queuedEditHint replaces the composer placeholder while a message is queued
// and informs the user they can press up to edit it.
const queuedEditHint = "Press up to edit queued messages"

func (m model) queueMessage(text string) model {
	text = strings.TrimSpace(text)
	if text == "" {
		return m
	}
	// A second prompt queued during the same run stacks under the first
	// rather than silently replacing it; both send as one message.
	if m.hasQueuedMessage() {
		text = m.queuedMessage + "\n" + text
	}
	m.queuedMessage = text
	m.rememberInput(text)
	m.clearComposer()
	m.clearSuggestions()
	return m
}

// popQueuedMessageForEdit moves the queued message back into the composer so
// it can be edited before the next turn. Queued text lands above whatever is
// already being typed.
func (m model) popQueuedMessageForEdit() model {
	if !m.hasQueuedMessage() {
		return m
	}
	text := m.queuedMessage
	m.queuedMessage = ""
	if draft := m.composerValue(); strings.TrimSpace(draft) != "" {
		text += "\n" + draft
	}
	// Multiline text must go through the composer state: m.input only holds a
	// flattened display copy (see syncInputFromComposer).
	m.setComposerState(composerState{text: text, cursor: len([]rune(text))})
	m.recomputeSuggestions()
	return m
}

func (m model) clearQueuedMessage() model {
	m.queuedMessage = ""
	return m
}

func (m model) hasQueuedMessage() bool {
	return strings.TrimSpace(m.queuedMessage) != ""
}

func renderQueuedMessagePreview(message string, width int) string {
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return ""
	}
	line := zeroTheme.accent.Render("[queued]") + " " + zeroTheme.muted.Render(message)
	return fitStyledLine(line, width)
}
