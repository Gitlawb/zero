package repomap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPromptIncludesRepoSummaryAndRelativePaths(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "Zero"))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	repo := RepoMap{
		Root: root,
		Files: []File{
			{Path: filepath.Join(root, "web", "app.ts")},
			{Path: filepath.Join(root, "internal", "repomap", "prompt.go")},
			{Path: filepath.Join(root, "README.md")},
			{Path: filepath.Join(root, "go.mod")},
			{Path: filepath.Join(root, "cmd", "zero", "main.go")},
		},
	}

	got := RenderPrompt(repo, 2048)

	for _, want := range []string{
		"Repo: Zero",
		"Counts: files=5 dirs=5",
		"Important files: README.md, go.mod",
		"Languages: Go=2, Markdown=1, TypeScript=1",
		"Extensions: .go=2, .md=1, .mod=1, .ts=1",
		"Files:",
		"  cmd/zero/main.go",
		"  internal/repomap/prompt.go",
		"  web/app.ts",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderPrompt() missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, root) || strings.Contains(got, filepath.ToSlash(root)) {
		t.Fatalf("RenderPrompt() leaked absolute root %q in:\n%s", root, got)
	}
}

func TestRenderPromptIsDeterministicAndDeduplicatesPaths(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "Zero"))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	left := RepoMap{
		Root: root,
		Files: []File{
			{Path: filepath.Join(root, "b", "two.go")},
			{Path: filepath.Join(root, "a", "one.go")},
			{Path: filepath.Join(root, "b", "two.go")},
		},
	}
	right := RepoMap{
		Root: root,
		Files: []File{
			{Path: filepath.Join(root, "a", "one.go")},
			{Path: filepath.Join(root, "b", "two.go")},
		},
	}

	gotLeft := RenderPrompt(left, 1024)
	gotRight := RenderPrompt(right, 1024)

	if gotLeft != gotRight {
		t.Fatalf("RenderPrompt() should be deterministic after sorting/deduping:\nleft:\n%s\nright:\n%s", gotLeft, gotRight)
	}
	if strings.Index(gotLeft, "  a/one.go") > strings.Index(gotLeft, "  b/two.go") {
		t.Fatalf("RenderPrompt() file list is not sorted:\n%s", gotLeft)
	}
	if strings.Count(gotLeft, "b/two.go") != 1 {
		t.Fatalf("RenderPrompt() should dedupe repeated paths:\n%s", gotLeft)
	}
}

func TestRenderPromptHonorsBudget(t *testing.T) {
	repo := RepoMap{
		Root: "Zero",
		Files: []File{
			{Path: "cmd/zero/main.go"},
			{Path: "internal/agent/loop.go"},
			{Path: "internal/agent/system_prompt.go"},
			{Path: "internal/repomap/prompt.go"},
			{Path: "internal/repomap/prompt_test.go"},
			{Path: "README.md"},
			{Path: "AGENTS.md"},
		},
	}

	for _, budget := range []int{1, 8, 32, 80, 120, 200} {
		got := RenderPrompt(repo, budget)
		if len(got) > budget {
			t.Fatalf("RenderPrompt() length=%d exceeds budget=%d:\n%s", len(got), budget, got)
		}
	}

	got := RenderPrompt(repo, 80)
	if got != "" && !strings.Contains(got, "[clipped]") {
		t.Fatalf("RenderPrompt() should mark clipped output when possible, got:\n%s", got)
	}
}

func TestRenderPromptReturnsEmptyForNonPositiveBudget(t *testing.T) {
	got := RenderPrompt(RepoMap{Root: "Zero", Files: []File{{Path: "main.go"}}}, 0)
	if got != "" {
		t.Fatalf("RenderPrompt() with zero budget=%q want empty", got)
	}
}
