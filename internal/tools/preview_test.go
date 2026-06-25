package tools

import (
	"strings"
	"testing"
)

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
