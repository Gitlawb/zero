package tools

import (
	"strings"
	"testing"
)

func TestNewFileDiffPreview(t *testing.T) {
	content := strings.Repeat("line\n", 30) // 30 lines, > previewBodyLines
	got := newFileDiffPreview("site/app.js", content, 30, false)
	for _, want := range []string{"--- /dev/null", "+++ b/site/app.js", "@@ -0,0 +1,30 @@"} {
		if !strings.Contains(got, want) {
			t.Errorf("new-file preview missing %q:\n%s", want, got)
		}
	}
	if adds := strings.Count(got, "\n+"); adds < previewBodyLines {
		t.Errorf("expected at least %d added lines, got %d:\n%s", previewBodyLines, adds, got)
	}
	if !strings.Contains(got, "… +15 lines") {
		t.Errorf("expected a truncation trailer (30 > %d):\n%s", previewBodyLines, got)
	}
	// Overwrite of an existing file is not a create (--- a/, not /dev/null).
	if ov := newFileDiffPreview("x.txt", "a\nb", 2, true); !strings.Contains(ov, "--- a/x.txt") || strings.Contains(ov, "/dev/null") {
		t.Errorf("overwrite preview should not read as a create:\n%s", ov)
	}
}

func TestEditDiffPreview(t *testing.T) {
	content := "alpha\nbeta\ngamma\ndelta\n"
	got := editDiffPreview("m.go", content, "beta", "BETA")
	for _, want := range []string{"--- a/m.go", "+++ b/m.go", "-beta", "+BETA"} {
		if !strings.Contains(got, want) {
			t.Errorf("edit preview missing %q:\n%s", want, got)
		}
	}
	// The hunk should start at the matched line (beta is line 2).
	if !strings.Contains(got, "@@ -2,1 +2,1 @@") {
		t.Errorf("edit preview hunk should start at line 2:\n%s", got)
	}
}

func TestPreviewPreservesEmptyAndTrailingLines(t *testing.T) {
	// Empty content -> the 3 header lines only, no fake body line.
	if got := newFileDiffPreview("e.txt", "", 0, false); strings.Count(got, "\n")+1 != 3 {
		t.Errorf("empty file should be 3 header lines only, got:\n%s", got)
	}
	// Trailing blank lines preserved: "a\n\n\n" -> body +a,+,+ (3) atop 3 headers.
	if got := newFileDiffPreview("t.txt", "a\n\n\n", 3, false); strings.Count(got, "\n")+1 != 6 {
		t.Errorf("expected 3 headers + 3 body lines = 6, got:\n%s", got)
	}
	// splitDiffLines: empty -> nil; trailing "\n" artifact dropped; no-trailing kept.
	if got := splitDiffLines(""); got != nil {
		t.Errorf(`splitDiffLines("") = %v, want nil`, got)
	}
	if got := splitDiffLines("a\nb\n"); len(got) != 2 {
		t.Errorf("splitDiffLines should drop the trailing artifact, got %v", got)
	}
	if got := splitDiffLines("a\nb"); len(got) != 2 {
		t.Errorf("splitDiffLines with no trailing newline should keep all, got %v", got)
	}
}

func TestCapPreviewDiff(t *testing.T) {
	short := "--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n-a\n+b"
	if got := capPreviewDiff(short); got != short {
		t.Errorf("short diff should pass through unchanged:\n%s", got)
	}
	long := "--- a/x\n+++ b/x\n@@ -1,40 +1,40 @@\n" + strings.Repeat("+x\n", 40)
	got := capPreviewDiff(long)
	if !strings.Contains(got, "… +") {
		t.Errorf("long diff should be capped with a trailer:\n%s", got)
	}
	if strings.Count(got, "\n")+1 > previewBodyLines+5 {
		t.Errorf("capped diff should be bounded, got %d lines", strings.Count(got, "\n")+1)
	}
}
