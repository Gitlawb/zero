package tui

import (
	"reflect"
	"sort"
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
		"  /provider - Show the active provider.",
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

func TestCommandDefinitionsExposeStartupChipsInStableOrder(t *testing.T) {
	chips := startupCommandNames()
	metadataChips := startupChipNamesFromDefinitions(t)
	want := []string{"/plan", "/debug", "/tools", "/model", "/provider"}

	if !reflect.DeepEqual(chips, want) {
		t.Fatalf("expected startup chips %v, got %v", want, chips)
	}
	if !reflect.DeepEqual(chips, metadataChips) {
		t.Fatalf("expected startup chips to come from metadata, helper=%v metadata=%v", chips, metadataChips)
	}
	for _, clutter := range []string{"Enter", "Tab", "Ctrl+C", "/clear", "/exit"} {
		if commandTestStringSliceContains(chips, clutter) {
			t.Fatalf("startup chips should stay compact and not contain %q: %v", clutter, chips)
		}
	}
}

func startupChipNamesFromDefinitions(t *testing.T) []string {
	t.Helper()

	definitionType := reflect.TypeOf(commandDefinition{})
	orderField, ok := definitionType.FieldByName("startupOrder")
	if !ok {
		t.Fatal("commandDefinition should expose startupOrder metadata")
	}
	if orderField.Type.Kind() != reflect.Int {
		t.Fatalf("startupOrder should be an int, got %s", orderField.Type)
	}

	type startupChip struct {
		name  string
		order int
	}
	chips := []startupChip{}
	for _, command := range commandDefinitions {
		value := reflect.ValueOf(command).FieldByName("startupOrder")
		if value.Int() > 0 {
			chips = append(chips, startupChip{
				name:  command.name,
				order: int(value.Int()),
			})
		}
	}
	sort.Slice(chips, func(left int, right int) bool {
		if chips[left].order == chips[right].order {
			return chips[left].name < chips[right].name
		}
		return chips[left].order < chips[right].order
	})

	names := make([]string, 0, len(chips))
	for _, chip := range chips {
		names = append(names, chip.name)
	}
	return names
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
