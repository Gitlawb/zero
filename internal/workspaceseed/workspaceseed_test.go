package workspaceseed

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildOrdersAndClassifiesWorkspaceSeed(t *testing.T) {
	dirty := true
	got := Build(Input{
		CWD:        filepath.Join("tmp", "Zero"),
		GitBranch:  "feat/context",
		GitDirty:   &dirty,
		GitSummary: "3 modified, 1 untracked",
		Paths: []string{
			"internal/agent/loop.go",
			"docs/WORK_SPLIT_PRD.md",
			"package.json",
			"cmd/zero/main.go",
			"AGENTS.md",
			"docs/PRD.md",
			"ZERO.md",
			"go.mod",
		},
	})

	if got.CWD != filepath.Clean(filepath.Join("tmp", "Zero")) {
		t.Fatalf("CWD=%q", got.CWD)
	}
	if got.GitBranch != "feat/context" {
		t.Fatalf("GitBranch=%q", got.GitBranch)
	}
	if got.GitSummary != "dirty: 3 modified, 1 untracked" {
		t.Fatalf("GitSummary=%q", got.GitSummary)
	}
	if want := []string{"AGENTS.md", "ZERO.md", "cmd/", "docs/", "go.mod", "internal/", "package.json"}; !reflect.DeepEqual(got.Layout, want) {
		t.Fatalf("Layout=%v want %v", got.Layout, want)
	}
	if want := []string{"go.mod", "package.json", "AGENTS.md", "docs/PRD.md", "docs/WORK_SPLIT_PRD.md"}; !reflect.DeepEqual(got.ProjectFiles, want) {
		t.Fatalf("ProjectFiles=%v want %v", got.ProjectFiles, want)
	}
	if want := []string{"AGENTS.md", "ZERO.md"}; !reflect.DeepEqual(got.MemoryFiles, want) {
		t.Fatalf("MemoryFiles=%v want %v", got.MemoryFiles, want)
	}
}

func TestBuildKeepsPathsRelativeToCWD(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Zero")
	dirty := false
	got := Build(Input{
		CWD:       root,
		GitBranch: "main",
		GitDirty:  &dirty,
		Paths: []string{
			filepath.Join(root, "go.mod"),
			filepath.Join(root, "docs", "PRD.md"),
			filepath.Join(filepath.Dir(root), "outside", "AGENTS.md"),
		},
	})

	rendered := Render(got, RenderOptions{MaxLines: 20, Width: 120})
	if strings.Contains(rendered, filepath.ToSlash(root)) || strings.Contains(rendered, root) {
		t.Fatalf("Render leaked absolute root %q in:\n%s", root, rendered)
	}
	if strings.Contains(rendered, "outside") {
		t.Fatalf("Render included path outside root:\n%s", rendered)
	}
	if !strings.Contains(rendered, "cwd: Zero") {
		t.Fatalf("Render should use safe cwd base, got:\n%s", rendered)
	}
	if got.GitSummary != "clean" {
		t.Fatalf("GitSummary=%q want clean", got.GitSummary)
	}
}

func TestBuildRejectsAbsolutePathsWithoutCWD(t *testing.T) {
	got := Build(Input{
		Paths: []string{"/home/alice/repo/go.mod", `C:\Users\alice\repo\package.json`},
	})

	if len(got.ProjectFiles) != 0 {
		t.Fatalf("ProjectFiles=%v want none", got.ProjectFiles)
	}
	if len(got.Layout) != 0 {
		t.Fatalf("Layout=%v want none", got.Layout)
	}
}

func TestBuildNormalizesBackslashPathsBeforeTraversalChecks(t *testing.T) {
	got := Build(Input{
		Paths: []string{
			`docs\PRD.md`,
			`..\..\etc\passwd`,
			`\\server\share\secret.txt`,
		},
	})

	if want := []string{"docs/"}; !reflect.DeepEqual(got.Layout, want) {
		t.Fatalf("Layout=%v want %v", got.Layout, want)
	}
	if want := []string{"docs/PRD.md"}; !reflect.DeepEqual(got.ProjectFiles, want) {
		t.Fatalf("ProjectFiles=%v want %v", got.ProjectFiles, want)
	}
}

func TestRenderHonorsLineAndWidthBudgets(t *testing.T) {
	seed := Build(Input{
		CWD:       "/repo/Zero",
		GitBranch: "feature/very-long-branch-name",
		Paths: []string{
			"averyveryveryverylongdirectoryname/averyveryveryverylongfilename.go",
			"cmd/zero/main.go",
			"docs/PRD.md",
			"docs/WORK_SPLIT_PRD.md",
			"go.mod",
			"package.json",
			"AGENTS.md",
			"ZERO.md",
		},
	})

	got := Render(seed, RenderOptions{MaxLines: 5, Width: 42})
	lines := strings.Split(got, "\n")
	if len(lines) > 5 {
		t.Fatalf("line count=%d want <= 5:\n%s", len(lines), got)
	}
	for _, line := range lines {
		if len(line) > 42 {
			t.Fatalf("line %q length=%d exceeds width", line, len(line))
		}
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("Render should indicate clipping when constrained:\n%s", got)
	}
}

func TestRenderClampsOnRuneBoundaries(t *testing.T) {
	got := clampLineNoFlag("café au lait", 7)

	if got != "café..." {
		t.Fatalf("clamped line = %q", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("clamped line split a rune: %q", got)
	}
}

func TestBuildFromWorkspaceUsesWorkspaceIndexWithoutGit(t *testing.T) {
	root := t.TempDir()
	writeSeedFile(t, root, "go.mod", "module example.test/zero\n")
	writeSeedFile(t, root, "cmd/zero/main.go", "package main\n")
	writeSeedFile(t, root, "docs/PRD.md", "# PRD\n")
	writeSeedFile(t, root, "node_modules/pkg/index.js", "ignored")

	got, err := BuildFromWorkspace(root, GitInfo{Branch: "main", Summary: "clean"})
	if err != nil {
		t.Fatalf("BuildFromWorkspace: %v", err)
	}

	if got.GitSummary != "clean" {
		t.Fatalf("GitSummary=%q want clean", got.GitSummary)
	}
	if want := []string{"go.mod", "docs/PRD.md"}; !reflect.DeepEqual(got.ProjectFiles, want) {
		t.Fatalf("ProjectFiles=%v want %v", got.ProjectFiles, want)
	}
	if contains(got.Layout, "node_modules/") {
		t.Fatalf("Layout should inherit workspaceindex skip rules, got %v", got.Layout)
	}
}

func writeSeedFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
