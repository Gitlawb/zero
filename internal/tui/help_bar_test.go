package tui

import (
	"context"
	"strings"
	"testing"
)

// TestHelpBarShowsRealChords: the persistent help bar legend uses the real,
// current key chords.
func TestHelpBarShowsRealChords(t *testing.T) {
	m := newModel(context.Background(), Options{})
	got := plainRender(t, m.helpBar(120))
	for _, want := range []string{"^k", "commands", "^o", "model", "^g", "sidebar", "send", "^c", "quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("help bar missing %q in %q", want, got)
		}
	}
}

// TestFooterShowsPersistentHelpBar: the help bar is shown under the composer when
// no contextual command hint is active.
func TestFooterShowsPersistentHelpBar(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 120
	got := plainRender(t, m.footerView(120))
	if !strings.Contains(got, "commands") || !strings.Contains(got, "sidebar") {
		t.Fatalf("footer should carry the persistent help bar:\n%s", got)
	}
}

// TestKeybindingHelpReflectsReboundChords: the `?` help data documents the real
// post-redesign bindings (ctrl+o model picker, ctrl+r detailed transcript, ctrl+k
// palette, ctrl+g sidebar) — the help stays in sync with the handlers.
func TestKeybindingHelpReflectsReboundChords(t *testing.T) {
	keys := map[string]string{}
	for _, group := range keybindingGroups {
		for _, b := range group.bindings {
			keys[b.keys] = b.desc
		}
	}
	for _, chord := range []string{"Ctrl+O", "Ctrl+R", "Ctrl+K", "Ctrl+G"} {
		if _, ok := keys[chord]; !ok {
			t.Errorf("keybinding help missing %q", chord)
		}
	}
	if d := keys["Ctrl+O"]; !strings.Contains(strings.ToLower(d), "model") {
		t.Errorf("Ctrl+O should describe the model picker, got %q", d)
	}
	if d := keys["Ctrl+R"]; !strings.Contains(strings.ToLower(d), "transcript") {
		t.Errorf("Ctrl+R should toggle the detailed transcript, got %q", d)
	}
}

// TestWorkingLineDrivenByRealVerb: the working/spinner line renders the rotating
// working verb from real state (not a static label).
func TestWorkingLineDrivenByRealVerb(t *testing.T) {
	m := newModel(context.Background(), Options{})
	if m.workingVerb == nil {
		t.Skip("working verb not initialized")
	}
	verb := m.workingVerb.Current()
	if strings.TrimSpace(verb) == "" {
		t.Skip("no working verb configured")
	}
	got := plainRender(t, m.workingStatusLine())
	if !strings.Contains(got, verb) {
		t.Fatalf("working line should show the live verb %q, got %q", verb, got)
	}
}
