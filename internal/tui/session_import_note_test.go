package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agentsessions"
	"github.com/Gitlawb/zero/internal/sessions"
)

// THE IMPORT NOTE IS A TRANSCRIPT ROW, and it was the one foreign-bytes path in
// /resume still drawn raw. The picker row directly beside it already runs every
// title through agentsessions.DisplayField; this note went to appendRow with the
// foreign session id and the recorded cwd exactly as the other agent's store
// spelled them, so an escape in either repainted the rows around it. The id is
// the transcript's FILE NAME, which is a place a control byte survives on every
// platform Zero runs on.
func TestTheImportNoteSanitizesTheForeignIdAndCwd(t *testing.T) {
	home := t.TempDir()
	// The id is the base name, so the escape has to live in the file name for
	// this to exercise the field the note actually prints.
	id := "abc\x1b[2Kdef"
	transcript := filepath.Join(home, ".claude", "projects", "-w", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"/elsewhere/\u001b[31mred/proj","sessionId":"x","message":{"role":"user","content":"hi"}}`
	if err := os.WriteFile(transcript, []byte(line+"\n"), 0o644); err != nil {
		t.Skipf("this platform will not hold a control byte in a file name: %v", err)
	}
	// Every root the adapters resolve, not only the redirect variable: three of
	// the four have no redirect and fall back to HOME.
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	m := model{sessionStore: testSessionStore(t), cwd: t.TempDir()}
	zeroID, note, err := m.importForeignSession("claude-code:" + id)
	if err != nil {
		t.Fatalf("importing a foreign session: %v", err)
	}
	if zeroID == "" {
		t.Fatal("import returned no Zero session id")
	}

	if strings.Contains(note, "\x1b") {
		t.Errorf("a terminal escape reached the transcript note: %q", note)
	}
	// Exact text, so a note that simply dropped the id would not pass. The
	// sanitizer deletes the escape and keeps the rest of the name legible.
	wantPrefix := "Imported claude-code session abc[2Kdef into Zero as " + zeroID
	if !strings.HasPrefix(note, wantPrefix) {
		t.Errorf("note = %q, want it to start %q", note, wantPrefix)
	}
	// And the second sentence, which names the recorded workspace. The temp cwd
	// above is never /elsewhere, so this branch always runs.
	const wantCwd = "\nIt ran in /elsewhere/[31mred/proj, so paths it mentions refer to that tree."
	if !strings.HasSuffix(note, wantCwd) {
		t.Errorf("note = %q, want it to end %q", note, wantCwd)
	}
}

// THE CWD HALF NEEDS THE RECORD AN OLDER BUILD LEFT. agentsessions.Import now
// sanitizes what it stores, so the end-to-end test above cannot see this call —
// it would hold with or without it. A session imported before that change still
// carries the foreign bytes, and this note is what puts them on a transcript row.
func TestTheImportNoteCleansAStoredCwdItDidNotWrite(t *testing.T) {
	result := agentsessions.ImportResult{
		Session: sessions.Metadata{SessionID: "zero_1", Cwd: "/elsewhere/\x1b[31mred\x1b[0m/proj"},
		Events:  2,
		Source: agentsessions.ForeignSession{
			Agent: "claude-code", ID: "abc\x1b[2Kdef", Cwd: "/elsewhere/\x1b[31mred\x1b[0m/proj",
		},
	}
	got := importedSessionNote(result, t.TempDir())
	const want = "Imported claude-code session abc[2Kdef into Zero as zero_1 (2 events).\n" +
		"It ran in /elsewhere/[31mred[0m/proj, so paths it mentions refer to that tree."
	if got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

// A session recorded in THIS workspace says nothing about a different tree — the
// sanitizing must not have disturbed the comparison that decides.
func TestTheImportNoteOmitsTheWorkspaceSentenceInTheSameTree(t *testing.T) {
	here := t.TempDir()
	got := importedSessionNote(agentsessions.ImportResult{
		Session: sessions.Metadata{SessionID: "zero_1", Cwd: here},
		Events:  1,
		Source:  agentsessions.ForeignSession{Agent: "codex", ID: "x", Cwd: here},
	}, here)
	if strings.Contains(got, "It ran in") {
		t.Errorf("a session imported from the current workspace was called foreign: %q", got)
	}
}
