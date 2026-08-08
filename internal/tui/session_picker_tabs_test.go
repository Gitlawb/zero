package tui

import (
	"strings"
	"testing"
)

func tabbedPicker(items ...pickerItem) *commandPicker {
	picker := &commandPicker{
		kind:     pickerSession,
		items:    append([]pickerItem{}, items...),
		allItems: append([]pickerItem{}, items...),
		tabs:     sessionPickerTabs(items),
	}
	picker.applyQuery()
	return picker
}

func sessionRow(title, agent string) pickerItem {
	return pickerItem{Label: title, Value: title + "-id", Meta: agent, Tab: agent}
}

func TestSessionAgentNameComesFromTheImportTag(t *testing.T) {
	cases := map[string]string{
		"":                     "zero",
		"   ":                  "zero",
		"imported:claude-code": "claude-code",
		"imported:codex":       "codex",
		"imported:factory":     "factory",
		" imported:pi ":        "pi",
		"imported:":            "zero", // malformed tag is not an agent
		"some-other-tag":       "zero",
	}
	for tag, want := range cases {
		if got := sessionAgentName(tag); got != want {
			t.Errorf("sessionAgentName(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestTheStripOnlyAppearsWhenThereIsMoreThanOneSource(t *testing.T) {
	// A strip reading "All | zero" is chrome that tells the user nothing.
	only := tabbedPicker(sessionRow("a", "zero"), sessionRow("b", "zero"))
	if only.hasTabs() {
		t.Errorf("tabs = %v, want none when every session came from one agent", only.tabs)
	}

	mixed := tabbedPicker(sessionRow("a", "zero"), sessionRow("b", "codex"))
	if !mixed.hasTabs() {
		t.Fatal("expected a tab strip once sessions come from two agents")
	}
	if mixed.tabs[0] != pickerTabAll {
		t.Errorf("tabs[0] = %q, want %q first", mixed.tabs[0], pickerTabAll)
	}
}

func TestTheBusiestAgentSitsNearestToAll(t *testing.T) {
	picker := tabbedPicker(
		sessionRow("a", "codex"),
		sessionRow("b", "zero"), sessionRow("c", "zero"), sessionRow("d", "zero"),
		sessionRow("e", "factory"), sessionRow("f", "factory"),
	)
	want := []string{pickerTabAll, "zero", "factory", "codex"}
	if strings.Join(picker.tabs, ",") != strings.Join(want, ",") {
		t.Errorf("tabs = %v, want %v (most sessions first)", picker.tabs, want)
	}
}

func TestAnAgentWithNoSessionsGetsNoTab(t *testing.T) {
	picker := tabbedPicker(sessionRow("a", "zero"), sessionRow("b", "codex"))
	for _, tab := range picker.tabs {
		if tab == "factory" || tab == "pi" {
			t.Errorf("tabs = %v, want no tab for an agent with nothing in it", picker.tabs)
		}
	}
}

// TestAllShowsEverythingAndTabNarrows is the behaviour asked for: All lists
// every session labelled by agent, Tab moves to one agent at a time.
func TestAllShowsEverythingAndTabNarrows(t *testing.T) {
	picker := tabbedPicker(
		sessionRow("zero-one", "zero"), sessionRow("zero-two", "zero"),
		sessionRow("cx", "codex"),
	)
	if picker.activeTabName() != pickerTabAll {
		t.Fatalf("opened on %q, want All", picker.activeTabName())
	}
	if len(picker.items) != 3 {
		t.Fatalf("All shows %d rows, want all 3", len(picker.items))
	}
	// Each row says which agent it came from, so All is readable.
	for _, item := range picker.items {
		if item.Meta == "" {
			t.Errorf("row %q has no agent label", item.Label)
		}
	}

	picker.cycleTab(1)
	if picker.activeTabName() != "zero" {
		t.Fatalf("after one Tab: %q, want zero (the busiest)", picker.activeTabName())
	}
	if len(picker.items) != 2 {
		t.Fatalf("zero tab shows %d rows, want 2", len(picker.items))
	}

	picker.cycleTab(1)
	if picker.activeTabName() != "codex" || len(picker.items) != 1 {
		t.Fatalf("codex tab = %q with %d rows, want codex with 1", picker.activeTabName(), len(picker.items))
	}
	if picker.items[0].Label != "cx" {
		t.Errorf("codex tab shows %q, want the codex session", picker.items[0].Label)
	}

	// And it wraps back to All rather than dead-ending.
	picker.cycleTab(1)
	if picker.activeTabName() != pickerTabAll || len(picker.items) != 3 {
		t.Errorf("cycling past the last tab = %q with %d rows, want All with 3",
			picker.activeTabName(), len(picker.items))
	}
}

// TestTheTabNarrowsAnEmptySearchToo is the regression this design invites:
// filtering only inside the scored branch of applyQuery leaves an empty search
// box showing every tab's rows — precisely when the strip must be trusted.
func TestTheTabNarrowsAnEmptySearchToo(t *testing.T) {
	picker := tabbedPicker(
		sessionRow("alpha", "zero"), sessionRow("beta", "zero"),
		sessionRow("gamma", "codex"),
	)
	selectTab(t, picker, "codex")
	if picker.query != "" {
		t.Fatal("precondition: the search box must be empty")
	}
	if len(picker.items) != 1 || picker.items[0].Label != "gamma" {
		t.Fatalf("empty query on the codex tab = %d rows (%v), want only gamma",
			len(picker.items), pickerLabels(picker.items))
	}
}

func TestSwitchingTabsKeepsTheSearchText(t *testing.T) {
	picker := tabbedPicker(
		sessionRow("parser fix", "zero"),
		sessionRow("parser rewrite", "codex"),
		sessionRow("unrelated", "codex"),
	)
	picker.appendQuery([]rune("parser"))
	if len(picker.items) != 2 {
		t.Fatalf("query across All = %d rows, want 2", len(picker.items))
	}
	selectTab(t, picker, "codex")
	if picker.query != "parser" {
		t.Errorf("query = %q, want it kept across a tab change", picker.query)
	}
	if len(picker.items) != 1 || picker.items[0].Label != "parser rewrite" {
		t.Errorf("codex+query = %v, want only the matching codex session", pickerLabels(picker.items))
	}
}

func TestCyclingBackwardsWraps(t *testing.T) {
	picker := tabbedPicker(sessionRow("a", "zero"), sessionRow("b", "codex"))
	last := picker.tabs[len(picker.tabs)-1]
	picker.cycleTab(-1)
	// Asserted by position, not by agent name: the strip's order depends on how
	// many sessions each agent has, which is not what this test is about.
	if picker.activeTabName() != last {
		t.Errorf("cycling back from All = %q, want the last tab %q", picker.activeTabName(), last)
	}
}

func TestAPickerWithoutTabsIsUnaffected(t *testing.T) {
	// /model, /effort and friends must behave exactly as before.
	plain := &commandPicker{
		kind:     pickerModel,
		items:    []pickerItem{{Label: "a", Tab: "ignored"}, {Label: "b"}},
		allItems: []pickerItem{{Label: "a", Tab: "ignored"}, {Label: "b"}},
	}
	plain.applyQuery()
	if plain.hasTabs() {
		t.Fatal("a picker with no tabs must not claim to have them")
	}
	if len(plain.items) != 2 {
		t.Errorf("got %d rows, want both — Tab values must not filter a tabless picker", len(plain.items))
	}
	plain.cycleTab(1) // must be a no-op, not a panic
	if plain.activeTabName() != pickerTabAll || len(plain.items) != 2 {
		t.Error("cycleTab changed a tabless picker")
	}
}

// selectTab cycles until the named tab is active, so tests state WHICH tab they
// mean instead of depending on the count-based ordering.
func selectTab(t *testing.T, picker *commandPicker, name string) {
	t.Helper()
	for range picker.tabs {
		if picker.activeTabName() == name {
			return
		}
		picker.cycleTab(1)
	}
	t.Fatalf("no %q tab in %v", name, picker.tabs)
}

func pickerLabels(items []pickerItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Label)
	}
	return out
}
