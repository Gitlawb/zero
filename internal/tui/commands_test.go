package tui

import (
	"strings"
	"testing"
)

func TestFormatCommandHelpLinesGroupsCommandsByStableOrder(t *testing.T) {
	lines := formatCommandHelpLines()
	help := strings.Join(lines, "\n")

	groupOrder := []string{"model:", "session:", "runtime:", "tools:", "meta:"}
	lastIndex := -1
	for _, group := range groupOrder {
		index := strings.Index(help, group)
		if index < 0 {
			t.Fatalf("expected grouped help to contain %q, got:\n%s", group, help)
		}
		if index <= lastIndex {
			t.Fatalf("expected %q after previous groups, got:\n%s", group, help)
		}
		lastIndex = index
	}

	for _, want := range []string{
		"model:",
		"  /provider [status] - Open provider setup.",
		"  /model [list|id] - Show or switch the active model.",
		"  /effort [list|low|medium|high|auto] - Show or set reasoning effort for supported models.",
		"session:",
		"  /plan - Show planning mode status.",
		"runtime:",
		"  /permissions - Show the active permission mode and sandbox grants.",
		"  /debug (/debug-mode) - Show debug mode status.",
		"tools:",
		"  /search <query> (/find) - Search local session events. Requires a query argument.",
		"meta:",
		"  /exit (/quit) - Exit Zero.",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected grouped help to contain %q, got:\n%s", want, help)
		}
	}
}

func commandTestStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestParseImageCommand(t *testing.T) {
	cases := []struct {
		input string
		kind  commandKind
		text  string
	}{
		{input: "/image photo.png", kind: commandImage, text: "photo.png"},
		{input: "/image ./a b.png", kind: commandImage, text: "./a b.png"},
		{input: "/image clear", kind: commandImage, text: "clear"},
		{input: "/image", kind: commandImage, text: ""},
	}
	for _, tc := range cases {
		got := parseCommand(tc.input)
		if got.kind != tc.kind || got.text != tc.text {
			t.Fatalf("%q: got kind=%v text=%q, want kind=%v text=%q", tc.input, got.kind, got.text, tc.kind, tc.text)
		}
	}
}

func TestCommandSelectionRequiresInputFromUsage(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "/spec", want: true},
		{name: "/search", want: true},
		{name: "/find", want: true},
		{name: "/image", want: true},
		{name: "/rewind", want: false},
		{name: "/model", want: false},
		{name: "/help", want: false},
	}
	for _, tc := range cases {
		if got := commandSelectionRequiresInput(tc.name); got != tc.want {
			t.Fatalf("commandSelectionRequiresInput(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCommandRequiredInputHintFromUsage(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "/spec", want: "[task]"},
		{name: "/search", want: "[query]"},
		{name: "/find", want: "[query]"},
		{name: "/image", want: "[path]"},
		{name: "/model", want: ""},
	}
	for _, tc := range cases {
		if got := commandRequiredInputHint(tc.name); got != tc.want {
			t.Fatalf("commandRequiredInputHint(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestImageCommandIsDiscoverable(t *testing.T) {
	found := false
	for _, name := range listCommandNames() {
		if name == "/image" {
			found = true
		}
	}
	if !found {
		t.Fatal("/image should be listed so it appears in help and autocomplete")
	}
}
