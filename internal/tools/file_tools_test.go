package tools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReadFileToolReadsLineRanges(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "notes.txt"), "alpha\nbeta\ngamma\ndelta")

	result := NewReadFileTool(root).Run(context.Background(), map[string]any{
		"path":       "notes.txt",
		"start_line": 2,
		"max_lines":  2,
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	for _, want := range []string{
		"File: notes.txt (lines 2-3 of 4)",
		"2 | beta",
		"3 | gamma",
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, result.Output)
		}
	}
	if strings.Contains(result.Output, "alpha") || strings.Contains(result.Output, "delta") {
		t.Fatalf("line range leaked outside requested slice: %q", result.Output)
	}
}

func TestReadFileToolRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeTestFile(t, outside, "secret")

	result := NewReadFileTool(root).Run(context.Background(), map[string]any{
		"path": outside,
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must stay inside the workspace") {
		t.Fatalf("expected workspace error, got %q", result.Output)
	}
}

func TestListDirectoryToolListsRecursivelyAndIgnoresJunk(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "src", "main.go"), "package main")
	writeTestFile(t, filepath.Join(root, "node_modules", "leftpad", "index.js"), "module.exports = 1")
	writeTestFile(t, filepath.Join(root, "README.md"), "# Zero")

	result := NewListDirectoryTool(root).Run(context.Background(), map[string]any{
		"path":      ".",
		"recursive": true,
		"max_depth": 2,
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "src/") || !strings.Contains(result.Output, "main.go") {
		t.Fatalf("expected recursive source listing, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "README.md") {
		t.Fatalf("expected README.md, got %q", result.Output)
	}
	if strings.Contains(result.Output, "node_modules") || strings.Contains(result.Output, "leftpad") {
		t.Fatalf("expected ignored junk directory, got %q", result.Output)
	}
}

func TestGlobToolFindsMatchesWithLimit(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.go"), "package zero")
	writeTestFile(t, filepath.Join(root, "nested", "b.go"), "package nested")
	writeTestFile(t, filepath.Join(root, "nested", "c.txt"), "text")

	result := NewGlobTool(root).Run(context.Background(), map[string]any{
		"pattern": "**/*.go",
		"limit":   1,
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	if result.Truncated != true {
		t.Fatalf("expected truncated result")
	}
	matchedPaths := regexp.MustCompile(`(?m)^[^\n]*\.go\b`).FindAllString(result.Output, -1)
	if got := len(matchedPaths); got != 1 {
		t.Fatalf("expected exactly one go match, got %d in %q", got, result.Output)
	}
}

func TestGlobToolCanIncludeDirectoryMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := NewGlobTool(root).Run(context.Background(), map[string]any{
		"pattern":      "src",
		"include_dirs": true,
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	if strings.TrimSpace(result.Output) != "src" {
		t.Fatalf("expected src directory match, got %q", result.Output)
	}
}

func TestGrepToolSearchesContent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "cmd", "main.go"), "package main\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(root, "README.md"), "main docs\n")

	result := NewGrepTool(root).Run(context.Background(), map[string]any{
		"pattern":    "func main",
		"path":       ".",
		"glob":       "**/*.go",
		"head_limit": 5,
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "cmd/main.go:2: func main() {}") {
		t.Fatalf("expected formatted grep result, got %q", result.Output)
	}
	if strings.Contains(result.Output, "README.md") {
		t.Fatalf("glob filter leaked README match: %q", result.Output)
	}
}

func TestGrepToolSupportsFilesAndCountModes(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.txt"), "needle\nneedle\n")
	writeTestFile(t, filepath.Join(root, "b.txt"), "needle\n")

	files := NewGrepTool(root).Run(context.Background(), map[string]any{
		"pattern":     "needle",
		"output_mode": "files_with_matches",
	})
	if files.Status != StatusOK {
		t.Fatalf("expected files result, got %s: %s", files.Status, files.Output)
	}
	if !strings.Contains(files.Output, "a.txt") || !strings.Contains(files.Output, "b.txt") {
		t.Fatalf("expected both files, got %q", files.Output)
	}

	count := NewGrepTool(root).Run(context.Background(), map[string]any{
		"pattern":     "needle",
		"output_mode": "count",
	})
	if count.Status != StatusOK {
		t.Fatalf("expected count result, got %s: %s", count.Status, count.Output)
	}
	if count.Output != "3 matches found" {
		t.Fatalf("expected count output, got %q", count.Output)
	}
}

// Finding 1: grep must not follow an in-workspace symlink that points to a file
// OUTSIDE the workspace (confinement bypass). The symlinked secret must not be
// searched or returned, mirroring read_file's EvalSymlinks confinement.
func TestGrepDoesNotFollowSymlinkOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, "secret.txt")
	writeTestFile(t, secret, "needle leaked from outside\n")
	writeTestFile(t, filepath.Join(root, "keep.txt"), "needle inside\n")

	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	res := NewGrepTool(root).Run(context.Background(), map[string]any{
		"pattern":     "needle",
		"output_mode": "content",
	})
	if res.Status != StatusOK {
		t.Fatalf("status=%s output=%s", res.Status, res.Output)
	}
	if strings.Contains(res.Output, "leaked from outside") || strings.Contains(res.Output, "escape.txt") {
		t.Fatalf("grep followed symlink outside workspace, leaked:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "keep.txt") {
		t.Fatalf("expected in-workspace match, got:\n%s", res.Output)
	}
}

// Finding 2: when the workspace root itself lives under a symlink (e.g. macOS
// /tmp -> /private/tmp), match paths must be clean workspace-relative paths with
// NO leading "../" — because the walked paths are EvalSymlinks-resolved while the
// root was previously only Abs-normalized.
func TestGrepReturnsCleanRelativePathsUnderSymlinkedRoot(t *testing.T) {
	realDir := t.TempDir()
	writeTestFile(t, filepath.Join(realDir, "pkg", "main.go"), "func main() {}\n")

	linkRoot := filepath.Join(t.TempDir(), "ws")
	if err := os.Symlink(realDir, linkRoot); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	res := NewGrepTool(linkRoot).Run(context.Background(), map[string]any{
		"pattern":     "func main",
		"output_mode": "content",
	})
	if res.Status != StatusOK {
		t.Fatalf("status=%s output=%s", res.Status, res.Output)
	}
	if strings.Contains(res.Output, "../") || strings.HasPrefix(strings.TrimSpace(res.Output), "/") {
		t.Fatalf("expected clean workspace-relative path, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "pkg/main.go:1: func main") {
		t.Fatalf("expected pkg/main.go match, got:\n%s", res.Output)
	}

	// files_with_matches mode must likewise be clean-relative.
	res = NewGrepTool(linkRoot).Run(context.Background(), map[string]any{
		"pattern":     "func main",
		"output_mode": "files_with_matches",
	})
	if strings.TrimSpace(res.Output) != "pkg/main.go" {
		t.Fatalf("expected pkg/main.go, got %q", res.Output)
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrepSkipsAlwaysExcludedDirectories(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("keep.txt", "needle here")
	mustWrite(".git/config", "needle here")
	mustWrite(".zero/state.json", "needle here")
	mustWrite("node_modules/pkg/index.js", "needle here")
	mustWrite("vendor/pkg/lib.go", "needle here")
	mustWrite(".worktrees/branch/main.go", "needle here")

	res := NewGrepTool(root).Run(context.Background(), map[string]any{
		"pattern":     "needle",
		"output_mode": "files_with_matches",
	})
	if res.Status != StatusOK {
		t.Fatalf("status=%s output=%s", res.Status, res.Output)
	}
	for _, forbidden := range []string{".git", ".zero", "node_modules", "vendor", ".worktrees"} {
		if strings.Contains(res.Output, forbidden) {
			t.Fatalf("grep must not descend into excluded dir %q, got:\n%s", forbidden, res.Output)
		}
	}
	if !strings.Contains(res.Output, "keep.txt") {
		t.Fatalf("expected keep.txt in results, got:\n%s", res.Output)
	}
}

func TestGrepSkipsBinaryLikeFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "keep.txt"), "needle here")
	writeTestFile(t, filepath.Join(root, "image.png"), "needle hidden")
	writeTestFile(t, filepath.Join(root, "archive.zip"), "needle hidden")

	res := NewGrepTool(root).Run(context.Background(), map[string]any{
		"pattern":     "needle",
		"output_mode": "files_with_matches",
	})
	if res.Status != StatusOK {
		t.Fatalf("status=%s output=%s", res.Status, res.Output)
	}
	if strings.Contains(res.Output, "image.png") || strings.Contains(res.Output, "archive.zip") {
		t.Fatalf("grep must skip binary-like files, got:\n%s", res.Output)
	}
	if strings.TrimSpace(res.Output) != "keep.txt" {
		t.Fatalf("expected only keep.txt, got:\n%s", res.Output)
	}

	direct := NewGrepTool(root).Run(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "image.png",
	})
	if direct.Status != StatusOK {
		t.Fatalf("direct status=%s output=%s", direct.Status, direct.Output)
	}
	if strings.Contains(direct.Output, "image.png") || strings.Contains(direct.Output, "needle hidden") {
		t.Fatalf("direct binary-like grep should be skipped, got:\n%s", direct.Output)
	}
}

func TestGlobSkipsWorkspaceExcludedDirectoriesAndBinaryFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeTestFile(t, filepath.Join(root, ".zero", "state.json"), "{}")
	writeTestFile(t, filepath.Join(root, "vendor", "pkg", "lib.go"), "package lib")
	writeTestFile(t, filepath.Join(root, ".worktrees", "branch", "main.go"), "package main")
	writeTestFile(t, filepath.Join(root, "image.png"), "binary")

	res := NewGlobTool(root).Run(context.Background(), map[string]any{
		"pattern": "**/*",
		"limit":   20,
	})
	if res.Status != StatusOK {
		t.Fatalf("status=%s output=%s", res.Status, res.Output)
	}
	for _, forbidden := range []string{".zero", "vendor", ".worktrees", "image.png"} {
		if strings.Contains(res.Output, forbidden) {
			t.Fatalf("glob must skip %q, got:\n%s", forbidden, res.Output)
		}
	}
	if strings.TrimSpace(res.Output) != "keep.txt" {
		t.Fatalf("expected only keep.txt, got:\n%s", res.Output)
	}
}
