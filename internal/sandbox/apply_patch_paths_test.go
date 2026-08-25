package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPatchPathBlockOnlyRejectsRelativeTraversal(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "main.js")
	structured := func(path string) string {
		return strings.Join([]string{"*** Begin Patch", "*** Update File: " + path, "@@", "-a", "+b", "*** End Patch"}, "\n")
	}
	for name, patch := range map[string]string{
		"absolute inside workspace": structured(inside),
		"relative":                  structured("main.js"),
		"decorated markers":         "*** Begin Patch ***\n*** Update File: main.js\n@@\n-a\n+b\n*** End Patch ***",
		"no-space marker":           "***Begin Patch\n*** Update File: main.js\n@@\n-a\n+b\n***End Patch",
	} {
		request := Request{ToolName: "apply_patch", WorkspaceRoot: root, SideEffect: SideEffectWrite, Args: map[string]any{"patch": patch}}
		if block := applyPatchPathBlock(request); block != nil {
			t.Fatalf("%s: unexpected block %+v", name, block)
		}
	}
	for _, path := range []string{"../escape.js", ".."} {
		for name, patch := range map[string]string{"canonical": structured(path), "no-space": "***Begin Patch\n*** Update File: " + path + "\n@@\n-a\n+b\n***End Patch"} {
			request := Request{ToolName: "apply_patch", WorkspaceRoot: root, SideEffect: SideEffectWrite, Args: map[string]any{"patch": patch}}
			block := applyPatchPathBlock(request)
			if block == nil || block.Code != BlockOutsideWorkspace {
				t.Fatalf("%s %q must be blocked as traversal, got %+v", name, path, block)
			}
		}
	}
}

// Every marker spelling the tool applies must be classified as structured at
// the sandbox boundary too; otherwise the boundary scans the patch as a unified
// diff, extracts no targets, and validates nothing (fail-open).
func TestStructuredPatchClassifierMatchesToolSpellings(t *testing.T) {
	for _, header := range []string{"*** Begin Patch", "*** Begin Patch ***", "***Begin Patch", "  *** Begin Patch  ", "\ufeff*** Begin Patch"} {
		patch := header + "\n*** Update File: main.js\n@@\n-a\n+b\n*** End Patch"
		if !IsStructuredPatch(patch) {
			t.Fatalf("%q must classify as a structured patch", header)
		}
		if paths := applyPatchPaths(patch); len(paths) != 1 || paths[0] != "main.js" {
			t.Fatalf("%q: sandbox must extract the structured target, got %v", header, paths)
		}
	}
	for _, header := range []string{"--- a/x", "Begin Patch", "*** Begin Patchwork", "*** Update File: x"} {
		if IsStructuredPatch(header + "\n-a\n+b") {
			t.Fatalf("%q must not classify as a structured patch", header)
		}
	}
	if StructuredPatchMarker("*** End Patch ***") != "end" || StructuredPatchMarker("***End Patch") != "end" {
		t.Fatal("decorated end markers must classify as end")
	}
}

func TestApplyPatchRequestPathsCarryAbsolutePathsToScopeValidation(t *testing.T) {
	root := t.TempDir()
	// NewScope also grants the system temp dir, so a sibling t.TempDir() is
	// legitimately in scope; pick a path under the filesystem root instead.
	outside, err := filepath.Abs(filepath.Join(string(filepath.Separator), "zero-outside-workspace-test", "escape.js"))
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]string{
		"canonical": strings.Join([]string{"*** Begin Patch", "*** Update File: " + outside, "@@", "-a", "+b", "*** End Patch"}, "\n"),
		"no-space":  strings.Join([]string{"***Begin Patch", "*** Update File: " + outside, "@@", "-a", "+b", "***End Patch"}, "\n"),
	} {
		// structuredPatchHeaderPaths normalises separators to "/", so compare the
		// slash form; the absolute path itself must survive untouched.
		paths := applyPatchRequestPaths(map[string]any{"patch": patch})
		if len(paths) != 1 || paths[0] != filepath.ToSlash(outside) {
			t.Fatalf("%s: absolute patch path must reach scope validation unchanged, got %v", name, paths)
		}
	}
	scope, err := NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if block := scope.validate(outside); block == nil || block.Code != BlockOutsideWorkspace {
		t.Fatalf("scope must still deny an absolute path outside the workspace, got %+v", block)
	}
	if block := scope.validate(filepath.Join(root, "main.js")); block != nil {
		t.Fatalf("scope must accept an absolute path inside the workspace, got %+v", block)
	}
}
