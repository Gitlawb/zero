package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agentsessions"
	"github.com/Gitlawb/zero/internal/sessions"
)

// twoResumableSessions seeds a store with sessions "a" and "b" in workspace
// and returns a model whose active session is "a".
func twoResumableSessions(t *testing.T) (model, *sessions.Store) {
	t.Helper()
	store := testSessionStore(t)
	workspace := t.TempDir()
	var metaA sessions.Metadata
	for _, id := range []string{"a", "b"} {
		meta, err := store.Create(sessions.CreateInput{SessionID: id, Title: "session " + id, Cwd: workspace})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendEvents(id, []sessions.AppendEventInput{
			{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "ask " + id}},
			{Type: sessions.EventMessage, Payload: map[string]any{"role": "assistant", "content": "answer " + id}},
		}); err != nil {
			t.Fatal(err)
		}
		if id == "a" {
			metaA = meta
		}
	}
	m := model{
		sessionStore:     store,
		agentSessionsEnv: agentsessions.Env{Home: t.TempDir()},
		cwd:              workspace,
		now:              func() time.Time { return time.Unix(0, 0) },
		activeSession:    metaA,
	}
	return m, store
}

// A LATE PICKER MUST NOT SWITCH A RUNNING SESSION. Bare /resume dispatches
// discovery asynchronously and checked m.pending only at dispatch. A prompt
// submitted before the result arrived started a run; the result then installed
// the picker anyway, and choosing another session called resumeZeroSession
// without rechecking, so the active session changed under the live run and its
// completion appended into the other conversation. Discovery stays asynchronous;
// what changes is that a result is tied to the request and session it was made
// for, is refused while a run is active, and the selection route rechecks on its
// own — the refusal comes before any mutation, never as repair afterwards.
func TestALatePickerResultCannotSwitchSessionsMidRun(t *testing.T) {
	m, _ := twoResumableSessions(t)
	m, cmd := m.sessionPickerCmd()
	// The user submits a prompt while discovery is still pending.
	m.pending = true
	msg, ok := cmd().(sessionPickerLoadedMsg)
	if !ok {
		t.Fatal("discovery did not return a picker message")
	}
	updated, _ := m.updateModel(msg)
	next := updated.(model)
	if next.picker != nil {
		t.Fatal("a picker was installed while a run was active")
	}
	if !transcriptContains(next.transcript, "out of date") {
		t.Fatalf("the stale result was not explained: %+v", next.transcript)
	}
	// And the selection route itself refuses, regardless of how it was reached.
	next, text, _ := next.startResumeCommand("b")
	if !strings.Contains(text, "Cannot resume sessions while a run is active") {
		t.Fatalf("a local selection during a run was not refused: %q", text)
	}
	if next.activeSession.SessionID != "a" {
		t.Fatalf("active session switched to %q under a live run", next.activeSession.SessionID)
	}
}

func TestAPickerResultForAnotherSessionOrRequestIsDiscarded(t *testing.T) {
	t.Run("session changed before the result", func(t *testing.T) {
		m, store := twoResumableSessions(t)
		m, cmd := m.sessionPickerCmd()
		metaB, err := store.Get("b")
		if err != nil {
			t.Fatal(err)
		}
		m.activeSession = *metaB
		updated, _ := m.updateModel(cmd())
		if next := updated.(model); next.picker != nil {
			t.Fatal("a result made for session a was installed on session b")
		}
	})
	t.Run("newer request supersedes the older", func(t *testing.T) {
		m, _ := twoResumableSessions(t)
		m, first := m.sessionPickerCmd()
		m, second := m.sessionPickerCmd()
		updated, _ := m.updateModel(first())
		if next := updated.(model); next.picker != nil {
			t.Fatal("a superseded request's result was installed")
		}
		updated, _ = m.updateModel(second())
		if next := updated.(model); next.picker == nil {
			t.Fatal("the current request's result was not installed")
		}
	})
	t.Run("idle selection still works", func(t *testing.T) {
		m, _ := twoResumableSessions(t)
		m, cmd := m.sessionPickerCmd()
		updated, _ := m.updateModel(cmd())
		next := updated.(model)
		if next.picker == nil {
			t.Fatal("an idle request's result was not installed")
		}
		next, text, _ := next.startResumeCommand("b")
		if text != "" || next.activeSession.SessionID != "b" {
			t.Fatalf("idle selection failed: text=%q active=%q", text, next.activeSession.SessionID)
		}
	})
}

// THE JOIN THE SANITIZER HAS TO SURVIVE: a stored tool argument is decoded by
// argHint/firstArgValue when the resumed tool row is drawn. A foreign input
// whose path is the JSON-escaped "ESC[2J FORGED ghp_AAAA…" is clean as encoded
// bytes and hostile once decoded, so this imports a transcript, reads the
// persisted event back and runs the renderer's own extraction on it.
func TestAResumedToolRowCannotDecodeAnImportedEscapeOrCredential(t *testing.T) {
	pat := "ghp_" + strings.Repeat("A", 36)
	home := t.TempDir()
	transcript := filepath.Join(home, ".claude", "projects", "-w", "hostile.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	// Spelled as the six-character JSON escape on purpose: that is the form the
	// foreign file carries, and the form the sanitizer could not see through.
	line := `{"type":"user","cwd":"/w","sessionId":"hostile","message":{"role":"user","content":"go"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"path":"\u001b[2J FORGED ghp_` + strings.Repeat("A", 36) + `"}}]}}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	env := agentsessions.Env{Home: home, Getenv: func(name string) string {
		if name == "CLAUDE_CONFIG_DIR" {
			return filepath.Join(home, ".claude")
		}
		return ""
	}}
	store := testSessionStore(t)
	result, err := agentsessions.Import(store, agentsessions.ClaudeCode(env), "hostile", agentsessions.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ReadEvents(result.Session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	for _, event := range events {
		if event.Type != sessions.EventToolCall {
			continue
		}
		checked = true
		hint := argHint(payloadString(sessionPayload(event), "arguments"))
		if strings.Contains(hint, "\x1b") {
			t.Fatalf("the resumed tool row decodes to a live escape: %q", hint)
		}
		if strings.Contains(hint, pat) {
			t.Fatalf("the resumed tool row decodes to the credential: %q", hint)
		}
		if !strings.Contains(hint, "FORGED") {
			t.Fatalf("the ordinary argument text was lost: %q", hint)
		}
	}
	if !checked {
		t.Fatal("no tool call was imported")
	}
}

// The post-import note compares a transcript-supplied cwd. It must complete
// through the shared lexical policy; on Windows that is what keeps a UNC path
// from being dialled, which is pinned in agentsessions where the resolver seam
// lives. Here the value flows through the real note.
func TestTheImportNoteComparesAForeignWorkspaceWithoutResolvingIt(t *testing.T) {
	result := agentsessions.ImportResult{
		Session: sessions.Metadata{SessionID: "zero-1"},
		Source:  agentsessions.ForeignSession{Agent: "claude-code", ID: "abc", Cwd: `\\server\share\repo`},
		Events:  3,
	}
	note := importedSessionNote(result, t.TempDir())
	if !strings.Contains(note, "It ran in") || !strings.Contains(note, `\\server\share\repo`) {
		t.Fatalf("note = %q", note)
	}
}
