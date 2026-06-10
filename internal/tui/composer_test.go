package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestComposerInsertNewlineAtCursor(t *testing.T) {
	state := composerState{text: "helloworld", cursor: 5}

	got := insertComposerText(state, "\n")

	if got.text != "hello\nworld" {
		t.Fatalf("text = %q, want %q", got.text, "hello\nworld")
	}
	if got.cursor != len([]rune("hello\n")) {
		t.Fatalf("cursor = %d, want %d", got.cursor, len([]rune("hello\n")))
	}
}

func TestComposerDeleteWordBeforeCursor(t *testing.T) {
	state := composerState{text: "alpha beta  gamma", cursor: len([]rune("alpha beta  gamma"))}

	got := deleteComposerWordBefore(state)

	if got.text != "alpha beta  " {
		t.Fatalf("text = %q, want %q", got.text, "alpha beta  ")
	}
	if got.cursor != len([]rune("alpha beta  ")) {
		t.Fatalf("cursor = %d, want %d", got.cursor, len([]rune("alpha beta  ")))
	}
}

func TestComposerDeleteWordAfterCursor(t *testing.T) {
	state := composerState{text: "alpha  beta gamma", cursor: len([]rune("alpha  "))}

	got := deleteComposerWordAfter(state)

	if got.text != "alpha  gamma" {
		t.Fatalf("text = %q, want %q", got.text, "alpha  gamma")
	}
	if got.cursor != len([]rune("alpha  ")) {
		t.Fatalf("cursor = %d, want %d", got.cursor, len([]rune("alpha  ")))
	}
}

func TestSanitizeComposerPastePreservesNewlines(t *testing.T) {
	got := sanitizeComposerPaste("alpha\tbeta\x00\nsecond\r\nthird\x1b[31m")
	want := "alpha    beta\nsecond\nthird[31m"

	if got != want {
		t.Fatalf("sanitized paste = %q, want %q", got, want)
	}
}

func TestModifiedEnterInsertsNewlineWithoutSubmitting(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "alt enter", key: tea.KeyMsg{Type: tea.KeyEnter, Alt: true}},
		{name: "shift enter", key: tea.KeyMsg{Type: tea.KeyCtrlJ}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(context.Background(), Options{Provider: &fakeProvider{}, ProviderName: "test", ModelName: "test-model"})
			m.input.SetValue("first")
			m.input.CursorEnd()

			updated, cmd := m.Update(tc.key)
			next := updated.(model)

			if cmd != nil {
				t.Fatal("modified Enter should not launch a run")
			}
			if next.pending {
				t.Fatal("modified Enter should leave the model idle")
			}
			if got := next.composerValue(); got != "first\n" {
				t.Fatalf("input = %q, want %q", got, "first\n")
			}
		})
	}
}

func TestMultilineComposerEditingDoesNotFallBackToFlatInput(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.setComposerState(composerState{text: "alpha\nbeta gamma", cursor: len([]rune("alpha\nbeta"))})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	next := updated.(model)
	if got := next.composerValue(); got != "alpha\n gamma" {
		t.Fatalf("ctrl+u composer value = %q, want current line prefix removed", got)
	}
	if !next.composerActive {
		t.Fatal("ctrl+u should keep multiline composer state active")
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	next = updated.(model)
	if got := next.composerValue(); got != "alpha\n" {
		t.Fatalf("ctrl+k composer value = %q, want current line suffix removed", got)
	}
	if !next.composerActive {
		t.Fatal("ctrl+k should keep multiline composer state active")
	}
}

func TestMultilineComposerAcceptsSpaceKey(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.setComposerState(composerState{text: "alpha\nbetagamma", cursor: len([]rune("alpha\nbeta"))})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	next := updated.(model)

	if got := next.composerValue(); got != "alpha\nbeta gamma" {
		t.Fatalf("composer value = %q, want space inserted in multiline state", got)
	}
	if !next.composerActive {
		t.Fatal("space insertion should keep multiline composer state active")
	}
}
