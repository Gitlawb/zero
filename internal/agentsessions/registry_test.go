package agentsessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/sessions"
)

type invalidImportAdapter struct{}

func (invalidImportAdapter) Name() string { return "invalid" }
func (invalidImportAdapter) Discover(string) ([]ForeignSession, error) {
	return []ForeignSession{{Agent: "invalid", ID: "broken", Title: "broken"}}, nil
}

func TestImportBindsMetadataAndContentToExactDiscoveredTranscript(t *testing.T) {
	home := t.TempDir()
	older := filepath.Join(home, ".claude", "projects", "-old", "duplicate.jsonl")
	newer := filepath.Join(home, ".claude", "projects", "-new", "duplicate.jsonl")
	writeFile(t, older, `{"type":"user","cwd":"/old","sessionId":"duplicate","message":{"role":"user","content":"older content","model":"older-model"}}`+"\n")
	writeFile(t, newer, `{"type":"user","cwd":"/new","sessionId":"duplicate","message":{"role":"user","content":"newer content","model":"newer-model"}}`+"\n")
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	adapter := ClaudeCode(testEnv(home, nil))
	found, err := adapter.Discover("")
	if err != nil || len(found) != 2 {
		t.Fatalf("discover duplicate IDs: %v (%d results)", err, len(found))
	}
	if found[0].Cwd != "/new" {
		t.Fatalf("newest selected row = %+v, want /new", found[0])
	}
	store := sessions.NewStore(sessions.StoreOptions{RootDir: filepath.Join(t.TempDir(), "sessions")})
	result, err := ImportSource(store, adapter, found[0], ReadOptions{})
	if err != nil {
		t.Fatalf("ImportSource: %v", err)
	}
	if result.Session.Cwd != "/new" || result.Session.SourceModelID != "newer-model" {
		t.Fatalf("imported metadata = %+v, want selected /new transcript", result.Session)
	}
	events, err := store.ReadEvents(result.Session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	encodedEvents, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedEvents), "newer content") || strings.Contains(string(encodedEvents), "older content") {
		t.Fatalf("imported events did not come from selected transcript: %+v", events)
	}

	if _, err := Import(store, adapter, "duplicate", ReadOptions{}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ID-only import error = %v, want duplicate ambiguity", err)
	}
	listed, problems := DiscoverAll(testEnv(home, nil), "")
	for _, session := range listed {
		if session.Agent == "claude-code" && session.ID == "duplicate" {
			t.Fatalf("ambiguous duplicate was presented as importable: %+v", session)
		}
	}
	if !strings.Contains(fmt.Sprint(problems), "ambiguous") {
		t.Fatalf("duplicate discovery problems = %v, want ambiguity", problems)
	}
}

func TestImportRejectsTranscriptChangedAfterDiscovery(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "projects", "-w", "changing.jsonl")
	writeFile(t, path, `{"type":"user","cwd":"/w","sessionId":"changing","message":{"role":"user","content":"before"}}`+"\n")
	adapter := ClaudeCode(testEnv(home, nil))
	found, err := adapter.Discover("")
	if err != nil || len(found) != 1 {
		t.Fatalf("discover: %v (%d results)", err, len(found))
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"assistant","cwd":"/w","message":{"role":"assistant","content":"after"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	store := sessions.NewStore(sessions.StoreOptions{RootDir: filepath.Join(t.TempDir(), "sessions")})
	if _, err := ImportSource(store, adapter, found[0], ReadOptions{}); err == nil || !strings.Contains(err.Error(), "changed after discovery") {
		t.Fatalf("changed-source import error = %v", err)
	}
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("changed source left an imported session: %+v", metas)
	}
}
func (invalidImportAdapter) Read(ForeignSession, ReadOptions) ([]sessions.AppendEventInput, error) {
	return []sessions.AppendEventInput{{Type: sessions.EventMessage, Payload: map[string]any{"invalid": make(chan int)}}}, nil
}

// WHAT THE STORE HOLDS IS WHAT EVERY CONSUMER DRAWS. The import used
// stripControl on the title and nothing at all on the cwd, which left two
// separate hazards in the record itself: stripControl deliberately keeps
// newlines — right for a transcript line, wrong for a label rendered as one row
// — and it does not redact, so a title (usually the user's first prompt, which
// is exactly where a pasted key lands) stayed a live credential for `zero
// sessions list`, the /resume picker and the import summary to leak
// independently. Sanitizing per consumer is how one of them gets forgotten;
// this pins the chokepoint instead.
func TestAnImportedSessionStoresADisplaySafeTitleAndCwd(t *testing.T) {
	home := t.TempDir()
	transcript := filepath.Join(home, ".claude", "projects", "-w", "hostile.jsonl")
	// \u001b, \u0007 and \u000d are how a control byte actually reaches these
	// fields: encoding/json rejects a raw one inside a string, so a transcript
	// that carries an escape carries it escaped.
	writeFile(t, transcript, strings.Join([]string{
		`{"type":"user","cwd":"/w/\u001b[2Kmoved\u000dhidden/proj","sessionId":"hostile","message":{"role":"user","content":"hi","model":"claude\u001b[2K-opus\nsk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAA"}}`,
		`{"type":"ai-title","aiTitle":"deploy\u0007 it\nwith key sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAA","sessionId":"hostile"}`,
	}, "\n")+"\n")

	adapter := ClaudeCode(testEnv(home, nil))
	store := sessions.NewStore(sessions.StoreOptions{RootDir: filepath.Join(t.TempDir(), "sessions")})
	result, err := Import(store, adapter, "hostile", ReadOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// Exact values, not "contains no escape": the readable text has to survive,
	// or a sanitizer that deleted the field would pass. The newline became a
	// SPACE rather than vanishing, which is what leaves the word boundary the
	// secret pattern anchors on — see DisplayField.
	const wantTitle = "deploy it with key [REDACTED]"
	if result.Session.Title != wantTitle {
		t.Errorf("stored title = %q, want %q", result.Session.Title, wantTitle)
	}
	// The escape is gone entirely and the return left a space behind it, so the
	// stored path is one line and still legible.
	const wantCwd = "/w/[2Kmoved hidden/proj"
	if result.Session.Cwd != wantCwd {
		t.Errorf("stored cwd = %q, want %q", result.Session.Cwd, wantCwd)
	}
	const wantWorkspaceKey = "/w/\x1b[2Kmoved\rhidden/proj"
	if result.Session.WorkspaceKey != filepath.Clean(wantWorkspaceKey) {
		t.Errorf("workspace key = %q, want %q", result.Session.WorkspaceKey, filepath.Clean(wantWorkspaceKey))
	}
	const wantModel = "claude[2K-opus [REDACTED]"
	if result.Session.ModelID != "" || result.Session.SourceModelID != wantModel {
		t.Errorf("stored operational/source model = %q / %q, want empty / %q", result.Session.ModelID, result.Session.SourceModelID, wantModel)
	}

	// And the record on disk, not merely the value handed back: the metadata is
	// re-read by every later `zero sessions` verb and by /resume.
	reloaded, err := store.Get(result.Session.SessionID)
	if err != nil || reloaded == nil {
		t.Fatalf("reloading the imported session: %v", err)
	}
	if reloaded.Title != wantTitle || reloaded.Cwd != wantCwd || reloaded.WorkspaceKey != filepath.Clean(wantWorkspaceKey) || reloaded.ModelID != "" || reloaded.SourceModelID != wantModel {
		t.Errorf("reloaded title/cwd/operational/source model = %q / %q / %q / %q, want %q / %q / empty / %q",
			reloaded.Title, reloaded.Cwd, reloaded.ModelID, reloaded.SourceModelID, wantTitle, wantCwd, wantModel)
	}
	if got := sessions.OperationalCwd(*reloaded); got != filepath.Clean(wantWorkspaceKey) {
		t.Errorf("operational cwd = %q, want canonical key %q", got, filepath.Clean(wantWorkspaceKey))
	}
	if got := sessions.OperationalCwd(sessions.Metadata{Cwd: "/legacy/workspace"}); got != "/legacy/workspace" {
		t.Errorf("older metadata operational cwd = %q", got)
	}
}

// A TITLE IS THE USER'S FIRST PROMPT, so a credential in it arrives on a line of
// its own far more often than inline — and the separator is what decided whether
// redaction fired. Every secret pattern anchors on \b, so deleting the newline
// glued the preceding word onto the shape and the match stopped happening;
// "key:" happened to still match because a colon is already a boundary, which is
// how a test written with punctuation would have passed while the real case
// leaked. Both spellings are pinned here.
func TestACredentialAfterALineBreakIsStillRedactedInAMetadataField(t *testing.T) {
	key := "sk-ant-api03-" + strings.Repeat("A", 24)
	for _, separator := range []struct {
		name  string
		value string
	}{
		{name: "newline", value: "\n"},
		{name: "tab", value: "\t"},
		{name: "carriage return", value: "\r"},
	} {
		t.Run(separator.name, func(t *testing.T) {
			// "key" ends in a word character, so deleting the separator destroys
			// the boundary. This is the spelling that leaked.
			got := DisplayField("rotate the key" + separator.value + key)
			if strings.Contains(got, key) {
				t.Errorf("a credential after a %s reached a display field verbatim: %q", separator.name, got)
			}
			if got != "rotate the key [REDACTED]" {
				t.Errorf("DisplayField = %q, want %q", got, "rotate the key [REDACTED]")
			}
		})
	}
}

func TestImportRemovesSessionWhenAppendingEventsFails(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := Import(store, invalidImportAdapter{}, "broken", ReadOptions{}); err == nil {
		t.Fatal("import with an unencodable event unexpectedly succeeded")
	}
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("failed import left a durable session behind: %+v", metas)
	}
}
