package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMouseClickSelectsThenAppliesCommandSuggestionRow(t *testing.T) {
	m := mouseTestModel()
	m = typeRunes(t, m, "/sp")
	if len(m.suggestions) == 0 {
		t.Fatalf("expected command suggestions, got %#v", m.suggestions)
	}

	width := chatWidth(m.width)
	top := m.overlayMouseTop(len(viewLines(m.suggestionOverlay(width))), width)
	click := tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      width / 2,
		Y:      top + 3,
	}
	updated, cmd := m.Update(click)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("first command click should not return a command")
	}
	if got := next.input.Value(); got != "/sp" {
		t.Fatalf("input after first command click = %q, want /sp", got)
	}
	if !next.suggestionsActive() {
		t.Fatal("suggestions should stay open after first command click")
	}

	updated, cmd = next.Update(click)
	next = updated.(model)
	if cmd != nil {
		t.Fatal("required-argument second command click should not return a command")
	}
	if got := next.input.Value(); got != "/spec" {
		t.Fatalf("input after second command click = %q, want /spec", got)
	}
	if next.suggestionsActive() {
		t.Fatalf("suggestions should close after second command click, got %#v", next.suggestions)
	}
}

func TestMouseClickSelectsThenAppliesPickerRow(t *testing.T) {
	m := mouseTestModel()
	m.modelName = "claude-sonnet-4.5"
	m.picker = &commandPicker{
		kind:  pickerEffort,
		title: "select reasoning effort",
		items: []pickerItem{
			{Label: "auto", Value: "auto"},
			{Label: "high", Value: "high"},
		},
		selected: 0,
	}
	m.picker.allItems = append([]pickerItem{}, m.picker.items...)

	width := chatWidth(m.width)
	top := m.overlayMouseTop(len(viewLines(m.pickerOverlay(width))), width)
	click := tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      width / 2,
		Y:      top + 3,
	}
	updated, cmd := m.Update(click)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("first picker click should not return a command")
	}
	if next.picker == nil || next.picker.selected != 1 {
		t.Fatalf("picker after first click = %#v, want selected index 1", next.picker)
	}
	if next.reasoningEffort != "" {
		t.Fatalf("reasoning effort after first picker click = %q, want unchanged", next.reasoningEffort)
	}

	updated, cmd = next.Update(click)
	next = updated.(model)
	if cmd != nil {
		t.Fatal("second picker click should not return a command")
	}
	if next.picker != nil {
		t.Fatalf("picker should close after second click apply, got %#v", next.picker)
	}
	if next.reasoningEffort != "high" {
		t.Fatalf("reasoning effort after second picker click = %q, want high", next.reasoningEffort)
	}
}

func TestMouseClickSelectsProviderWizardRow(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()
	if m.providerWizard == nil || len(m.providerWizard.providers) < 2 {
		t.Fatalf("expected multiple providers, got %#v", m.providerWizard)
	}

	width := chatWidth(m.width)
	top := m.overlayMouseTop(len(viewLines(m.providerWizardOverlay(width))), width)
	click := tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      width / 2,
		Y:      top + 5,
	}
	updated, cmd := m.Update(click)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("mouse selection should not return a command")
	}
	if next.providerWizard == nil || next.providerWizard.selectedProvider != 1 {
		t.Fatalf("provider selection = %#v, want selected index 1", next.providerWizard)
	}
	if next.providerWizard.step != providerWizardStepProvider {
		t.Fatalf("first provider click should not advance, got step %v", next.providerWizard.step)
	}

	updated, cmd = next.Update(click)
	next = updated.(model)
	if cmd != nil {
		t.Fatal("second provider click should not return a command in this fixture")
	}
	if next.providerWizard == nil || next.providerWizard.step == providerWizardStepProvider {
		t.Fatalf("second provider click should advance, got %#v", next.providerWizard)
	}
}

func TestMouseWheelMovesProviderWizardRows(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()

	updated, cmd := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	next := updated.(model)
	if cmd != nil {
		t.Fatal("mouse wheel should not return a command")
	}
	if next.providerWizard == nil || next.providerWizard.selectedProvider != 1 {
		t.Fatalf("provider selection after wheel = %#v, want selected index 1", next.providerWizard)
	}
}

func mouseTestModel() model {
	m := newModel(context.Background(), Options{})
	m.width = 100
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true
	return m
}
