package agentsessions

import (
	"strings"
	"testing"
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
