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

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
