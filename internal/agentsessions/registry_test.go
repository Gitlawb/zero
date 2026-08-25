package agentsessions

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
)

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
		`{"type":"user","cwd":"/w/\u001b[2Kmoved\u000dhidden/proj","sessionId":"hostile","message":{"role":"user","content":"hi"}}`,
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

	// And the record on disk, not merely the value handed back: the metadata is
	// re-read by every later `zero sessions` verb and by /resume.
	reloaded, err := store.Get(result.Session.SessionID)
	if err != nil || reloaded == nil {
		t.Fatalf("reloading the imported session: %v", err)
	}
	if reloaded.Title != wantTitle || reloaded.Cwd != wantCwd {
		t.Errorf("reloaded title/cwd = %q / %q, want %q / %q",
			reloaded.Title, reloaded.Cwd, wantTitle, wantCwd)
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
