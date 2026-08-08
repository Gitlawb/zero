package agentsessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures below are hand-written to the shapes observed in a real
// ~/.claude/projects corpus (record types, field names, nesting) rather than
// copied from real transcripts, which carry the author's actual work. The
// real-corpus test at the bottom of this file keeps the two honest: it runs
// against the live store when one exists and skips when it does not.

// writeClaudeStore lays out a projects/ tree and returns its root.
func writeClaudeStore(t *testing.T, files map[string][]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "projects")
	for relative, lines := range files {
		writeFile(t, filepath.Join(root, relative), strings.Join(lines, "\n")+"\n")
	}
	return root
}

func TestDiscoverIndexesASessionFromABoundedHeadRead(t *testing.T) {
	root := writeClaudeStore(t, map[string][]string{
		"-Users-someone-proj/aaa.jsonl": {
			`{"type":"mode","mode":"default"}`,
			`{"type":"user","cwd":"/Users/someone/proj","gitBranch":"main","sessionId":"aaa","timestamp":"2026-08-01T10:00:00.000Z","message":{"role":"user","content":"Fix the flaky retry test"}}`,
			`{"type":"ai-title","aiTitle":"Fix flaky retry test","sessionId":"aaa"}`,
			`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"On it."}]}}`,
		},
	})

	got, err := discoverFamily1("claude-code", root, "/Users/someone/proj", indexFamily1Transcript)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1", len(got))
	}
	session := got[0]
	if session.ID != "aaa" {
		t.Errorf("ID = %q, want aaa", session.ID)
	}
	if session.Cwd != "/Users/someone/proj" {
		t.Errorf("Cwd = %q", session.Cwd)
	}
	if session.GitBranch != "main" {
		t.Errorf("GitBranch = %q, want main", session.GitBranch)
	}
	if session.ModelID != "claude-opus-5" {
		t.Errorf("ModelID = %q, want claude-opus-5", session.ModelID)
	}
	// The agent's own generated title beats a truncated first prompt.
	if session.Title != "Fix flaky retry test" {
		t.Errorf("Title = %q, want the ai-title record to win", session.Title)
	}
	if session.StartedAt.IsZero() {
		t.Error("StartedAt was not populated from the first timestamp")
	}
}

func TestTheFirstPromptIsTheTitleWhenTheAgentRecordedNone(t *testing.T) {
	root := writeClaudeStore(t, map[string][]string{
		"-Users-someone-proj/bbb.jsonl": {
			`{"type":"user","cwd":"/Users/someone/proj","sessionId":"bbb","message":{"role":"user","content":"  Investigate   the   crash \n in the parser  "}}`,
		},
	})
	got, _ := discoverFamily1("claude-code", root, "", indexFamily1Transcript)
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1", len(got))
	}
	if got[0].Title != "Investigate the crash in the parser" {
		t.Errorf("Title = %q, want the whitespace-collapsed prompt", got[0].Title)
	}
}

// TestTheRecordedCwdBeatsTheDirectoryName is the property that makes the lossy
// slug safe. Both directories below are plausible spellings of the requested
// cwd, but only one transcript actually ran there.
func TestTheRecordedCwdBeatsTheDirectoryName(t *testing.T) {
	root := writeClaudeStore(t, map[string][]string{
		// Same slug shape; different real directories.
		"-Users-someone-dev-zero/real.jsonl": {
			`{"type":"user","cwd":"/Users/someone/dev/zero","sessionId":"real","message":{"role":"user","content":"in the right place"}}`,
		},
		"-Users-someone-dev-zero2/impostor.jsonl": {
			`{"type":"user","cwd":"/Users/someone/dev-zero","sessionId":"impostor","message":{"role":"user","content":"a different directory"}}`,
		},
	})

	got, err := discoverFamily1("claude-code", root, "/Users/someone/dev/zero", indexFamily1Transcript)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "real" {
		t.Fatalf("got %v, want only the transcript whose recorded cwd matches", ids(got))
	}
}

// TestSubagentTranscriptsAreNotSessions pins the fixed-depth glob against the
// real corpus shape: 997 of 1,266 files in the live store are sub-agent
// transcripts under <session>/subagents/, and listing them as resumable
// sessions would bury the 269 real ones.
func TestSubagentTranscriptsAreNotSessions(t *testing.T) {
	root := writeClaudeStore(t, map[string][]string{
		"-Users-someone-proj/parent.jsonl": {
			`{"type":"user","cwd":"/Users/someone/proj","sessionId":"parent","message":{"role":"user","content":"top level"}}`,
		},
		"-Users-someone-proj/parent/subagents/agent-1.jsonl": {
			`{"type":"user","cwd":"/Users/someone/proj","sessionId":"agent-1","message":{"role":"user","content":"delegated"}}`,
		},
	})
	got, _ := discoverFamily1("claude-code", root, "/Users/someone/proj", indexFamily1Transcript)
	if len(got) != 1 || got[0].ID != "parent" {
		t.Fatalf("got %v, want only the top-level session", ids(got))
	}
}

// TestNonTranscriptsAreSkippedRatherThanListed covers what the live corpus
// actually contains alongside real sessions: single-record "bridge-session"
// stubs (9 of 269 there), empty files, and half-written trailing lines from a
// session being appended to right now.
func TestNonTranscriptsAreSkippedRatherThanListed(t *testing.T) {
	root := writeClaudeStore(t, map[string][]string{
		"-Users-someone-proj/good.jsonl": {
			`{"type":"user","cwd":"/Users/someone/proj","sessionId":"good","message":{"role":"user","content":"real work"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"do`, // torn final line
		},
		"-Users-someone-proj/bridge.jsonl": {
			`{"type":"bridge-session","sessionId":"bridge"}`,
		},
		"-Users-someone-proj/empty.jsonl":   {""},
		"-Users-someone-proj/garbage.jsonl": {"not json at all", "{also not"},
	})

	got, err := discoverFamily1("claude-code", root, "", indexFamily1Transcript)
	if err != nil {
		t.Fatalf("discovery must not fail on junk beside real sessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("got %v, want only the real session — a torn trailing line must "+
			"not discard the records before it", ids(got))
	}
}

func TestDiscoveryOfAnAbsentStoreIsEmptyNotAnError(t *testing.T) {
	got, err := discoverFamily1("claude-code", filepath.Join(t.TempDir(), "never-created"), "", indexFamily1Transcript)
	if err != nil || len(got) != 0 {
		t.Fatalf("got (%v, %v), want (empty, nil)", ids(got), err)
	}
	if got, err := discoverFamily1("claude-code", "", "", indexFamily1Transcript); err != nil || len(got) != 0 {
		t.Fatalf("empty root: got (%v, %v), want (empty, nil)", ids(got), err)
	}
}

func TestSessionsAreListedMostRecentFirst(t *testing.T) {
	root := writeClaudeStore(t, map[string][]string{
		"-Users-someone-proj/old.jsonl": {
			`{"type":"user","cwd":"/Users/someone/proj","sessionId":"old","message":{"role":"user","content":"first"}}`,
		},
		"-Users-someone-proj/new.jsonl": {
			`{"type":"user","cwd":"/Users/someone/proj","sessionId":"new","message":{"role":"user","content":"second"}}`,
		},
	})
	// Make the ordering unambiguous rather than relying on write order.
	older := filepath.Join(root, "-Users-someone-proj", "old.jsonl")
	if err := os.Chtimes(older, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	got, _ := discoverFamily1("claude-code", root, "", indexFamily1Transcript)
	if len(got) != 2 || got[0].ID != "new" {
		t.Fatalf("got %v, want the most recently updated session first", ids(got))
	}
}

// TestFindTranscriptCannotBeTalkedIntoOpeningAnArbitraryPath is why ids are
// matched against glob results instead of being joined onto a root.
func TestFindTranscriptCannotBeTalkedIntoOpeningAnArbitraryPath(t *testing.T) {
	root := writeClaudeStore(t, map[string][]string{
		"-Users-someone-proj/aaa.jsonl": {
			`{"type":"user","cwd":"/Users/someone/proj","sessionId":"aaa","message":{"role":"user","content":"x"}}`,
		},
	})
	// A credential file one level above the store, exactly as every surveyed
	// agent ships one.
	secret := filepath.Join(filepath.Dir(root), "auth.json")
	writeFile(t, secret, `{"access_token":"tok_MUST_NEVER_BE_READ"}`)

	for _, hostile := range []string{
		"../auth",
		"../../auth",
		filepath.Join("..", "auth.json"),
		secret,
		strings.TrimSuffix(secret, ".json"),
		"/etc/passwd",
	} {
		if path, err := findTranscript(root, hostile); err == nil {
			t.Errorf("findTranscript(%q) resolved to %q, want an error", hostile, path)
		}
	}

	if path, err := findTranscript(root, "aaa"); err != nil || filepath.Base(path) != "aaa.jsonl" {
		t.Errorf("findTranscript(\"aaa\") = (%q, %v), want the real transcript", path, err)
	}
}

// TestTheRealCorpusStillParses runs against the live store when there is one.
// The fixtures above pin the logic; this pins the FORMAT — these are
// undocumented files belonging to another product, and a shape change upstream
// should surface here rather than as an empty list in front of a user.
func TestTheRealCorpusStillParses(t *testing.T) {
	env := OSEnv()
	adapter := ClaudeCode(env)
	root := claudeCodeRoot(env)
	if root == "" {
		t.Skip("no home directory")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skip("no Claude Code store on this machine")
	}

	found, err := adapter.Discover("")
	if err != nil {
		t.Fatalf("discovering the real store failed: %v", err)
	}
	transcripts := 0
	for _, dir := range globSessionDirs(root) {
		transcripts += len(globTranscripts(filepath.Join(dir, "*"+transcriptExt)))
	}
	if transcripts == 0 {
		t.Skip("store exists but holds no transcripts")
	}

	// Every indexed session must carry the fields the CLI will print. A format
	// change that silently blanks one of these is the failure mode worth
	// catching.
	for _, session := range found {
		if session.ID == "" || session.Cwd == "" || session.Title == "" {
			t.Errorf("incomplete index entry: %+v", session)
			break
		}
	}

	// Indexing should account for nearly every transcript. The known-legitimate
	// exclusions are single-record stubs (bridge-session), which ran at ~3% of
	// the corpus when this was written. A large unexplained gap means the head
	// budget or the record shape has drifted.
	if ratio := float64(len(found)) / float64(transcripts); ratio < 0.85 {
		t.Errorf("indexed %d of %d real transcripts (%.0f%%) — too many are being "+
			"dropped; check defaultHeadLimit.MaxBytes and the record shape",
			len(found), transcripts, ratio*100)
	}
	t.Logf("indexed %d of %d transcripts in the live store", len(found), transcripts)
}

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func ids(items []ForeignSession) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}
