package agentsessions

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

// A NORMALIZER THAT REMOVES BYTES IS ALSO A REASSEMBLER, SO IT CANNOT RUN LAST.
//
// redact() strips control bytes and matches secrets by shape. The order decides
// whether it works at all: matching first lets a transcript split a credential
// with a byte the stripper then deletes, rejoining the halves after the patterns
// have already declined to match them. Every shape leaked that way.
//
// This is the same defect as #835, where an MCP failure reason was redacted
// before the terminal sanitizer rejoined its halves, so the test is written to
// fail loudly rather than to describe the current behaviour.
//
// A foreign transcript is untrusted input by construction, which is what makes
// this worth a dedicated test: the whole feature is reading one.
func TestASecretSplitByAControlByteIsStillRedacted(t *testing.T) {
	secrets := []struct {
		name  string
		value string
	}{
		// Shapes redaction recognizes. Synthetic, and long enough to match the
		// real patterns rather than a near-miss that would pass for the wrong
		// reason.
		{name: "anthropic key", value: "sk-ant-api03-" + strings.Repeat("A", 24)},
		{name: "github pat", value: "ghp_" + strings.Repeat("B", 36)},
		{name: "aws access key", value: "AKIA" + strings.Repeat("C", 16)},
	}
	// Every byte stripControl removes, because each one rejoins the halves. Tab
	// and newline are deliberately absent: those survive stripping, so they
	// separate rather than reassemble.
	splitters := []struct {
		name string
		byte string
	}{
		{name: "NUL", byte: "\x00"},
		{name: "ESC", byte: "\x1b"},
		{name: "backspace", byte: "\x08"},
		{name: "DEL", byte: "\x7f"},
		{name: "C1 (0x85)", byte: string(rune(0x85))},
	}

	for _, secret := range secrets {
		t.Run(secret.name, func(t *testing.T) {
			// The control arm. If redaction cannot catch the unsplit value then
			// the split cases below would pass for the wrong reason.
			if got := redact("token " + secret.value + " end"); strings.Contains(got, secret.value) {
				t.Fatalf("redaction does not recognize this shape at all, so the split cases prove nothing: %q", got)
			}

			for _, splitter := range splitters {
				t.Run(splitter.name, func(t *testing.T) {
					// An empty splitter would make every Contains check below vacuously
					// true. Assert it rather than trust the literal survived editing.
					if splitter.byte == "" {
						t.Fatal("splitter byte is empty; the literal was lost and this case proves nothing")
					}
					half := len(secret.value) / 2
					split := secret.value[:half] + splitter.byte + secret.value[half:]

					got := redact("token " + split + " end")

					// The assertion is about the text a READER ends up with. The
					// splitter is gone by then either way, so checking for the
					// intact secret in the output is checking exactly what would
					// reach a picker row or a transcript line.
					if strings.Contains(got, secret.value) {
						t.Errorf("a credential split by %s was reassembled after redaction and reached the output: %q", splitter.name, got)
					}
					if strings.Contains(got, splitter.byte) {
						t.Errorf("the control byte survived into the output: %q", got)
					}
				})
			}
		})
	}
}

// The counterpart, so the fix cannot be "strip everything and call it redaction".
// A separator that survives stripping does NOT rejoin, and the text around a
// secret has to come through intact either way.
func TestRedactKeepsTheSurroundingText(t *testing.T) {
	got := redact("before sk-ant-api03-" + strings.Repeat("A", 24) + " after")
	for _, want := range []string{"before", "after"} {
		if !strings.Contains(got, want) {
			t.Errorf("redaction ate the surrounding text, leaving %q", got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("the secret was not redacted at all: %q", got)
	}
	// A newline separates rather than rejoins, so the halves must NOT become the
	// secret, and the newline itself is legitimate transcript content.
	//
	// THE COMMENT ABOVE WAS THE ONLY THING ASSERTING THE FIRST HALF OF THAT. The
	// newline check alone passes just as well if the matcher spans the newline
	// and redacts both halves as one secret — the separator would survive inside
	// a "[REDACTED]" that ate the text around it. Both halves are named here, and
	// so is the absence of any redaction at all, because the failure this guards
	// against is over-redaction: a credential cannot contain a raw newline, so
	// treating a newline-split pair as one destroys legitimate transcript content
	// while protecting nothing. It is also what stops stripControl being widened
	// to strip newlines, which would make the NUL case above pass for the wrong
	// reason.
	halves := []string{"sk-ant-api03-", strings.Repeat("A", 24)}
	split := redact("before " + halves[0] + "\n" + halves[1] + " after")
	if !strings.Contains(split, "\n") {
		t.Errorf("a newline was stripped from transcript text: %q", split)
	}
	for _, half := range halves {
		if !strings.Contains(split, half) {
			t.Errorf("a newline-separated half %q was consumed as part of a secret, leaving %q", half, split)
		}
	}
	if strings.Contains(split, "[REDACTED]") {
		t.Errorf("two halves separated by a newline were redacted as one secret: %q", split)
	}
}

// A FORMAT CHARACTER IS NOT A CONTROL CHARACTER, and unicode.IsControl agrees —
// which is the problem. U+202E RIGHT-TO-LEFT OVERRIDE reorders everything after
// it, so a title or a tool name can be made to render as something entirely
// different while every byte stays innocent: "gnp.txt.exe" preceded by an
// override reads as an image file. Category Cf is invisible by definition and
// nothing in a transcript needs it.
func TestABidiOverrideIsStrippedFromTitlesAndToolNames(t *testing.T) {
	for _, hidden := range []string{"\u202e", "\u200b", "\u2066", "\u2069", "\ufeff"} {
		title := "deploy " + hidden + "gnp.txt.exe"
		if got := DisplayField(title); strings.Contains(got, hidden) {
			t.Errorf("a format character %q survived DisplayField: %q", hidden, got)
		}
		toolName := "read" + hidden + "_file"
		if got := stripControl(toolName); strings.Contains(got, hidden) {
			t.Errorf("a format character %q survived stripControl: %q", hidden, got)
		}
	}
	// The legible text still comes through — this is stripping, not deletion.
	if got := DisplayField("deploy \u202egnp.txt.exe"); !strings.Contains(got, "gnp.txt.exe") {
		t.Errorf("stripping the override ate the filename: %q", got)
	}
	// And a newline in a transcript body is still legitimate content, so
	// stripControl must not have widened into it.
	if got := stripControl("line one\nline two"); !strings.Contains(got, "\n") {
		t.Errorf("stripControl removed a legitimate newline: %q", got)
	}
}

// THE OTHER DIRECTION OF THE SAME COMPOSITION. Stripping controls can assemble
// a split key the patterns could not see (the test above), and it can also
// ERASE the word boundary an intact key needs: "progress\rsk-ant-…" was a
// recognizable key after a carriage return and became "progresssk-ant-…", a
// mid-word run \bsk-ant- refuses to match, so the whole key persisted through
// messageEvent. Both must hold at once; reversing the two calls would trade one
// for the other, so redaction runs on both sides of normalization.
func TestAnIntactKeyAfterARemovedSeparatorIsStillRedacted(t *testing.T) {
	keys := []struct{ name, value string }{
		{"anthropic key", "sk-ant-api03-" + strings.Repeat("A", 24)},
		{"github pat", "ghp_" + strings.Repeat("B", 36)},
		{"aws access key", "AKIA" + strings.Repeat("C", 16)},
	}
	separators := []struct{ name, value string }{{"CR", "\r"}, {"NUL", "\x00"}, {"ESC", "\x1b"}, {"DEL", "\x7f"}, {"C1", "\u0085"}}
	for _, key := range keys {
		for _, sep := range separators {
			input := "progress" + sep.value + key.value + " done"
			for _, probe := range []struct {
				name string
				got  string
			}{
				{"redact", redact(input)},
				{"message", str(t, messageEvent("user", input), "content")},
				{"tool result", str(t, toolResultEvent(&importCallIdentities{}, "bash", "c1", "ok", input), "output")},
			} {
				if strings.Contains(probe.got, key.value) {
					t.Errorf("%s / %s / %s: intact key survived: %q", key.name, sep.name, probe.name, probe.got)
				}
				if !strings.Contains(probe.got, "progress") || !strings.Contains(probe.got, "done") {
					t.Errorf("%s / %s / %s: surrounding text was eaten: %q", key.name, sep.name, probe.name, probe.got)
				}
			}
		}
	}
	// Legitimate layout is still kept in transcript text.
	if got := redact("line one\nline\ttwo"); got != "line one\nline\ttwo" {
		t.Errorf("newline/tab were not preserved: %q", got)
	}
}

func TestCombinedRemovedSeparatorsCannotHideSplitCredentials(t *testing.T) {
	secret := "ghp_" + strings.Repeat("A", 36)
	for _, separator := range []struct {
		name  string
		value string
	}{
		{name: "NUL", value: "\x00"},
		{name: "ESC", value: "\x1b"},
		{name: "DEL", value: "\x7f"},
		{name: "C1", value: "\u0085"},
	} {
		t.Run(separator.name, func(t *testing.T) {
			input := "progress" + separator.value + secret[:22] + separator.value + secret[22:]
			for _, probe := range []struct {
				name string
				got  string
			}{
				{name: "transcript", got: redact(input)},
				{name: "stored message", got: str(t, messageEvent("user", input), "content")},
				{name: "stored tool result", got: str(t, toolResultEvent(&importCallIdentities{}, "shell", "call", tools.StatusOK, input), "output")},
				{name: "display", got: DisplayField(input)},
			} {
				if strings.Contains(probe.got, secret) {
					t.Fatalf("%s reassembled and exposed a split credential: %q", probe.name, probe.got)
				}
				if !strings.Contains(probe.got, "progress") || !strings.Contains(probe.got, "[REDACTED]") {
					t.Fatalf("%s did not preserve readable text and a redaction marker: %q", probe.name, probe.got)
				}
			}
		})
	}

	input := "progress\x00" + secret[:22] + "\x00" + secret[22:]
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(sessions.CreateInput{SessionID: "combined_redaction"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvents(session.SessionID, []sessions.AppendEventInput{
		messageEvent("user", input),
		toolResultEvent(&importCallIdentities{}, "shell", "call", tools.StatusOK, input),
	}); err != nil {
		t.Fatal(err)
	}
	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		payload := string(event.Payload)
		if strings.Contains(payload, secret) || !strings.Contains(payload, "[REDACTED]") {
			t.Fatalf("persisted %s payload did not retain the redaction boundary: %s", event.Type, payload)
		}
	}
}

func TestDisplayFieldDoesNotLeakCredentialFragmentsAcrossNormalizedSeparators(t *testing.T) {
	key := "sk-proj-" + strings.Repeat("B", 87)
	separators := []struct {
		name  string
		value string
	}{
		{name: "ESC", value: "\x1b"},
		{name: "NUL", value: "\x00"},
		{name: "NEL", value: "\u0085"},
		{name: "zero width space", value: "\u200b"},
		{name: "right-to-left override", value: "\u202e"},
		{name: "carriage return", value: "\r"},
		{name: "tab", value: "\t"},
	}

	for _, separator := range separators {
		t.Run(separator.name, func(t *testing.T) {
			got := DisplayField("problem " + key[:48] + separator.value + key[48:] + " end")
			assertNoCredentialRun(t, got, key, 8)
			if !strings.Contains(got, "problem") || !strings.Contains(got, "end") || !strings.Contains(got, "[REDACTED]") {
				t.Fatalf("DisplayField did not retain readable context and a redaction marker: %q", got)
			}
		})
	}
}

func assertNoCredentialRun(t *testing.T, got, credential string, runLength int) {
	t.Helper()
	for start := 0; start+runLength <= len(credential); start++ {
		run := credential[start : start+runLength]
		if strings.Contains(got, run) {
			t.Fatalf("output leaked credential run %q at byte %d: %q", run, start, got)
		}
	}
}
