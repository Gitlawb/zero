package agentsessions

import (
	"path/filepath"
	"testing"
)

// The TestTheReal*CorpusStillParses tests pin the on-disk FORMAT, but only on a
// machine that has a live ~/.claude or ~/.codex store — in CI they Skip, so the
// format goes unchecked exactly where regressions would land. These tests point
// the real adapters at a checked-in fixture store under testdata via the same
// redirect variables production honours (CLAUDE_CONFIG_DIR, CODEX_HOME), so the
// format-pin runs deterministically and never skips. A shape change upstream
// fails a test here instead of silently emptying a picker in front of a user.
//
// The fixtures carry generic, invented work — never a real transcript — so
// nothing personal is checked in; the live-store tests keep them honest against
// the real shapes.
func fixtureEnv(t *testing.T, redirectVar string, dir string) Env {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", dir))
	if err != nil {
		t.Fatal(err)
	}
	return testEnv("", map[string]string{redirectVar: abs})
}

func TestTheClaudeCodeFixtureParsesEndToEnd(t *testing.T) {
	adapter := ClaudeCode(fixtureEnv(t, "CLAUDE_CONFIG_DIR", "claude-config"))

	found, err := adapter.Discover("")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("the checked-in Claude Code fixture indexed no sessions — the format pin is broken")
	}
	session := found[0]
	for name, value := range map[string]string{
		"ID": session.ID, "Cwd": session.Cwd, "Title": session.Title, "ModelID": session.ModelID,
	} {
		if value == "" {
			t.Errorf("%s is empty in the fixture index — it is a field the CLI prints", name)
		}
	}

	// Discover and Read must agree: a session the picker lists must import.
	events, err := adapter.Read(session.ID, ReadOptions{})
	if err != nil {
		t.Fatalf("Read of a discovered fixture session failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("Read produced no events from the fixture")
	}
}

func TestTheCodexFixtureParsesEndToEnd(t *testing.T) {
	adapter := Codex(fixtureEnv(t, "CODEX_HOME", "codex-home"))

	found, err := adapter.Discover("")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("the checked-in Codex fixture indexed no sessions — the format pin is broken")
	}
	session := found[0]
	if session.ID == "" || session.Cwd == "" || session.ModelID == "" {
		t.Errorf("incomplete Codex fixture index entry: %+v", session)
	}

	events, err := adapter.Read(session.ID, ReadOptions{})
	if err != nil {
		t.Fatalf("Read of a discovered Codex fixture session failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("Read produced no events from the Codex fixture")
	}
}
