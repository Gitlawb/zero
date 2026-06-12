package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreSystemPromptIncludesCodingQualityRules(t *testing.T) {
	prompt := strings.ToLower(buildSystemPrompt(Options{}))

	for _, want := range []string{
		"read-before-edit",
		"inspect the target file",
		"plan then act",
		"choose the narrowest tool",
		"prefer edit_file or apply_patch",
		"verify after edits",
		"honor the active permission mode",
		"avoid broad refactors",
		"final response",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected core system prompt to include %q, got:\n%s", want, buildSystemPrompt(Options{}))
		}
	}
}

func TestBuildSystemPromptIncludesWorkspaceSeedFromCwd(t *testing.T) {
	cwd := t.TempDir()
	writeSystemPromptTestFile(t, cwd, "go.mod", "module example.test/zero\n")
	writeSystemPromptTestFile(t, cwd, "AGENTS.md", "Use Go commands.\n")
	writeSystemPromptTestFile(t, cwd, "cmd/zero/main.go", "package main\n")
	writeSystemPromptTestFile(t, cwd, "internal/agent/loop.go", "package agent\n")
	writeSystemPromptTestFile(t, cwd, "node_modules/pkg/index.js", "ignored")
	writeSystemPromptTestFile(t, cwd, filepath.Join(".git", "HEAD"), "ref: refs/heads/feature/seed\n")

	prompt := buildSystemPrompt(Options{Cwd: cwd})

	for _, want := range []string{
		"<workspace_seed>",
		"Workspace context seed",
		"cwd: " + filepath.Base(cwd),
		"git: feature/seed",
		"layout: AGENTS.md, cmd/, go.mod, internal/",
		"project files: go.mod, AGENTS.md",
		"memory hints: AGENTS.md",
		"</workspace_seed>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected workspace seed to include %q, got:\n%s", want, prompt)
		}
	}
	seed := systemPromptTestBlock(t, prompt, "<workspace_seed>", "</workspace_seed>")
	if strings.Contains(seed, cwd) {
		t.Fatalf("workspace seed should use safe cwd label, not absolute path %q, got:\n%s", cwd, seed)
	}
	if strings.Contains(prompt, "node_modules") {
		t.Fatalf("workspace seed should inherit workspace skip rules, got:\n%s", prompt)
	}
}

func TestBuildSystemPromptOmitsWorkspaceSeedWithoutCwd(t *testing.T) {
	prompt := buildSystemPrompt(Options{})

	if strings.Contains(prompt, "<workspace_seed>") || strings.Contains(prompt, "Workspace context seed") {
		t.Fatalf("workspace seed should be absent without cwd, got:\n%s", prompt)
	}
}

func writeSystemPromptTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func systemPromptTestBlock(t *testing.T, prompt, start, end string) string {
	t.Helper()
	startIndex := strings.Index(prompt, start)
	if startIndex < 0 {
		t.Fatalf("missing block start %q", start)
	}
	afterStart := prompt[startIndex+len(start):]
	body, _, ok := strings.Cut(afterStart, end)
	if !ok {
		t.Fatalf("missing block end %q", end)
	}
	return body
}
