package agentsessions

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
)

// TestAnImportedSessionIsResumable is the end-to-end join this whole package
// exists to reach: a foreign transcript becomes a Zero session that Zero's own
// resume machinery accepts, with no special-casing anywhere downstream.
//
// It runs the real sessions.PrepareExec and sessions.FormatExecPrompt — the
// same functions behind `zero exec --resume` and the TUI's /resume — so a change
// to either that broke imported sessions would fail here rather than in front of
// a user. The only thing it does not do is call a provider.
func TestAnImportedSessionIsResumable(t *testing.T) {
	home := t.TempDir()
	transcript := filepath.Join(home, ".claude", "projects", "-Users-someone-proj", "abc123.jsonl")
	writeFile(t, transcript, strings.Join([]string{
		`{"type":"user","cwd":"/Users/someone/proj","gitBranch":"fix/parser","sessionId":"abc123","timestamp":"2026-08-01T10:00:00.000Z","message":{"role":"user","content":"The parser drops trailing commas"}}`,
		`{"type":"ai-title","aiTitle":"Fix trailing comma parsing","sessionId":"abc123"}`,
		`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"parser.go"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"func parse() {}"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"The bug is in parse(): it returns before the comma check."}]}}`,
	}, "\n")+"\n")

	adapter := ClaudeCode(testEnv(home, nil))
	store := sessions.NewStore(sessions.StoreOptions{RootDir: filepath.Join(t.TempDir(), "sessions")})

	result, err := Import(store, adapter, "abc123", ReadOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	// 4 conversation events plus the activity summary the import adds.
	if result.Events < 4 {
		t.Fatalf("imported %d events, want at least the 4 conversation events", result.Events)
	}
	// Metadata the CLI and picker display must be carried over, not invented.
	if result.Session.Title != "Fix trailing comma parsing" {
		t.Errorf("title = %q", result.Session.Title)
	}
	if result.Session.Cwd != "/Users/someone/proj" {
		t.Errorf("cwd = %q", result.Session.Cwd)
	}
	// The tag records the agent AND the foreign session id, which is what lets
	// the /resume picker tell an already-imported session from one still only on
	// the other agent's disk.
	if result.Session.Tag != "imported:claude-code:abc123" {
		t.Errorf("tag = %q, want agent and source id recorded", result.Session.Tag)
	}
	agent, sourceID, ok := ParseImportTag(result.Session.Tag)
	if !ok || agent != "claude-code" || sourceID != "abc123" {
		t.Errorf("ParseImportTag(%q) = (%q, %q, %v)", result.Session.Tag, agent, sourceID, ok)
	}

	// The real resume path.
	prepared, err := sessions.PrepareExec(sessions.PrepareExecOptions{
		Store:  store,
		Resume: result.Session.SessionID,
	})
	if err != nil {
		t.Fatalf("PrepareExec on an imported session: %v", err)
	}
	if prepared.Mode != sessions.ModeResume {
		t.Fatalf("mode = %s, want resume", prepared.Mode)
	}
	if len(prepared.ContextEvents) != result.Events {
		t.Fatalf("resume loaded %d events, want the %d that were imported",
			len(prepared.ContextEvents), result.Events)
	}

	prompt := sessions.FormatExecPrompt("What is left to do?", prepared)

	// The digest is what the next model actually sees. If the imported work is
	// not in it, the import accomplished nothing.
	for _, want := range []string{
		"The parser drops trailing commas", // the original ask
		"returns before the comma check",   // what the other agent concluded
		"What is left to do?",              // the new request
		result.Session.SessionID,           // continuity
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("resume prompt is missing %q:\n%s", want, prompt)
		}
	}
}

// TestImportingTwiceMakesTwoSnapshots documents the deliberate choice not to
// derive the Zero id from the foreign one. The foreign session may be continued
// in its own tool after an import, so a second import is a second snapshot
// rather than a mistake to refuse.
func TestImportingTwiceMakesTwoSnapshots(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "projects", "-p", "s1.jsonl"),
		`{"type":"user","cwd":"/p","sessionId":"s1","message":{"role":"user","content":"hello"}}`+"\n")

	adapter := ClaudeCode(testEnv(home, nil))
	store := sessions.NewStore(sessions.StoreOptions{RootDir: filepath.Join(t.TempDir(), "sessions")})

	first, err := Import(store, adapter, "s1", ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Import(store, adapter, "s1", ReadOptions{})
	if err != nil {
		t.Fatalf("a second import must be allowed: %v", err)
	}
	if first.Session.SessionID == second.Session.SessionID {
		t.Error("both imports produced the same Zero session id")
	}
}

func TestImportRejectsAnUnknownReference(t *testing.T) {
	env := testEnv(t.TempDir(), nil)
	for _, ref := range []string{
		"",
		"claude-code",
		"claude-code:",
		":abc",
		"cursor:abc",
	} {
		if _, _, err := ParseRef(env, ref); err == nil {
			t.Errorf("ParseRef(%q) returned no error", ref)
		}
	}
	adapter, id, err := ParseRef(env, "claude-code:abc123")
	if err != nil || adapter.Name() != "claude-code" || id != "abc123" {
		t.Errorf("ParseRef of a valid ref = (%v, %q, %v)", adapter, id, err)
	}
}

func TestImportTagsRoundTrip(t *testing.T) {
	agent, sourceID, ok := ParseImportTag(ImportTag("codex", "019f-abc"))
	if !ok || agent != "codex" || sourceID != "019f-abc" {
		t.Errorf("round trip = (%q, %q, %v)", agent, sourceID, ok)
	}

	// Non-import tags, and the older two-part form that records no source id,
	// must not parse as a source reference.
	for _, tag := range []string{"", "   ", "some-tag", "imported:", "imported:codex"} {
		if _, _, ok := ParseImportTag(tag); ok {
			t.Errorf("ParseImportTag(%q) reported a source id it does not have", tag)
		}
	}

	// ImportedAgent is deliberately more forgiving: a session imported before
	// the tag carried a source id must still group under its agent in the
	// picker rather than silently becoming a Zero-native session.
	for tag, want := range map[string]string{
		"imported:codex":           "codex",
		"imported:claude-code:abc": "claude-code",
		"":                         "",
		"unrelated":                "",
	} {
		if got := ImportedAgent(tag); got != want {
			t.Errorf("ImportedAgent(%q) = %q, want %q", tag, got, want)
		}
	}
}
