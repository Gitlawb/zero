package tui

import "strings"

func (m model) queueMessage(text string) model {
	text = strings.TrimSpace(text)
	if text == "" {
		return m
	}
	m.queuedMessage = text
	m.rememberInput(text)
	m.clearComposer()
	m.clearSuggestions()
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
