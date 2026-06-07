package tui

import "strings"

// commandSuggestion is one row in the slash-command autocomplete overlay: the
// canonical command name and its short description.
type commandSuggestion struct {
	Name string
	Desc string
}

// maxCommandSuggestions caps how many rows the autocomplete overlay shows so a
// short prefix can't flood the screen.
const maxCommandSuggestions = 8

// suggestionsActive reports whether the autocomplete overlay should drive key
// handling: the input is a slash-command fragment, there is at least one match,
// and no modal (permission / questionnaire) is competing for keys.
func (m model) suggestionsActive() bool {
	if m.pendingPermission != nil || m.pendingAskUser != nil {
		return false
	}
	return len(m.suggestions) > 0
}

// recomputeSuggestions rebuilds the autocomplete match list from the current
// input. It only matches a leading slash token (no spaces yet) so suggestions
// disappear once the user starts typing arguments. Modals suppress matching
// entirely. The selected index is preserved when still in range, otherwise reset.
func (m *model) recomputeSuggestions() {
	if m.pendingPermission != nil || m.pendingAskUser != nil {
		m.suggestions = nil
		m.suggestionIdx = 0
		return
	}

	value := m.input.Value()
	trimmed := strings.TrimLeft(value, " ")
	// Only a single bare "/token" (no whitespace, so no argument started yet)
	// drives suggestions.
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, " \t") {
		m.suggestions = nil
		m.suggestionIdx = 0
		return
	}
	token := strings.TrimSpace(trimmed)
	if token == "" || token == "/" {
		// "/" alone surfaces nothing until at least one more char is typed; this
		// keeps the overlay from popping for an empty slash.
		m.suggestions = nil
		m.suggestionIdx = 0
		return
	}

	matches := matchCommandSuggestions(token)
	m.suggestions = matches
	if m.suggestionIdx >= len(matches) {
		m.suggestionIdx = 0
	}
	if m.suggestionIdx < 0 {
		m.suggestionIdx = 0
	}
}

// matchCommandSuggestions returns commands whose canonical name or any alias has
// the typed prefix (case-insensitive), preserving commandDefinitions order and
// capped at maxCommandSuggestions. A command matched via an alias is still listed
// by its canonical name (completing always inserts the canonical form).
func matchCommandSuggestions(token string) []commandSuggestion {
	prefix := strings.ToLower(strings.TrimSpace(token))
	if prefix == "" {
		return nil
	}
	out := make([]commandSuggestion, 0, maxCommandSuggestions)
	for _, command := range commandDefinitions {
		if !commandHasPrefix(command, prefix) {
			continue
		}
		out = append(out, commandSuggestion{Name: command.name, Desc: command.description})
		if len(out) >= maxCommandSuggestions {
			break
		}
	}
	return out
}

func commandHasPrefix(command commandDefinition, prefix string) bool {
	if strings.HasPrefix(command.name, prefix) {
		return true
	}
	for _, alias := range command.aliases {
		if strings.HasPrefix(alias, prefix) {
			return true
		}
	}
	return false
}

// moveSuggestion advances (delta +1) or rewinds (delta -1) the selected
// suggestion, wrapping at both ends.
func (m *model) moveSuggestion(delta int) {
	n := len(m.suggestions)
	if n == 0 {
		return
	}
	m.suggestionIdx = ((m.suggestionIdx+delta)%n + n) % n
}

// completeSuggestion replaces the input with the selected command name plus a
// trailing space (ready for arguments) and dismisses the overlay.
func (m model) completeSuggestion() model {
	if !m.suggestionsActive() {
		return m
	}
	idx := m.suggestionIdx
	if idx < 0 || idx >= len(m.suggestions) {
		idx = 0
	}
	m.input.SetValue(m.suggestions[idx].Name + " ")
	m.input.CursorEnd()
	m.suggestions = nil
	m.suggestionIdx = 0
	return m
}

// dismissSuggestions clears the overlay without touching the input or the run.
func (m model) dismissSuggestions() model {
	m.suggestions = nil
	m.suggestionIdx = 0
	return m
}
