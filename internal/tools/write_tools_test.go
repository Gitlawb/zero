package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreToolsExposeWriteAndPlanTools(t *testing.T) {
	toolset := CoreTools(t.TempDir())
	byName := make(map[string]Tool, len(toolset))
	for _, tool := range toolset {
		byName[tool.Name()] = tool
	}

	for _, name := range []string{"write_file", "edit_file", "apply_patch"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("expected core tools to include %s", name)
		}
		if tool.Safety().SideEffect != SideEffectWrite {
			t.Fatalf("%s side effect = %s, want write", name, tool.Safety().SideEffect)
		}
		if tool.Safety().Permission != PermissionPrompt {
			t.Fatalf("%s permission = %s, want prompt", name, tool.Safety().Permission)
		}
	}

	planTool, ok := byName["update_plan"]
	if !ok {
		t.Fatalf("expected core tools to include update_plan")
	}
	if planTool.Safety().Permission != PermissionAllow {
		t.Fatalf("update_plan permission = %s, want allow", planTool.Safety().Permission)
	}
}

func TestRegistryBlocksPromptToolsWithoutGrant(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "blocked.txt")
	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))

	result := registry.Run(context.Background(), "write_file", map[string]any{
		"path":    "blocked.txt",
		"content": "nope",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "Permission required for write_file") {
		t.Fatalf("expected permission error, got %q", result.Output)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file to remain absent, stat err=%v", err)
	}
}

func TestRegistryRunsPromptToolsWithGrant(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))

	result := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":    "allowed.txt",
		"content": "hello",
	}, RunOptions{PermissionGranted: true})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "allowed.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("expected written content, got %q", string(content))
	}
}

func TestWriteFileToolCreatesAndProtectsExistingFiles(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)

	created := tool.Run(context.Background(), map[string]any{
		"path":    "nested/file.txt",
		"content": "first",
	})
	if created.Status != StatusOK {
		t.Fatalf("expected create ok, got %s: %s", created.Status, created.Output)
	}
	if !strings.Contains(created.Output, "Created nested/file.txt") {
		t.Fatalf("unexpected create output: %q", created.Output)
	}

	refused := tool.Run(context.Background(), map[string]any{
		"path":    "nested/file.txt",
		"content": "second",
	})
	if refused.Status != StatusError {
		t.Fatalf("expected overwrite refusal, got %s", refused.Status)
	}
	content, err := os.ReadFile(filepath.Join(root, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first" {
		t.Fatalf("expected original content, got %q", string(content))
	}

	overwrote := tool.Run(context.Background(), map[string]any{
		"path":      "nested/file.txt",
		"content":   "second",
		"overwrite": true,
	})
	if overwrote.Status != StatusOK {
		t.Fatalf("expected overwrite ok, got %s: %s", overwrote.Status, overwrote.Output)
	}
	content, err = os.ReadFile(filepath.Join(root, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "second" {
		t.Fatalf("expected overwritten content, got %q", string(content))
	}
}

func TestWriteFileToolAllowsEmptyContent(t *testing.T) {
	root := t.TempDir()

	result := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path":    "empty.txt",
		"content": "",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "empty.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "" {
		t.Fatalf("expected empty file, got %q", string(content))
	}
}

func TestWriteFileToolReportsTypeErrorsForEmptyAllowedStrings(t *testing.T) {
	result := NewWriteFileTool(t.TempDir()).Run(context.Background(), map[string]any{
		"path":    "bad.txt",
		"content": 42,
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "content must be a string") {
		t.Fatalf("expected string type error, got %q", result.Output)
	}
}

func TestWriteFileToolRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	result := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path":    outside,
		"content": "secret",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must stay inside the workspace") {
		t.Fatalf("expected workspace error, got %q", result.Output)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("expected outside file to remain absent, stat err=%v", err)
	}
}

func TestWriteFileToolRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.MkdirAll(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDirectory, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	result := NewWriteFileTool(root).Run(context.Background(), map[string]any{
		"path":    "link/escape.txt",
		"content": "secret",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must not traverse symlink") {
		t.Fatalf("expected symlink error, got %q", result.Output)
	}
	if _, err := os.Stat(filepath.Join(realDirectory, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected symlink target file to remain absent, stat err=%v", err)
	}
}

func TestEditFileToolReplacesExactStrings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "code.go")
	writeTestFile(t, path, "const a = 1\nconst b = 2\n")

	result := NewEditFileTool(root).Run(context.Background(), map[string]any{
		"path":       "code.go",
		"old_string": "const a = 1",
		"new_string": "const a = 42",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected edit ok, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "const a = 42\nconst b = 2\n" {
		t.Fatalf("unexpected edited content: %q", string(content))
	}
}

func TestEditFileToolAllowsDeletingRegions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	writeTestFile(t, path, "keep\nremove\nkeep\n")

	result := NewEditFileTool(root).Run(context.Background(), map[string]any{
		"path":       "notes.txt",
		"old_string": "remove\n",
		"new_string": "",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected edit ok, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep\nkeep\n" {
		t.Fatalf("unexpected edited content: %q", string(content))
	}
}

func TestEditFileToolRejectsMissingAndAmbiguousMatches(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dup.txt")
	writeTestFile(t, path, "x\nx\n")
	tool := NewEditFileTool(root)

	missing := tool.Run(context.Background(), map[string]any{
		"path":       "dup.txt",
		"old_string": "missing",
		"new_string": "y",
	})
	if missing.Status != StatusError || !strings.Contains(missing.Output, "Could not find") {
		t.Fatalf("expected missing error, got %s: %s", missing.Status, missing.Output)
	}

	ambiguous := tool.Run(context.Background(), map[string]any{
		"path":       "dup.txt",
		"old_string": "x",
		"new_string": "y",
	})
	if ambiguous.Status != StatusError || !strings.Contains(ambiguous.Output, "matches 2 locations") {
		t.Fatalf("expected ambiguity error, got %s: %s", ambiguous.Status, ambiguous.Output)
	}

	all := tool.Run(context.Background(), map[string]any{
		"path":        "dup.txt",
		"old_string":  "x",
		"new_string":  "y",
		"replace_all": true,
	})
	if all.Status != StatusOK {
		t.Fatalf("expected replace_all ok, got %s: %s", all.Status, all.Output)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "y\ny\n" {
		t.Fatalf("expected all replacements, got %q", string(content))
	}
}

func TestApplyPatchToolAppliesUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "hello.txt"), "hello\nold\n")
	patch := strings.Join([]string{
		"diff --git a/hello.txt b/hello.txt",
		"--- a/hello.txt",
		"+++ b/hello.txt",
		"@@ -1,2 +1,2 @@",
		" hello",
		"-old",
		"+new",
		"",
	}, "\n")

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{
		"patch": patch,
	})

	if result.Status != StatusOK {
		t.Fatalf("expected patch ok, got %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(content), "\r\n", "\n") != "hello\nnew\n" {
		t.Fatalf("unexpected patched content: %q", string(content))
	}
}

func TestApplyPatchToolRejectsSymlinkPath(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.MkdirAll(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDirectory, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	patch := strings.Join([]string{
		"diff --git a/link/new.txt b/link/new.txt",
		"new file mode 100644",
		"index 0000000..e965047",
		"--- /dev/null",
		"+++ b/link/new.txt",
		"@@ -0,0 +1 @@",
		"+hello",
		"",
	}, "\n")

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{
		"patch": patch,
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must not traverse symlink") {
		t.Fatalf("expected symlink error, got %q", result.Output)
	}
	if _, err := os.Stat(filepath.Join(realDirectory, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected symlink target file to remain absent, stat err=%v", err)
	}
}

func TestApplyPatchToolRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{
		"cwd": outside,
		"patch": strings.Join([]string{
			"diff --git a/nope.txt b/nope.txt",
			"--- a/nope.txt",
			"+++ b/nope.txt",
			"@@ -0,0 +1 @@",
			"+nope",
			"",
		}, "\n"),
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must stay inside the workspace") {
		t.Fatalf("expected workspace error, got %q", result.Output)
	}
}
