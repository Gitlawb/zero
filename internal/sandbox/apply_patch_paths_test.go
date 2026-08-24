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
	} {
		request := Request{ToolName: "apply_patch", WorkspaceRoot: root, SideEffect: SideEffectWrite, Args: map[string]any{"patch": patch}}
		if block := applyPatchPathBlock(request); block != nil {
			t.Fatalf("%s: unexpected block %+v", name, block)
		}
	}
	for _, path := range []string{"../escape.js", ".."} {
		request := Request{ToolName: "apply_patch", WorkspaceRoot: root, SideEffect: SideEffectWrite, Args: map[string]any{"patch": structured(path)}}
		block := applyPatchPathBlock(request)
		if block == nil || block.Code != BlockOutsideWorkspace {
			t.Fatalf("%q must be blocked as traversal, got %+v", path, block)
		}
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
	patch := strings.Join([]string{"*** Begin Patch", "*** Update File: " + outside, "@@", "-a", "+b", "*** End Patch"}, "\n")
	paths := applyPatchRequestPaths(map[string]any{"patch": patch})
	if len(paths) != 1 || paths[0] != outside {
		t.Fatalf("absolute patch path must reach scope validation unchanged, got %v", paths)
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
