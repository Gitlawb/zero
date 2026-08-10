package agentsessions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
)

// TestImportedControlBytesAreStripped pins the terminal-injection fix: a foreign
// transcript is attacker-influenced, and an ESC or NUL in a message must not
// survive into a transcript line or picker row (the #835/#876 class). The
// content is built with Go escapes and JSON-encoded so the transcript carries
// the real control bytes.
func TestImportedControlBytesAreStripped(t *testing.T) {
	malicious := "before\x1b[2J\x1b[1;1H FORGED \x00\x07 after"
	line, err := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": malicious},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, string(line))

	events, err := translateFamily1(path, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Inspect the payload strings DIRECTLY, not a json.Marshal of the events —
	// JSON encoding would escape a surviving control byte to "" and hide it.
	var contentSeen string
	for _, event := range events {
		payload, ok := event.Payload.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range payload {
			s, ok := field.(string)
			if !ok {
				continue
			}
			if strings.ContainsAny(s, "\x1b\x00\x07") {
				t.Errorf("a control byte survived translation into a payload string: %q", s)
			}
			if strings.Contains(s, "before") {
				contentSeen = s
			}
		}
	}
	if contentSeen == "" || !strings.Contains(contentSeen, "after") {
		t.Errorf("stripping removed visible text, not just control bytes: %q", contentSeen)
	}
}

// TestStripControlKeepsTabAndNewline guards the one carve-out: transcripts
// legitimately carry tab and newline, and dropping them would mangle real text.
func TestStripControlKeepsTabAndNewline(t *testing.T) {
	if got := stripControl("a\tb\nc\x1bd\x00e"); got != "a\tb\ncde" {
		t.Errorf("stripControl = %q, want tab and newline kept and ESC/NUL dropped", got)
	}
}

// TestActivitySummaryIsAMessageNotACompaction pins the replay-contract fix.
// EventCompaction drives RehydrateEvents, which hoists a bookkeeping-less summary
// to the front of the transcript; the activity summary must not be that type.
func TestActivitySummaryIsAMessageNotACompaction(t *testing.T) {
	note := noteEvent("Prior session activity: 1 tool call.")
	if note.Type == sessions.EventCompaction {
		t.Fatal("activity summary is EventCompaction — RehydrateEvents will hoist it to the transcript front")
	}
	if note.Type != sessions.EventMessage {
		t.Fatalf("activity summary type = %s, want EventMessage", note.Type)
	}
	if !NoteEventIsSummary(note.Payload) {
		t.Fatal("activity summary carries no marker, so nothing can tell it from a real assistant turn")
	}
}
