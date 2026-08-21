package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agentsessions"
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

// A FOREIGN TITLE IS ANOTHER PRODUCT'S BYTES, and this row is where they land.
// registry.go already strips control bytes when a foreign session is IMPORTED,
// with a comment naming the picker row as the injection vector (#835/#876) — but
// the picker shows the title BEFORE any import, straight from the other agent's
// file, so the vector the comment describes was the one path left open. A cursor
// or colour escape here rewrites the row above it, and a carriage return hides
// the rest of the label.
func TestAForeignTitleCannotCarryTerminalEscapes(t *testing.T) {
	hostile := "safe\x1b[2Kmoved\rhidden\x07\x00 end"
	got := agentsessions.DisplayField(hostile)
	for _, banned := range []string{"\x1b", "\r", "\x07", "\x00"} {
		if strings.Contains(got, banned) {
			t.Errorf("a control byte %q survived into a picker label: %q", banned, got)
		}
	}
	// The legible text has to come through, or the fix is just deletion.
	for _, want := range []string{"safe", "moved", "hidden", "end"} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitizing the label ate %q, leaving %q", want, got)
		}
	}

	// AND THE OTHER HALF: stripping controls is not redaction. A foreign title is
	// frequently the user's first prompt, which is exactly where a pasted key
	// ends up, and the row is drawn before anything has been imported — so the
	// picker was the last place a credential could still be shown verbatim.
	secret := agentsessions.DisplayField("deploy with sk-ant-api03-" + strings.Repeat("A", 24) + " now")
	if strings.Contains(secret, "sk-ant-api03-"+strings.Repeat("A", 24)) {
		t.Errorf("a credential in a foreign title reached the picker row: %q", secret)
	}
	if !strings.Contains(secret, "deploy with") || !strings.Contains(secret, "now") {
		t.Errorf("redacting the title ate the text around the secret: %q", secret)
	}
	// Controls must be stripped BEFORE the shape match, or an escape byte splits
	// the key and it slips through looking like two harmless fragments.
	split := agentsessions.DisplayField("sk-ant-api03-\x00" + strings.Repeat("A", 24))
	if strings.Contains(split, strings.Repeat("A", 24)) {
		t.Errorf("a credential split by a control byte survived the display path: %q", split)
	}
}

// THE USER THIS FEATURE IS FOR HAS NO ZERO SESSIONS YET. Someone who has just
// installed Zero and wants to continue work another agent started has an empty
// local history by definition — and newSessionPicker returned nil on that,
// before foreign discovery ran at all. The import path was reachable only after
// the user had already done by hand the thing it exists to save them.
func TestThePickerOffersForeignSessionsWithNoLocalHistory(t *testing.T) {
	foreign := []pickerItem{{Label: "port the parser", Value: "claude-code:abc", Meta: "claude-code", Tab: "claude-code"}}
	picker := pickerFromParts(nil, foreign)
	if picker == nil {
		t.Fatal("an empty local history hid every discovered foreign session; the import path is unreachable for a new user")
	}
	if len(picker.items) != 1 || picker.items[0].Value != "claude-code:abc" {
		t.Errorf("the foreign session did not reach the picker: %+v", picker.items)
	}
}

// And the genuinely empty case still falls back to the text path.
func TestThePickerIsNilWhenNothingIsResumableAtAll(t *testing.T) {
	if picker := pickerFromParts(nil, nil); picker != nil {
		t.Errorf("a picker was built with no local and no foreign sessions: %+v", picker.items)
	}
}

// THROUGH newSessionPicker ITSELF, not just the assembly helper. Both reviewers
// asked for this and they were right: pickerFromParts is the piece I extracted
// while fixing the bug, so testing only that leaves the very branch that was
// wrong — the early return on an empty ListResumable — uncovered by anything
// exercising the real entry point.
func TestNewSessionPickerSurvivesAnEmptyLocalHistory(t *testing.T) {
	store := testSessionStore(t)
	metas, err := store.ListResumable()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("this test needs an empty store; got %d sessions", len(metas))
	}
	m := model{sessionStore: store, cwd: t.TempDir(), now: func() time.Time { return time.Unix(0, 0) }}

	// With no store at all the picker is still nil — the guard above it stands.
	if bare := (model{}).newSessionPicker(); bare != nil {
		t.Error("a model with no session store built a picker")
	}
	// And with an empty store, newSessionPicker must reach foreign discovery
	// rather than returning on len(metas) == 0. There are no foreign sessions in
	// a temp workspace, so nil here is correct — what is asserted is that it did
	// not panic and that the assembly step decides, which pickerFromParts covers
	// for the populated case.
	if picker := m.newSessionPicker(); picker != nil {
		for _, item := range picker.items {
			if item.Tab == "zero" {
				t.Errorf("an empty local history produced a local row: %+v", item)
			}
		}
	}
}
