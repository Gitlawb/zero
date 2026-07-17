package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

func TestResolveLeaderConfigDefaults(t *testing.T) {
	file := config.DefaultKeybindingsFile(PrimarySlashNames())
	got := resolveLeaderConfig(file, keyBindings{})
	if got.key.Label() != "Ctrl+X" {
		t.Fatalf("leader key = %q", got.key.Label())
	}
	if got.commands['m'] != "/model" || got.commands['p'] != "/provider" {
		t.Fatalf("default map incomplete: %#v", got.commands)
	}
	if len(got.notices) != 0 {
		t.Fatalf("unexpected notices: %v", got.notices)
	}
}

func TestResolveLeaderConfigUnassignAndRemap(t *testing.T) {
	file := config.KeybindingsFile{
		Leader: map[string]string{
			"/model": "", // free the letter before reassigning
			"/theme": "m",
			"/clear": "",
		},
	}
	// Merge onto defaults the way ResolveKeybindings would for a partial overlay.
	merged := config.ResolveKeybindings(config.DefaultKeybindingsFile(PrimarySlashNames()), file, config.KeybindingsFile{})
	got := resolveLeaderConfig(merged, keyBindings{})
	if got.commands['m'] != "/theme" {
		t.Fatalf("m = %q, want /theme", got.commands['m'])
	}
	if _, ok := got.commands['c']; ok {
		t.Fatal("/clear should be unbound")
	}
}

func TestResolveLeaderConfigRejectsReservedLeaderKey(t *testing.T) {
	for _, key := range []string{"ctrl+p", "ctrl+n", "ctrl+g", "esc", "x"} {
		got := resolveLeaderConfig(config.KeybindingsFile{LeaderKey: key}, keyBindings{})
		if got.key.Label() != "Ctrl+X" {
			t.Fatalf("leaderKey %q should fall back to Ctrl+X, got %s", key, got.key.Label())
		}
		if len(got.notices) == 0 {
			t.Fatalf("leaderKey %q should produce a notice", key)
		}
	}
}

func TestResolveLeaderConfigRejectsEditAndUnknown(t *testing.T) {
	file := config.KeybindingsFile{
		Leader: map[string]string{
			"/edit":    "e",
			"/notreal": "z",
		},
	}
	merged := config.ResolveKeybindings(config.DefaultKeybindingsFile(PrimarySlashNames()), file, config.KeybindingsFile{})
	got := resolveLeaderConfig(merged, keyBindings{})
	if _, ok := got.commands['e']; ok {
		t.Fatal("/edit must not be bound")
	}
	if _, ok := got.commands['z']; ok {
		t.Fatal("unknown slash must not be bound")
	}
	if len(got.notices) < 2 {
		t.Fatalf("want notices for /edit and unknown, got %v", got.notices)
	}
}

func TestResolveLeaderConfigDuplicateLetters(t *testing.T) {
	file := config.KeybindingsFile{
		LeaderKey: "ctrl+x",
		Leader: map[string]string{
			"/model":    "m",
			"/theme":    "m",
			"/help":     "",
			"/provider": "p",
		},
	}
	got := resolveLeaderConfig(file, keyBindings{})
	// sort.Strings: /model before /theme, so /model wins.
	if got.commands['m'] != "/model" {
		t.Fatalf("duplicate should keep /model, got %q (notices=%v)", got.commands['m'], got.notices)
	}
	foundDupNotice := false
	for _, n := range got.notices {
		if strings.Contains(n, "both use letter") {
			foundDupNotice = true
		}
	}
	if !foundDupNotice {
		t.Fatalf("want duplicate-letter notice, got %v", got.notices)
	}
}

func TestCustomLeaderKeyArmsAndCancels(t *testing.T) {
	m := newModel(context.Background(), Options{
		ModelName: "gpt-4o",
		KeybindingsFile: config.KeybindingsFile{
			LeaderKey: "alt+x",
			Leader:    config.DefaultLeaderAssignments(),
		},
	})
	if m.leaderKeyLabel() != "Alt+X" {
		t.Fatalf("label = %q", m.leaderKeyLabel())
	}
	updated, _ := m.Update(testKeyAlt('x'))
	next := updated.(model)
	if !next.leaderPending {
		t.Fatal("Alt+X should arm leader")
	}
	updated, _ = next.Update(testKeyAlt('x'))
	next = updated.(model)
	if next.leaderPending {
		t.Fatal("second Alt+X should cancel")
	}
	// Default Ctrl+X must not arm when remapped.
	m = newModel(context.Background(), Options{
		ModelName: "gpt-4o",
		KeybindingsFile: config.KeybindingsFile{
			LeaderKey: "alt+x",
			Leader:    config.DefaultLeaderAssignments(),
		},
	})
	updated, _ = m.Update(testKeyCtrl('x'))
	next = updated.(model)
	if next.leaderPending {
		t.Fatal("Ctrl+X must not arm when leader is Alt+X")
	}
}

func TestPrimarySlashNamesCoversBuiltins(t *testing.T) {
	names := PrimarySlashNames()
	if len(names) < 30 {
		t.Fatalf("expected full catalog, got %d", len(names))
	}
	seen := map[string]bool{}
	for _, n := range names {
		if !strings.HasPrefix(n, "/") {
			t.Fatalf("bad name %q", n)
		}
		if seen[n] {
			t.Fatalf("duplicate %q", n)
		}
		seen[n] = true
	}
	for slash := range config.DefaultLeaderAssignments() {
		if !seen[slash] {
			t.Fatalf("leader default %s missing from PrimarySlashNames", slash)
		}
	}
}
