package tui

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestThemeAutoReProbesBackground guards the M17 fix at the command-dispatch level
// (not just the handleThemeCommand helper): selecting `/theme auto` must return
// tea.RequestBackgroundColor so the terminal background is re-detected live, while
// a fixed theme must NOT re-probe. A regression that reverts the dispatch to
// `return m, nil` would otherwise pass the whole suite.
func TestThemeAutoReProbesBackground(t *testing.T) {
	// applyTheme mutates global palette state; restore it afterward.
	defer applyTheme(themeDark, true)

	m := newModel(context.Background(), Options{ModelName: "gpt-4"})
	m.input.SetValue("/theme auto")
	_, cmd := m.handleSubmit()
	if cmd == nil {
		t.Fatal("/theme auto must return a non-nil cmd (background re-probe)")
	}
	want := reflect.ValueOf(tea.RequestBackgroundColor).Pointer()
	if reflect.ValueOf(cmd).Pointer() != want {
		t.Error("/theme auto cmd must be tea.RequestBackgroundColor")
	}

	m2 := newModel(context.Background(), Options{ModelName: "gpt-4"})
	m2.input.SetValue("/theme dark")
	if _, cmd2 := m2.handleSubmit(); cmd2 != nil {
		t.Error("/theme dark must not return a background-color request")
	}
}
