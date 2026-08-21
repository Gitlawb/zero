package agentsessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
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
	adapter := ClaudeCode(fixtureEnv(t, "CLAUDE_CONFIG_DIR", "claude"))

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
	adapter := Codex(fixtureEnv(t, "CODEX_HOME", "codex"))

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

// WHY THESE FIXTURES EXIST. The TestTheReal*CorpusStillParses tests below assert
// STATISTICS over whatever store the machine running them happens to have, and
// that is not a property of this package. The same code reported "44 of 44
// rollouts, 43 with a model" and "360 of 367 transcripts" here while a reviewer
// with a smaller store got "0 with a model" and "15 of 21 (71%)" and a red
// build. Green for CI, red for the contributor, and the shapes that actually
// caused it were never written down anywhere a test could find them.
//
// These pin the two shapes by construction, so the behaviour is checked on every
// machine and a change to it fails here rather than in a reviewer's terminal.

// A SESSION WITH NO cwd ANYWHERE IS NOT INDEXABLE, and that is correct. cwd is
// only ever carried by user, attachment and system records; the preamble types
// (queue-operation, last-prompt, mode, permission-mode, bridge-session) never
// carry it. A transcript that never reached its first user turn therefore has no
// workspace to bind to, and a picker row for it could not be resumed anywhere.
//
// This is the whole of the gap on the machine this was written on: all 7 of the
// 367 unindexed transcripts were single-record bridge-session stubs.
func TestASessionWithNoWorkspaceIsNotIndexed(t *testing.T) {
	adapter := ClaudeCode(fixtureEnv(t, "CLAUDE_CONFIG_DIR", "drops"))
	found, err := adapter.Discover("")
	if err != nil {
		t.Fatal(err)
	}
	indexed := map[string]ForeignSession{}
	for _, session := range found {
		indexed[session.ID] = session
	}
	for _, id := range []string{"bridge", "preamble"} {
		if session, listed := indexed[id]; listed {
			t.Errorf("%q has no cwd in any record but was indexed as %+v — the picker would offer a session that cannot be resumed", id, session)
		}
	}
	// THE CONTROL, so the two assertions above cannot pass because the whole
	// fixture store failed to load and nothing was indexed at all.
	good, listed := indexed["good"]
	if !listed {
		t.Fatalf("the control session was not indexed; the drop assertions above prove nothing. Indexed: %v", found)
	}
	if good.Cwd != "/w" || good.Title == "" || good.ModelID == "" {
		t.Errorf("the control session lost a field the CLI prints: %+v", good)
	}
}

// THE cwd-BEARING RECORD IS SUBJECT TO THE PER-LINE CAP, and a session whose
// only cwd sits in a record longer than MaxLineBytes is dropped exactly like a
// stub above — same verdict, different cause, and nothing distinguishes them in
// the output. readBoundedLine keeps the first MaxLineBytes of an overlong record
// and the truncated JSON then fails to parse, so the record is skipped whole.
//
// This is not hypothetical on a real corpus. On the machine this was written on
// the opening user record is already over the cap in 30 of 367 transcripts, and
// 73 of the 360 indexed sessions take their cwd from a following attachment
// record rather than from the user record that should have supplied it. Those
// survive only because Claude Code happens to write a small attachment next. One
// that does not would vanish, and would look like a legitimate stub.
func TestAWorkspaceInAnOverlongRecordIsStillFound(t *testing.T) {
	adapter := ClaudeCode(fixtureEnv(t, "CLAUDE_CONFIG_DIR", "drops"))
	found, err := adapter.Discover("")
	if err != nil {
		t.Fatal(err)
	}
	var longcwd *ForeignSession
	for i := range found {
		if found[i].ID == "longcwd" {
			longcwd = &found[i]
		}
	}
	if longcwd == nil {
		t.Fatalf("a session whose cwd record fits the real per-line cap was dropped. Indexed: %v", found)
	}
	if longcwd.Cwd != "/w" {
		t.Errorf("longcwd indexed with Cwd %q, want /w", longcwd.Cwd)
	}
}

// turn_context IS THE ONLY RECORD CARRYING THE MODEL, and it is not always near
// the top. session_meta has no "model" key at all — enumerated across a real
// 44-rollout store, only turn_context does — so a rollout whose turn_context
// falls outside the bounded head scan indexes with an empty ModelID. This is the
// shape behind a reviewer's "2 titled, 0 with a model": their rollouts had it
// late, the ones here have it at line 4-8.
//
// The session is still listed, titled, addressable and importable; only the
// model label is missing. That is the intended trade — Discover walks the entire
// date-partitioned store on every picker open and must stay cheap — so this test
// pins the CURRENT behaviour rather than asserting the model is recovered. If
// the index is ever taught to recover it, this test should fail and be updated
// deliberately, not silently drift.
func TestARolloutWithALateTurnContextIndexesWithoutAModel(t *testing.T) {
	adapter := Codex(fixtureEnv(t, "CODEX_HOME", "codex-late"))
	found, err := adapter.Discover("")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly the one late-turn_context rollout, got %d: %v", len(found), found)
	}
	session := found[0]
	// The session is NOT lost — that is the part that matters.
	if session.Cwd == "" {
		t.Errorf("a rollout with a late turn_context lost its workspace too: %+v", session)
	}
	if session.ModelID != "" {
		t.Errorf("ModelID is %q; the head scan is not supposed to reach a turn_context past the line budget. "+
			"If the index was deliberately taught to recover it, update this test and the comment above it.", session.ModelID)
	}
	// And it still imports, which is what "not lost" has to mean in practice.
	events, err := adapter.Read(session.ID, ReadOptions{})
	if err != nil {
		t.Fatalf("a rollout indexed without a model failed to import: %v", err)
	}
	if len(events) == 0 {
		t.Error("the late-turn_context rollout imported no events")
	}
}

// AN ORDINARY LONG MESSAGE IS NOT AN EDGE CASE, and it was being deleted from
// the imported conversation without a word. Both translators passed the
// DISCOVERY per-line cap (64 KiB) to streamLines — a budget that exists because
// the index pays it once per file across the whole store — so a single assistant
// reply over 64 KiB was truncated into invalid JSON, skipped, and Read returned
// nil error.
//
// The result was worse than incomplete: the restored transcript read as a
// question, no answer, then the user's follow-up. Both the user and the model
// continuing the session would see a conversation that looks whole.
func TestAnOrdinaryLongMessageSurvivesImport(t *testing.T) {
	adapter, _ := longMessageStore(t, 65*1024)
	events, err := adapter.Read("s", ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("a %d KiB assistant reply was dropped from the import: got %d events, want 3", 65, len(events))
	}
	if got := payloadText(t, events[1]); len(got) < 60*1024 {
		t.Errorf("the long reply was imported truncated: %d bytes", len(got))
	}
}

// PAST THE IMPORT CAP TOO, THE LOSS IS NAMED. The cap is still a cap — a
// transcript cannot be allowed to exhaust memory — but a record that exceeds it
// produces a marker rather than a hole, so the gap is visible to whoever reads
// the session next.
func TestARecordPastTheImportCapIsReportedNotDropped(t *testing.T) {
	adapter, _ := longMessageStore(t, 9<<20)
	events, err := adapter.Read("s", ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var reported bool
	for _, event := range events {
		if event.Type == sessions.EventError && strings.Contains(payloadText(t, event), "could not be read") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("a record past the import cap vanished silently; events=%d", len(events))
	}
}

func longMessageStore(t *testing.T, size int) (Adapter, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", size)
	records := []any{
		map[string]any{"type": "user", "cwd": "/w", "timestamp": "2026-01-01T00:00:00Z",
			"message": map[string]any{"role": "user", "model": "m", "content": "short question"}},
		map[string]any{"type": "assistant", "cwd": "/w", "timestamp": "2026-01-01T00:00:01Z",
			"message": map[string]any{"role": "assistant", "content": body}},
		map[string]any{"type": "user", "cwd": "/w", "timestamp": "2026-01-01T00:00:02Z",
			"message": map[string]any{"role": "user", "content": "follow up"}},
	}
	file, err := os.Create(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(append(encoded, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return ClaudeCode(testEnv("", map[string]string{"CLAUDE_CONFIG_DIR": root})), root
}

func payloadText(t *testing.T, event sessions.AppendEventInput) string {
	t.Helper()
	payload, ok := event.Payload.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload shape %T", event.Payload)
	}
	for _, key := range []string{"content", "message"} {
		if value, ok := payload[key].(string); ok {
			return value
		}
	}
	return ""
}
