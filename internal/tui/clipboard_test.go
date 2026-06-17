package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMouseRightPressDetectsRightClick(t *testing.T) {
	if !mouseRightPress(testMouseClick(tea.MouseRight, 10, 5)) {
		t.Fatal("a right-click should be detected as a right press")
	}
	if mouseRightPress(testMouseClick(tea.MouseLeft, 10, 5)) {
		t.Fatal("a left-click must not register as a right press")
	}
}

func TestRightClickReturnsClipboardReadCmd(t *testing.T) {
	// A right-click pastes the clipboard directly (no menu): it returns the async
	// clipboard-read command. Assert at the handleMouse/handleSetupMouse scope
	// where the contract actually lives — not via Update, which batches unrelated
	// commands (ticks, etc.) so a nil-check there passes even if the right-click
	// branch regresses.
	m := mouseTestModel()
	if _, cmd := m.handleMouse(testMouseClick(tea.MouseRight, 40, 10)); cmd == nil {
		t.Fatal("a right-click in the chat should return the clipboard-read command")
	}

	// Provider wizard — also routed through handleMouse.
	w := mouseTestModel()
	w.providerWizard = &providerWizardState{step: providerWizardStepCredential}
	if _, cmd := w.handleMouse(testMouseClick(tea.MouseRight, 40, 10)); cmd == nil {
		t.Fatal("a right-click in the wizard should return the clipboard-read command")
	}

	// Setup mode is routed to handleSetupMouse, which has its own right-click
	// branch; without it setup would swallow the paste.
	s := mouseTestModel()
	if _, cmd := s.handleSetupMouse(testMouseClick(tea.MouseRight, 40, 10)); cmd == nil {
		t.Fatal("a right-click in setup should return the clipboard-read command")
	}
}

func TestClipboardReadRoutesToFocusedField(t *testing.T) {
	// Provider wizard API-key field.
	m := newModel(context.Background(), Options{})
	m.providerWizard = &providerWizardState{step: providerWizardStepCredential}
	updated, _ := m.Update(clipboardReadMsg{content: "sk-from-clipboard"})
	if next := updated.(model); next.providerWizard.apiKey != "sk-from-clipboard" {
		t.Fatalf("clipboard paste should fill the API-key field, got %q", next.providerWizard.apiKey)
	}

	// Composer when no modal field is focused.
	m2 := newModel(context.Background(), Options{})
	updated2, _ := m2.Update(clipboardReadMsg{content: "hello world"})
	if next := updated2.(model); !strings.Contains(next.input.Value(), "hello world") {
		t.Fatalf("clipboard paste should fill the composer, got %q", next.input.Value())
	}
}

func TestClipboardReadErrorShowsStatus(t *testing.T) {
	m := newModel(context.Background(), Options{})
	updated, _ := m.Update(clipboardReadMsg{err: errors.New("no clipboard utility")})
	if next := updated.(model); next.copyStatus != "Paste failed" {
		t.Fatalf("a clipboard-read error should surface a status, got %q", next.copyStatus)
	}
}

func TestClipboardReadEmptyIsNoop(t *testing.T) {
	m := newModel(context.Background(), Options{})
	updated, _ := m.Update(clipboardReadMsg{content: ""})
	if next := updated.(model); next.input.Value() != "" {
		t.Fatalf("an empty clipboard should be a silent no-op, got %q", next.input.Value())
	}
}
