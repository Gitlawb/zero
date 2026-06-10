package tui

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Gitlawb/zero/internal/workspaceindex"
)

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
	if m.pendingPermission != nil || m.pendingAskUser != nil || m.pendingSpecReview != nil {
		return false
	}
	return len(m.suggestions) > 0
}

func (m *model) clearSuggestions() {
	m.suggestions = nil
	m.suggestionIdx = 0
	m.suggestionsAreFiles = false
}

// recomputeSuggestions rebuilds the autocomplete match list from the current
// input. It only matches a leading slash token (no spaces yet) so suggestions
// disappear once the user starts typing arguments. Modals suppress matching
// entirely. The selected index is preserved when still in range, otherwise reset.
func (m *model) recomputeSuggestions() {
	if m.pendingPermission != nil || m.pendingAskUser != nil || m.pendingSpecReview != nil {
		m.clearSuggestions()
		return
	}

	value := m.input.Value()

	// File reference: a trailing "@token" (even mid-prompt) drives a workspace
	// file picker. Checked before the slash path so "@" is handled distinctly.
	if token, ok := trailingAtToken(value); ok {
		m.suggestionsAreFiles = true
		m.suggestions = fileSuggestions(m.cwd, token)
		if m.suggestionIdx >= len(m.suggestions) || m.suggestionIdx < 0 {
			m.suggestionIdx = 0
		}
		return
	}
	m.suggestionsAreFiles = false

	trimmed := strings.TrimLeft(value, " ")
	// A leading slash token (no whitespace yet) drives the command palette. A bare
	// "/" now lists the full palette (the footer advertises "/ commands").
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, " \t") {
		m.suggestions = nil
		m.suggestionIdx = 0
		return
	}
	token := strings.TrimSpace(trimmed)
	if token == "" {
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
	chosen := m.suggestions[idx].Name
	if m.suggestionsAreFiles {
		// Replace only the trailing "@token" with the chosen path so any preceding
		// prompt text ("read @foo") is preserved.
		m.input.SetValue(replaceTrailingAtToken(m.input.Value(), chosen) + " ")
	} else {
		m.input.SetValue(chosen + " ")
	}
	m.input.CursorEnd()
	m.suggestions = nil
	m.suggestionIdx = 0
	m.suggestionsAreFiles = false
	return m
}

// trailingAtToken returns the file-reference fragment at the end of value: the
// part AFTER an "@" in the last whitespace-delimited word (empty for a bare "@").
// ok is false when that word does not start with "@".
func trailingAtToken(value string) (string, bool) {
	last := value
	if i := strings.LastIndexAny(value, " \t\n"); i >= 0 {
		last = value[i+1:]
	}
	if !strings.HasPrefix(last, "@") {
		return "", false
	}
	return last[1:], true
}

// replaceTrailingAtToken swaps the trailing "@token" word in value for path.
func replaceTrailingAtToken(value, path string) string {
	if i := strings.LastIndexAny(value, " \t\n"); i >= 0 {
		return value[:i+1] + path
	}
	return path
}

// maxFileWalk bounds how many filesystem entries the "@file" picker visits per
// keystroke so a large workspace tree can't stall the TUI.
const maxFileWalk = 4000

// fileSuggestions lists workspace files whose path matches partial (case-
// insensitive substring), for the "@file" picker. The walk skips VCS/dependency/
// hidden directories and is bounded so it stays responsive per keystroke. Each
// suggestion's Name is the "@<relpath>" token that completion inserts.
func fileSuggestions(cwd, partial string) []commandSuggestion {
	return fileSuggestionsBounded(cwd, partial, maxFileWalk)
}

// fileSuggestionsBounded is fileSuggestions with an explicit walk budget so the
// per-keystroke bound is unit-testable without materializing maxFileWalk entries.
func fileSuggestionsBounded(cwd, partial string, maxVisited int) []commandSuggestion {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	needle := strings.ToLower(strings.TrimSpace(partial))
	out := make([]commandSuggestion, 0, maxCommandSuggestions)
	visited := 0
	_ = filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if visited >= maxVisited || len(out) >= maxCommandSuggestions {
			return fs.SkipAll
		}
		// Count every entry (directories included) so the walk is bounded even in
		// directory-heavy trees where few entries are files; otherwise a deep tree
		// could be traversed in full on each keystroke and stall the TUI.
		visited++
		if d.IsDir() {
			if path != cwd && workspaceindex.ShouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(cwd, path)
		if relErr != nil {
			rel = filepath.Base(path)
		}
		// Emit forward-slash paths on every platform (filepath.Rel uses "\" on
		// Windows) so the inserted "@path" token is portable and matchable.
		rel = filepath.ToSlash(rel)
		if workspaceindex.ShouldSkipFile(rel) {
			return nil
		}
		if needle == "" || strings.Contains(strings.ToLower(rel), needle) {
			out = append(out, commandSuggestion{Name: "@" + rel, Desc: "file"})
		}
		return nil
	})
	return out
}

// dismissSuggestions clears the overlay without touching the input or the run.
func (m model) dismissSuggestions() model {
	m.suggestions = nil
	m.suggestionIdx = 0
	return m
}
