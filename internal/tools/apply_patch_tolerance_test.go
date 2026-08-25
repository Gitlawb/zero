package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Models routinely decorate the structured markers ("*** Begin Patch ***") and
// write unified-diff ranges after "@@"; both used to fail on the first line and
// pushed the model into whole-file rewrites.
func TestApplyPatchToleratesDecoratedMarkersAndRangeHeaders(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "hello.txt"), "hello\nold\nbye\n")

	patch := strings.Join([]string{
		"*** Begin Patch ***",
		"*** Update File: hello.txt",
		"@@ -1,3 +1,3 @@",
		" hello",
		"-old",
		"+new",
		"*** End Patch ***",
		"",
	}, "\n")

	if !isStructuredPatch(patch) {
		t.Fatal("decorated begin marker must still be recognised as a structured patch")
	}
	result := NewScopedApplyPatchTool(root, nil).Run(context.Background(), map[string]any{"patch": patch})
	if result.Status != StatusOK {
		t.Fatalf("decorated structured patch should apply, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ReplaceAll(string(content), "\r\n", "\n"); got != "hello\nnew\nbye\n" {
		t.Fatalf("patched content = %q", got)
	}
}

func TestStructuredHunkAnchorKeepsHeadingAndPlainContext(t *testing.T) {
	cases := map[string]string{
		"-12,4 +12,6":                  "",
		"-12 +12":                      "",
		"-12,4 +12,6 @@":               "",
		"-12,4 +12,6 @@ func main() {": "func main() {",
		"func main() {":                "func main() {",
		"":                             "",
	}
	for input, want := range cases {
		if got := structuredHunkAnchor(input); got != want {
			t.Fatalf("structuredHunkAnchor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStructuredPatchMarkerSpellings(t *testing.T) {
	begin := []string{"*** Begin Patch", "*** Begin Patch ***", "***Begin Patch", "  *** Begin Patch  ", "*** Begin Patch **"}
	for _, line := range begin {
		if structuredPatchMarker(line) != "begin" {
			t.Fatalf("%q must read as the begin marker", line)
		}
	}
	if structuredPatchMarker("*** End Patch ***") != "end" {
		t.Fatal("decorated end marker must read as the end marker")
	}
	for _, line := range []string{"*** Update File: x", "Begin Patch", "*** Begin Patchwork"} {
		if structuredPatchMarker(line) != "" {
			t.Fatalf("%q must not read as a marker", line)
		}
	}
}

func TestStructuredPatchHeaderErrorCarriesFormatHint(t *testing.T) {
	_, err := parseStructuredPatch("--- a/x\n+++ b/x\n")
	if err == nil || !strings.Contains(err.Error(), "*** Update File: path") {
		t.Fatalf("header error should teach the format, got %v", err)
	}
}

func TestApplyPatchAcceptsAbsoluteInWorkspacePaths(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "hello.txt"), "hello\nold\n")
	absolute := filepath.Join(root, "hello.txt")

	structured := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + absolute,
		"@@",
		" hello",
		"-old",
		"+structured",
		"*** End Patch",
		"",
	}, "\n")
	result := NewScopedApplyPatchTool(root, nil).Run(context.Background(), map[string]any{"patch": structured})
	if result.Status != StatusOK {
		t.Fatalf("absolute in-workspace structured path should apply, got %s: %s", result.Status, result.Output)
	}
	content, _ := os.ReadFile(absolute)
	if got := strings.ReplaceAll(string(content), "\r\n", "\n"); got != "hello\nstructured\n" {
		t.Fatalf("structured content = %q", got)
	}
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "hello.txt" {
		t.Fatalf("changed files should be workspace-relative, got %v", result.ChangedFiles)
	}

	unified := strings.Join([]string{
		"--- " + absolute,
		"+++ " + absolute,
		"@@ -1,2 +1,2 @@",
		" hello",
		"-structured",
		"+unified",
		"",
	}, "\n")
	result = NewScopedApplyPatchTool(root, nil).Run(context.Background(), map[string]any{"patch": unified})
	if result.Status != StatusOK {
		t.Fatalf("absolute in-workspace unified path should apply, got %s: %s", result.Status, result.Output)
	}
	content, _ = os.ReadFile(absolute)
	if got := strings.ReplaceAll(string(content), "\r\n", "\n"); got != "hello\nunified\n" {
		t.Fatalf("unified content = %q", got)
	}
}

func TestApplyPatchStillRejectsAbsolutePathsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")
	writeTestFile(t, outside, "old\n")

	for name, patch := range map[string]string{
		"structured": strings.Join([]string{"*** Begin Patch", "*** Update File: " + outside, "@@", "-old", "+new", "*** End Patch", ""}, "\n"),
		"unified":    strings.Join([]string{"--- " + outside, "+++ " + outside, "@@ -1 +1 @@", "-old", "+new", ""}, "\n"),
	} {
		result := NewScopedApplyPatchTool(root, nil).Run(context.Background(), map[string]any{"patch": patch})
		if result.Status == StatusOK {
			t.Fatalf("%s patch outside the workspace must be rejected", name)
		}
		if content, _ := os.ReadFile(outside); string(content) != "old\n" {
			t.Fatalf("%s patch must not touch a file outside the workspace", name)
		}
	}
}

func TestRelativizeUnifiedPatchPathsLeavesHunkBodiesAlone(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "notes.txt")
	patch := strings.Join([]string{
		"diff --git " + absolute + " " + absolute,
		"--- " + absolute,
		"+++ " + absolute,
		"@@ -1,2 +1,2 @@",
		"-see " + absolute,
		"+see " + absolute + " (kept)",
		" --- not a header",
		"",
	}, "\n")
	got := relativizeUnifiedPatchPaths(root, patch)
	want := strings.Join([]string{
		"diff --git a/notes.txt b/notes.txt",
		"--- a/notes.txt",
		"+++ b/notes.txt",
		"@@ -1,2 +1,2 @@",
		"-see " + absolute,
		"+see " + absolute + " (kept)",
		" --- not a header",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("relativized patch mismatch:\n%s\n--- want ---\n%s", got, want)
	}
	if plain := "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n"; relativizeUnifiedPatchPaths(root, plain) != plain {
		t.Fatal("a patch without absolute paths must be returned unchanged")
	}
	crlf := "--- " + absolute + "\r\n+++ " + absolute + "\r\n@@ -1 +1 @@\r\n-a\r\n+b\r\n"
	if got, want := relativizeUnifiedPatchPaths(root, crlf), "--- a/notes.txt\r\n+++ b/notes.txt\r\n@@ -1 +1 @@\r\n-a\r\n+b\r\n"; got != want {
		t.Fatalf("CRLF patch must keep its line endings:\n%q\nwant\n%q", got, want)
	}
}
