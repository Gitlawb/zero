package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A COPIED NUL KILLED THE WHOLE PROGRAM ON WINDOWS.
//
// github.com/atotto/clipboard's Windows path calls syscall.StringToUTF16, which
// PANICS rather than returning an error when the string contains a NUL — so
// selecting transcript text that carried one took the TUI down with "program
// experienced a panic" and a stack ending in clipboard.writeAll. A teammate hit
// it on a real Windows machine.
//
// The NUL reaches the clipboard because ansi.Strip removes escape SEQUENCES and
// leaves C0 bytes alone, and the transcript carries them from two directions:
// tool output (binary reads, git's -z listings) and this package's own card
// protocol, whose prefixes are literally "\x00command-card\x00".
func TestNulNeverReachesTheClipboard(t *testing.T) {
	// The card prefix is a real, in-repo source: ansi.Strip leaves it intact.
	if !strings.ContainsRune(ansi.Strip(commandCardTranscriptPrefix+"Profile"), 0) {
		t.Fatal("setup: the card prefix no longer carries a NUL; this test guards the wrong thing")
	}
	for _, tc := range []struct{ name, in string }{
		{"card prefix", commandCardTranscriptPrefix + "Profile\nstatus: ok"},
		{"plan card prefix", planCardTranscriptPrefix + "plan"},
		{"binary tool output", "before\x00after"},
		{"git -z listing", "a.go\x00b.go\x00c.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clipboardSafeText(tc.in)
			if strings.ContainsRune(got, 0) {
				t.Fatalf("a NUL survived to the clipboard write: %q — Windows would panic here", got)
			}
			// The readable content must survive: this sanitises, it does not truncate.
			for _, want := range strings.Split(strings.ReplaceAll(tc.in, "\x00", "\n"), "\n") {
				if want = strings.TrimSpace(want); want != "" && !strings.Contains(got, want) {
					t.Fatalf("sanitising dropped real content %q from %q", want, got)
				}
			}
		})
	}
}

// Newlines and tabs are legal clipboard content and must survive — a multi-line
// selection is the normal case, not the exception.
func TestClipboardSanitisingKeepsLayout(t *testing.T) {
	in := "line one\n\tindented\nline three"
	if got := clipboardSafeText(in); got != in {
		t.Fatalf("clean multi-line text was altered:\n got %q\nwant %q", got, in)
	}
	// Other C0 bytes go: they would terminate the OSC52 escape early and
	// corrupt the copy even where they do not panic.
	if got := clipboardSafeText("a\x07b\x1bc"); got != "abc" {
		t.Fatalf("control bytes survived: %q", got)
	}
}
