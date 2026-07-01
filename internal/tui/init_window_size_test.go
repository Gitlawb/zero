package tui

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestInitRequestsWindowSize: Bubble Tea documents an initial WindowSizeMsg as
// delivered automatically on program start, but that's the terminal
// proactively pushing a size — if it's ever missed (a slow/unusual terminal,
// a multiplexer, a startup race), nothing else asks again, m.height stays 0
// forever, and transcriptView's `if m.altScreen && m.height > 0` gate falls
// back to the unpadded, non-fullscreen render path for the whole session (the
// alt-screen viewport never gets filled below the actual content). Init must
// explicitly request it too, the same way it already explicitly requests the
// background color rather than relying solely on an unprompted push.
func TestInitRequestsWindowSize(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4"})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() must return a non-nil command")
	}

	want := reflect.ValueOf(tea.RequestWindowSize).Pointer()
	if !batchContainsCmd(t, cmd, want) {
		t.Error("Init() must request the window size (tea.RequestWindowSize), not rely solely on the terminal's unprompted initial WindowSizeMsg")
	}
}

// batchContainsCmd reports whether cmd (Init()'s single top-level
// tea.Batch(...)) contains a sub-command matching want, compared by pointer
// (the same technique TestThemeAutoReProbesBackground uses for
// tea.RequestBackgroundColor — reliable for named top-level functions like
// tea.RequestWindowSize, not closures). Invokes cmd itself once to unpack the
// tea.BatchMsg, but only pointer-compares the sub-commands — never calls
// them, since Init()'s batch also carries real closures (e.g. provider model
// discovery) that make live network calls and must not fire in a unit test.
func batchContainsCmd(t *testing.T, cmd tea.Cmd, want uintptr) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	if reflect.ValueOf(cmd).Pointer() == want {
		return true
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return false
	}
	for _, sub := range batch {
		if sub != nil && reflect.ValueOf(sub).Pointer() == want {
			return true
		}
	}
	return false
}
