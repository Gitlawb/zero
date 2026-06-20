package tools

import (
	"slices"
	"testing"
)

func TestPatchHeaderPathsHandlesQuotedAndSpacedNames(t *testing.T) {
	// git C-quotes a path that contains a space; the old strings.Fields parse kept
	// only the first whitespace-delimited token ("a/dir/file" and the literal `name`
	// pieces would split), losing the real name. The whole post-prefix value, with a
	// trailing tab timestamp dropped and the quotes removed, must survive (L18).
	patch := "--- \"a/dir/file name.go\"\t2024-01-01 00:00:00\n" +
		"+++ \"b/dir/file name.go\"\n" +
		"@@ -1 +1 @@\n-old\n+new\n"
	got := patchHeaderPaths(patch)
	if !slices.Contains(got, "dir/file name.go") {
		t.Fatalf("quoted spaced path not extracted: %v", got)
	}
}

func TestPatchHeaderPathsUnspacedStillWorks(t *testing.T) {
	patch := "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n"
	got := patchHeaderPaths(patch)
	if !slices.Contains(got, "x.go") {
		t.Fatalf("plain path not extracted: %v", got)
	}
	// /dev/null (a deletion/creation sentinel) must not be reported as a target.
	if slices.Contains(got, "/dev/null") {
		t.Fatalf("/dev/null should not be a header path: %v", got)
	}
}
