package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestQuestionMarkOpensHelpOnEmptyComposer(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	updated, _ := m.Update(testKeyText("?"))
	next := updated.(model)
	if !next.helpOverlay {
		t.Fatal("? on an empty composer should open the help overlay")
	}
	// The ? must NOT have been typed into the composer.
	if next.composerValue() != "" {
		t.Fatalf("composer should stay empty when ? opens help, got %q", next.composerValue())
	}
}

func TestQuestionMarkTypesIntoNonEmptyComposer(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	m = typeRunes(t, m, "what")
	updated, _ := m.Update(testKeyText("?"))
	next := updated.(model)
	if next.helpOverlay {
		t.Fatal("? after text should type a literal '?', not open help")
	}
	if got := next.composerValue(); got != "what?" {
		t.Fatalf("composer = %q, want %q", got, "what?")
	}
}

func TestHelpOverlayClosesOnQuestionMarkAndEsc(t *testing.T) {
	for _, closer := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"question-mark", testKeyText("?")},
		{"esc", testKey(tea.KeyEsc)},
		{"q", testKeyText("q")},
		{"enter", testKey(tea.KeyEnter)},
	} {
		m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
		m.helpOverlay = true
		updated, _ := m.Update(closer.key)
		next := updated.(model)
		if next.helpOverlay {
			t.Fatalf("%s should close the help overlay", closer.name)
		}
	}
}

func TestHelpOverlaySwallowsOtherKeys(t *testing.T) {
	// While the overlay is open, an ordinary key must not type into the composer.
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	m.helpOverlay = true
	updated, _ := m.Update(testKeyText("x"))
	next := updated.(model)
	if !next.helpOverlay {
		t.Fatal("an ordinary key should not close the overlay")
	}
	if next.composerValue() != "" {
		t.Fatalf("keys must be swallowed while the overlay is open, composer = %q", next.composerValue())
	}
}

func TestHelpOverlayViewRendersGroupsAndKeys(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	m.width = 90
	m.height = 40
	m.helpOverlay = true
	view := plainRender(t, m.View())
	for _, want := range []string{
		"Keyboard Shortcuts",
		"Ctrl+T", "cycle reasoning effort",
		"Shift+Tab", "Ctrl+P", "Ctrl+O",
		"drill into its sub-session",
		keybindingHelpFooter,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("help overlay view missing %q, got:\n%s", want, view)
		}
	}
}

func TestBuildKeybindingGroupsAreWellFormed(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	groups := m.buildKeybindingGroups()
	if len(groups) == 0 {
		t.Fatal("buildKeybindingGroups must not return empty")
	}
	for _, group := range groups {
		if strings.TrimSpace(group.title) == "" {
			t.Fatal("every keybinding group needs a title")
		}
		if len(group.bindings) == 0 {
			t.Fatalf("group %q has no bindings", group.title)
		}
		for _, binding := range group.bindings {
			if strings.TrimSpace(binding.keys) == "" || strings.TrimSpace(binding.desc) == "" {
				t.Fatalf("group %q has a binding with an empty key or description: %+v", group.title, binding)
			}
		}
	}
}

// #419: the `?` help overlay must render ON TOP of the chat (like the model
// picker), not REPLACE the whole screen. The old full-screen replace produced
// only the centered shortcut block on a blank canvas — no title bar, no
// composer. With the overlay open, surrounding chat chrome that is NOT covered
// by the centered box (the model title bar at top, the composer at bottom) must
// still be present alongside "Keyboard Shortcuts".
func TestHelpOverlayCompositesOverChatNotReplacingIt(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	m.width = 100
	m.height = 40
	m.altScreen = true

	base := plainRender(t, m.View()) // no overlay: baseline chrome
	m.helpOverlay = true
	over := plainRender(t, m.View())

	if !strings.Contains(over, "Keyboard Shortcuts") {
		t.Fatalf("help overlay not rendered:\n%s", over)
	}
	// Chrome that renders in the baseline (and sits outside the centered overlay
	// box) must survive behind the overlay. The full-screen replace showed none.
	for _, marker := range []string{"gpt-4o", "describe a task"} {
		if !strings.Contains(base, marker) {
			t.Fatalf("precondition: baseline chat should contain %q:\n%s", marker, base)
		}
		if !strings.Contains(over, marker) {
			t.Fatalf("#419: help replaced the chat instead of overlaying it; %q is gone:\n%s", marker, over)
		}
	}
}

// A populated transcript row also survives behind the overlay (peeking out to
// the left of the centered box), proving the chat body — not just the chrome —
// is composited under the overlay rather than discarded.
func TestHelpOverlayKeepsTranscriptBodyBehindIt(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	m.width = 120
	m.height = 40
	m.altScreen = true
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: "hello there this is a chat line"})
	m.helpOverlay = true

	view := plainRender(t, m.View())
	if !strings.Contains(view, "Keyboard Shortcuts") {
		t.Fatalf("help overlay not rendered:\n%s", view)
	}
	// The start of the transcript line peeks to the left of the centered box.
	if !strings.Contains(view, "hello") {
		t.Fatalf("#419: transcript body was replaced by the help overlay:\n%s", view)
	}
}
