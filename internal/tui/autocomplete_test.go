package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
)

// typeRunes feeds each rune of s through Update as an individual key press,
// exercising the same recompute-after-input path the real loop uses.
func typeRunes(t *testing.T, m model, s string) model {
	t.Helper()
	for _, r := range s {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}
	return m
}

func suggestionNames(m model) []string {
	names := make([]string, 0, len(m.suggestions))
	for _, s := range m.suggestions {
		names = append(names, s.Name)
	}
	return names
}

func contains(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func TestSuggestionsSurfaceMatchingCommands(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/mo")

	if !m.suggestionsActive() {
		t.Fatal("expected suggestions active after typing /mo")
	}
	names := suggestionNames(m)
	if !contains(names, "/model") || !contains(names, "/mode") {
		t.Fatalf("expected /model and /mode in suggestions, got %v", names)
	}
}

func TestSuggestionsMatchAliasButListCanonical(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/find") // alias of /search

	names := suggestionNames(m)
	if !contains(names, "/search") {
		t.Fatalf("expected alias /find to surface canonical /search, got %v", names)
	}
}

func TestSuggestionsInactiveWithoutSlashOrToken(t *testing.T) {
	m := newModel(context.Background(), Options{})

	m1 := typeRunes(t, m, "hello")
	if m1.suggestionsActive() {
		t.Fatal("plain text should not surface suggestions")
	}

	// A slash followed by a space (an argument has started) drops suggestions.
	m2 := typeRunes(t, m, "/model ")
	if m2.suggestionsActive() {
		t.Fatal("suggestions should clear once an argument is typed")
	}
}

func TestTabCyclesSuggestions(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/mo")
	start := m.suggestionIdx

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(model)
	if m.suggestionIdx == start {
		t.Fatal("Tab should advance the selected suggestion")
	}

	// Tab past the end wraps to 0.
	for i := 0; i < len(m.suggestions); i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(model)
	}
	if m.suggestionIdx != m.suggestionIdx%len(m.suggestions) {
		t.Fatal("selection index out of range after cycling")
	}
}

func TestUpDownMoveSuggestions(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/mo")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.suggestionIdx != 1 {
		t.Fatalf("Down should select index 1, got %d", m.suggestionIdx)
	}
	// Up from index 0 wraps to the last suggestion.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	if m.suggestionIdx != len(m.suggestions)-1 {
		t.Fatalf("Up past the top should wrap to last (%d), got %d", len(m.suggestions)-1, m.suggestionIdx)
	}
}

func TestEnterCompletesSuggestion(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/mod") // selects /model first

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if cmd != nil {
		t.Fatal("Enter on a suggestion should complete, not submit a run")
	}
	if got := m.input.Value(); got != "/model " {
		t.Fatalf("expected input completed to %q, got %q", "/model ", got)
	}
	if m.suggestionsActive() {
		t.Fatal("completing a suggestion should dismiss the overlay")
	}
}

func TestTabCompletesAfterSelection(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/mo")

	// Move to /mode, then Tab again -> per spec Tab cycles, so we use Down then
	// Enter to lock the selection; verify Tab keeps cycling not completing.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(model)
	if m.input.Value() != "/mo" {
		t.Fatalf("Tab should cycle, not yet complete; input=%q", m.input.Value())
	}
}

func TestEscDismissesSuggestionsWithoutClearingInput(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/mo")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)

	if m.suggestionsActive() {
		t.Fatal("Esc should dismiss the suggestion overlay")
	}
	if m.input.Value() != "/mo" {
		t.Fatalf("Esc should not clear the input, got %q", m.input.Value())
	}
}

func TestEscWithoutSuggestionsClearsInputAsBefore(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "hello") // no suggestions

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.input.Value() != "" {
		t.Fatalf("Esc with no suggestions should clear input, got %q", m.input.Value())
	}
}

func TestEnterWithNoSuggestionStillSubmits(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("hello zero") // plain prompt, no suggestions

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if next.input.Value() != "" {
		t.Fatal("Enter should submit (and clear) a plain prompt")
	}
	if !transcriptContains(next.transcript, "hello zero") {
		t.Fatal("submitted prompt should appear in the transcript")
	}
}

func TestSuggestionsSuppressedDuringModals(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.pendingAskUser = &pendingAskUserPrompt{
		request: agent.AskUserRequest{Questions: []agent.AskUserQuestion{{Question: "name?"}}},
		answer:  func([]string) {},
	}
	// Typing while a questionnaire is active feeds the answer field; no overlay.
	m = typeRunes(t, m, "/mo")
	if m.suggestionsActive() {
		t.Fatal("suggestions must stay suppressed while a questionnaire is active")
	}
}

func TestSuggestionsSuppressedDuringSpecReview(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.suggestions = []commandSuggestion{{Name: "/model", Desc: "Pick a model."}}
	m.pendingSpecReview = &pendingSpecReviewPrompt{SpecID: "spec-1", SpecFilePath: ".zero/specs/spec-1.md"}

	if m.suggestionsActive() {
		t.Fatal("stale suggestions must stay suppressed while spec review is active")
	}

	m = typeRunes(t, m, "/mo")
	if m.suggestionsActive() {
		t.Fatal("new suggestions must stay suppressed while spec review is active")
	}
}

func TestSuggestionOverlayRenders(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 96, 30
	m = typeRunes(t, m, "/mo")

	view := m.View()
	if !strings.Contains(view, "/model") || !strings.Contains(view, "/mode") {
		t.Fatal("view should render the suggestion overlay")
	}
}

func TestFileSuggestionsMatchesAndSkipsVCSDirs(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("cmd/server/main.go")
	mustWrite(".git/config")               // hidden VCS dir: must be skipped
	mustWrite("node_modules/dep/index.js") // dependency dir: must be skipped

	got := suggestionTokens(fileSuggestions(root, "main"))
	if !contains(got, "@cmd/server/main.go") {
		t.Fatalf("expected @cmd/server/main.go in %v", got)
	}

	all := suggestionTokens(fileSuggestions(root, ""))
	for _, name := range all {
		if strings.Contains(name, ".git/") || strings.Contains(name, "node_modules/") {
			t.Fatalf("walk must skip VCS/dependency dirs, got %q", name)
		}
	}
}

func TestFileSuggestionsUseWorkspaceIndexSkipRules(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("internal/keep.go")
	mustWrite("build/generated.go") // workspaceindex.ShouldSkipDir skips build
	mustWrite("assets/logo.png")    // workspaceindex.ShouldSkipFile skips binary assets

	got := suggestionTokens(fileSuggestions(root, ""))
	if !contains(got, "@internal/keep.go") {
		t.Fatalf("expected normal source file in suggestions, got %v", got)
	}
	for _, skipped := range []string{"@build/generated.go", "@assets/logo.png"} {
		if contains(got, skipped) {
			t.Fatalf("file suggestions must use workspaceindex skip rules; found %s in %v", skipped, got)
		}
	}
}

// TestFileSuggestionsBoundCountsDirectories proves the walk budget counts
// directory entries (not just files): with a tiny budget, a match that sits
// behind many directories is never reached, so the per-keystroke walk stays
// bounded in directory-heavy trees.
func TestFileSuggestionsBoundCountsDirectories(t *testing.T) {
	root := t.TempDir()
	// Many empty directories sort before "zzz" lexically, so WalkDir visits them
	// first and exhausts the budget before reaching the matching file.
	for i := 0; i < 50; i++ {
		if err := os.MkdirAll(filepath.Join(root, "dir"+string(rune('a'+i%26))+string(rune('0'+i/26))), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	deep := filepath.Join(root, "zzz")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "needle.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Budget smaller than the directory count: must bail before the match.
	if got := suggestionTokens(fileSuggestionsBounded(root, "needle", 5)); contains(got, "@zzz/needle.go") {
		t.Fatalf("walk should have hit the budget before the deep match, got %v", got)
	}
	// Ample budget: the match is reachable.
	if got := suggestionTokens(fileSuggestionsBounded(root, "needle", maxFileWalk)); !contains(got, "@zzz/needle.go") {
		t.Fatalf("with an ample budget the match should be found, got %v", got)
	}
}

func suggestionTokens(s []commandSuggestion) []string {
	names := make([]string, 0, len(s))
	for _, c := range s {
		names = append(names, c.Name)
	}
	return names
}
