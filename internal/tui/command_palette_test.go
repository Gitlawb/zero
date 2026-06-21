package tui

import (
	"context"
	"strings"
	"testing"
)

// TestCtrlKOpensRealCommandPalette: ctrl+k on an empty prompt opens the palette
// populated from ZERO's real slash-command registry (not a fictional list).
func TestCtrlKOpensRealCommandPalette(t *testing.T) {
	m := newModel(context.Background(), Options{})
	updated, _ := m.Update(testKeyCtrl('k'))
	next := updated.(model)

	if !next.commandPaletteOpen {
		t.Fatal("ctrl+k should open the command palette on an empty prompt")
	}
	if len(next.suggestions) == 0 {
		t.Fatal("palette opened with no suggestions")
	}
	names := map[string]bool{}
	for _, s := range next.suggestions {
		names[s.Name] = true
	}
	for _, want := range []string{"/model", "/theme", "/help"} {
		if !names[want] {
			t.Errorf("palette missing real registry command %q", want)
		}
	}
}

// TestCtrlKWithTextDoesNotOpenPalette: ctrl+k while a prompt is being typed keeps
// the composer's kill-to-end behavior rather than hijacking it for the palette.
func TestCtrlKWithTextDoesNotOpenPalette(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.setComposerState(composerState{text: "fix the bug", cursor: 11})
	updated, _ := m.Update(testKeyCtrl('k'))
	next := updated.(model)
	if next.commandPaletteOpen {
		t.Fatal("ctrl+k with text present must not open the palette (kill-to-end instead)")
	}
}

// TestCommandPaletteDispatchesRealCommand: choosing a self-contained command from
// the palette actually runs it (handleSubmit) — proving the palette is wired to
// the real dispatch path, not decorative.
func TestCommandPaletteDispatchesRealCommand(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = m.openCommandPalette()

	idx := -1
	for i, s := range m.suggestions {
		if s.Name == "/help" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("/help not present in the real command palette")
	}
	m.suggestionIdx = idx

	updated, _ := m.chooseSuggestion()
	next := updated.(model)
	if next.commandPaletteOpen {
		t.Fatal("palette should close after dispatching a self-contained command")
	}
	if got := strings.TrimSpace(next.composerValue()); got != "" {
		t.Fatalf("composer should clear after dispatch (command ran, not inserted), got %q", got)
	}
}
