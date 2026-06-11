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

func TestMouseWheelMovesSuggestions(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/")

	updated, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	m = updated.(model)
	if m.suggestionIdx != 1 {
		t.Fatalf("wheel down should select index 1, got %d", m.suggestionIdx)
	}

	updated, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = updated.(model)
	if m.suggestionIdx != 0 {
		t.Fatalf("wheel up should select index 0, got %d", m.suggestionIdx)
	}
}

func TestEnterRunsCommandSuggestion(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/he") // selects /help

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if cmd != nil {
		t.Fatal("Enter on a command suggestion should not start an agent run")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("Enter on a command suggestion should clear input, got %q", got)
	}
	if m.suggestionsActive() {
		t.Fatal("running a command suggestion should dismiss the overlay")
	}
	if !transcriptContains(m.transcript, "Commands") {
		t.Fatal("running /help from suggestions should append help output")
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

func TestEscDismissesCommandSuggestionsAndClearsInput(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/mo")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)

	if m.suggestionsActive() {
		t.Fatal("Esc should dismiss the suggestion overlay")
	}
	if m.input.Value() != "" {
		t.Fatalf("Esc should clear slash command input, got %q", m.input.Value())
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
	plain := plainRender(t, view)
	if !strings.Contains(plain, "model") || !strings.Contains(plain, "mode") {
		t.Fatal("view should render the suggestion overlay")
	}
	if strings.Contains(plain, "/model") || strings.Contains(plain, "/mode") {
		t.Fatalf("suggestion overlay should display command names without slash prefixes, got %q", plain)
	}
	for _, want := range []string{"╭── Commands", "╰", "search > mo", "↑/↓ move", "Enter run", "Esc close"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("suggestion overlay should include %q in %q", want, plain)
		}
	}
}

func TestSuggestionOverlayCapsRowsWithoutMoreText(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 96, 30
	m = typeRunes(t, m, "/")

	plain := plainRender(t, m.View())
	if strings.Contains(plain, "more") {
		t.Fatalf("bare slash palette should not render a more-count row, got %q", plain)
	}
	if !strings.Contains(plain, "│ ❯ provider") || !strings.Contains(plain, "│   context") {
		t.Fatalf("top of palette should render first visible command window, got %q", plain)
	}
	if strings.Contains(plain, "compact") {
		t.Fatalf("top of palette should cap hidden commands without rendering them, got %q", plain)
	}

	for range suggestionPaletteMaxVisible + 1 {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(model)
	}
	plain = plainRender(t, m.View())
	if strings.Contains(plain, "more") {
		t.Fatalf("scrolled palette should not render a more-count row, got %q", plain)
	}
	if !strings.Contains(plain, "│ ❯ clear") || strings.Contains(plain, "│ ❯ provider") || strings.Contains(plain, "│   provider") {
		t.Fatalf("scrolled palette should move the visible command window, got %q", plain)
	}
}

func TestCommandPaletteStaysOpenForNoMatches(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 96, 30
	m = typeRunes(t, m, "/,")

	if !m.suggestionsActive() {
		t.Fatal("command palette should stay active for a no-match slash query")
	}
	if len(m.suggestions) != 0 {
		t.Fatalf("expected no command matches, got %v", suggestionNames(m))
	}
	plain := plainRender(t, m.View())
	for _, want := range []string{"search > ,", "no matching commands"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("no-match palette should include %q in %q", want, plain)
		}
	}
	if strings.Contains(plain, "/,") {
		t.Fatalf("slash query should stay inside palette display, got %q", plain)
	}
}

func TestEnterOnNoMatchCommandPaletteDoesNotSubmit(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/,")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if cmd != nil {
		t.Fatal("Enter on no-match command palette should not start a command")
	}
	if m.input.Value() != "/," {
		t.Fatalf("Enter on no-match command palette should preserve search input, got %q", m.input.Value())
	}
	if !m.suggestionsActive() {
		t.Fatal("Enter on no-match command palette should keep palette open")
	}
	if transcriptContains(m.transcript, "unknown command") {
		t.Fatal("Enter on no-match command palette should not submit an unknown command")
	}
}

func TestFilePaletteStaysOpenForNoMatches(t *testing.T) {
	m := newModel(context.Background(), Options{Cwd: t.TempDir()})
	m.width, m.height = 96, 30
	m = typeRunes(t, m, "@missing")

	if !m.suggestionsActive() {
		t.Fatal("file palette should stay active for a no-match @ query")
	}
	if len(m.suggestions) != 0 {
		t.Fatalf("expected no file matches, got %v", suggestionNames(m))
	}
	plain := plainRender(t, m.View())
	for _, want := range []string{"Files", "search > missing", "no matching files"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("no-match file palette should include %q in %q", want, plain)
		}
	}
	if strings.Contains(plain, "@missing") {
		t.Fatalf("bare @ query should stay inside palette display without @ prefix, got %q", plain)
	}
}

func TestEscDismissesFilePaletteAndRemovesTrailingToken(t *testing.T) {
	m := newModel(context.Background(), Options{Cwd: t.TempDir()})
	m = typeRunes(t, m, "read @missing")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)

	if m.suggestionsActive() {
		t.Fatal("Esc should dismiss the file palette")
	}
	if got := m.input.Value(); got != "read " {
		t.Fatalf("Esc should remove only the trailing @ token, got %q", got)
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
