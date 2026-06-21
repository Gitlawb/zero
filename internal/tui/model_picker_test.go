package tui

import (
	"context"
	"strings"
	"testing"
)

// TestCtrlOOpensModelPicker: ctrl+o opens the BYOK model picker, populated from the
// real model registry/catalog (not a hardcoded list).
func TestCtrlOOpensModelPicker(t *testing.T) {
	m := newModel(context.Background(), Options{})
	updated, _ := m.Update(testKeyCtrl('o'))
	next := updated.(model)

	if next.picker == nil || next.picker.kind != pickerModel {
		t.Fatal("ctrl+o should open the model picker")
	}
	if len(next.picker.items) == 0 {
		t.Fatal("model picker should list real catalog models")
	}
}

// TestModelPickerSelectionRoutesToRealSwitch: choosing a model dispatches through
// the real handleModelCommand path (recording its result) and closes the picker —
// the picker is functional, not cosmetic.
func TestModelPickerSelectionRoutesToRealSwitch(t *testing.T) {
	m := newModel(context.Background(), Options{})
	updated, _ := m.Update(testKeyCtrl('o'))
	m = updated.(model)

	idx := -1
	for i, it := range m.picker.items {
		if strings.TrimSpace(it.Value) != "" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no selectable model in the picker")
	}
	m.picker.selected = idx
	before := len(m.transcript)

	updated, _ = m.choosePicker()
	next := updated.(model)
	if next.picker != nil {
		t.Fatal("picker should close after selection")
	}
	if len(next.transcript) <= before {
		t.Fatal("model selection should route through the real handler and record a result")
	}
}

// TestModelSwitchRefusedWhilePending: the real switch path refuses mid-run, so a
// picker selection cannot swap the model out from under an in-flight turn.
func TestModelSwitchRefusedWhilePending(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.pending = true
	_, text := m.handleModelCommand("gpt-5.5")
	if !strings.Contains(text, "Cannot switch models while a run is active") {
		t.Fatalf("pending-run switch guard missing, got %q", text)
	}
}
