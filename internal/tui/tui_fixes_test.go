package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FIX 1: a bare "/" surfaces the full command palette (was suppressed).
func TestBareSlashListsCommandPalette(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = typeRunes(t, m, "/")
	if !m.suggestionsActive() {
		t.Fatal("a bare / should surface the command palette")
	}
	if len(m.suggestions) == 0 {
		t.Fatal("expected command suggestions for a bare /")
	}
	if m.suggestionsAreFiles {
		t.Fatal("a bare / should be command suggestions, not files")
	}
}

// FIX 4: "@" helpers — trailing-token detection and replacement.
func TestTrailingAtToken(t *testing.T) {
	cases := []struct {
		in    string
		token string
		ok    bool
	}{
		{"@", "", true},
		{"@foo", "foo", true},
		{"read @foo/bar", "foo/bar", true},
		{"hello", "", false},
		{"read @foo done", "", false}, // trailing word is "done", not an @token
		{"", "", false},
	}
	for _, c := range cases {
		token, ok := trailingAtToken(c.in)
		if ok != c.ok || token != c.token {
			t.Errorf("trailingAtToken(%q) = (%q,%v), want (%q,%v)", c.in, token, ok, c.token, c.ok)
		}
	}
}

func TestReplaceTrailingAtToken(t *testing.T) {
	if got := replaceTrailingAtToken("read @fo", "@internal/loop.go"); got != "read @internal/loop.go" {
		t.Fatalf("replaceTrailingAtToken mid-prompt = %q", got)
	}
	if got := replaceTrailingAtToken("@m", "@main.go"); got != "@main.go" {
		t.Fatalf("replaceTrailingAtToken whole-input = %q", got)
	}
}

// FIX 4: "@" surfaces workspace files, filters, and skips VCS dirs.
func TestFileSuggestionsListsAndFiltersWorkspaceFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"))
	mustWrite(t, filepath.Join(dir, "internal", "loop.go"))
	mustWrite(t, filepath.Join(dir, ".git", "config")) // must be skipped

	all := suggestionNames2(fileSuggestions(dir, ""))
	if !contains(all, "@main.go") || !contains(all, "@internal/loop.go") {
		t.Fatalf("expected workspace files, got %v", all)
	}
	for _, s := range all {
		if strings.Contains(s, ".git/") {
			t.Fatalf("file suggestions must skip .git, got %v", all)
		}
	}
	filtered := suggestionNames2(fileSuggestions(dir, "loop"))
	if !contains(filtered, "@internal/loop.go") || contains(filtered, "@main.go") {
		t.Fatalf("filter 'loop' = %v, want only loop.go", filtered)
	}
}

// FIX 2: the zeroline header reflects cost + cumulative tokens from the tracker.
func TestZerolineHeaderReflectsUsage(t *testing.T) {
	m := newModel(context.Background(), Options{})
	if m.usageTracker == nil {
		t.Skip("no usage tracker")
	}
	m.unpricedTokens = 1234 // fallback path when nothing is priced yet
	h := m.zerolineHeader()
	if h.TotalTokens != 1234 {
		t.Fatalf("header TotalTokens = %d, want 1234 (unpriced fallback)", h.TotalTokens)
	}
}

// FIX 3: "!cmd" parses as a shell escape, not a chat prompt.
func TestParseCommandBangIsShellEscape(t *testing.T) {
	got := parseCommand("!ls -la")
	if got.kind != commandBash || got.text != "ls -la" {
		t.Fatalf("parseCommand(!ls -la) = {kind:%v text:%q}, want commandBash/\"ls -la\"", got.kind, got.text)
	}
	if parseCommand("/help").kind == commandBash {
		t.Fatal("/help must not parse as bash")
	}
	if parseCommand("just chatting").kind != commandPrompt {
		t.Fatal("plain text must still parse as a prompt")
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func suggestionNames2(s []commandSuggestion) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		out = append(out, x.Name)
	}
	return out
}
