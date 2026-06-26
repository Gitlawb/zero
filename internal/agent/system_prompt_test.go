package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
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
		"search the web before answering",
		"do not recognize",
		"how each requirement is met",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected core system prompt to include %q, got:\n%s", want, buildSystemPrompt(Options{}))
		}
	}
}

func TestSystemPromptExplainsSandboxEscalationForHiddenHostState(t *testing.T) {
	prompt := strings.ToLower(buildSystemPrompt(Options{}))
	for _, want := range []string{
		"sandboxed shell command returns only sandbox-local state",
		"sandbox_permissions: \"require_escalated\"",
		"prefix_rule",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected sandbox escalation guidance %q, got:\n%s", want, buildSystemPrompt(Options{}))
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

func TestBuildSystemPromptIncludesApprovedCommandPrefixes(t *testing.T) {
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	if _, err := store.GrantCommandPrefix(sandbox.CommandPrefixInput{ToolName: "bash", Prefix: []string{"git", "status"}}); err != nil {
		t.Fatalf("GrantCommandPrefix returned error: %v", err)
	}
	prompt := buildSystemPrompt(Options{Sandbox: sandbox.NewEngine(sandbox.EngineOptions{Store: store})})
	for _, want := range []string{"Approved Command Prefixes", "bash: git status"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
		}
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

func TestBuildSystemPromptInjectsProjectGuidelinesCaseInsensitive(t *testing.T) {
	// Git tracks AGENTS.MD (uppercase MD) on a case-sensitive filesystem; the
	// loader must still resolve it when the cwd lookup uses lowercase.
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.MD"), []byte("Always run `make lint`."), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt(Options{Cwd: cwd})
	if !strings.Contains(prompt, "## Project guidelines (AGENTS.MD)") {
		t.Fatalf("expected case-insensitive AGENTS.MD resolution, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "make lint") {
		t.Fatalf("expected AGENTS.MD content injected, got:\n%s", prompt)
	}
}

func TestBuildSystemPromptProjectGuidelinesPathWalkingMonorepo(t *testing.T) {
	// Simulate a monorepo: root + sub-tree each have their own AGENTS.md.
	// The user launches Zero from the sub-tree, so both files should be
	// injected in general-to-specific order (root first, cwd last).
	root := t.TempDir()
	leaf := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Repo-wide: prefer Go."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "AGENTS.md"), []byte("API: follow REST conventions."), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt(Options{Cwd: leaf})
	normalized := filepath.ToSlash(prompt)
	rootLabel := "Project guidelines (AGENTS.md)"
	leafLabel := "Project guidelines (services/api/AGENTS.md)"
	rootBlock := systemPromptTestBlock(t, normalized, rootLabel, leafLabel)
	leafBlock := systemPromptTestBlock(t, normalized, leafLabel, "## Repo map")
	if !strings.Contains(rootBlock, "Repo-wide: prefer Go.") {
		t.Fatalf("expected root AGENTS.md in general-to-specific slot, got:\n%s", rootBlock)
	}
	if !strings.Contains(leafBlock, "API: follow REST conventions.") {
		t.Fatalf("expected leaf AGENTS.md in specific slot, got:\n%s", leafBlock)
	}
	// Root must appear before leaf in the prompt.
	rootIdx := strings.Index(normalized, "## "+rootLabel)
	leafIdx := strings.Index(normalized, "## "+leafLabel)
	if rootIdx < 0 || leafIdx < 0 || rootIdx > leafIdx {
		t.Fatalf("expected root (general) before leaf (specific) in prompt, got root=%d leaf=%d", rootIdx, leafIdx)
	}
}

func TestBuildSystemPromptProjectGuidelinesZeroFallback(t *testing.T) {
	// ZERO.md is the second-priority name at each level; the loader picks it
	// when no AGENTS.md is present.
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "ZERO.md"), []byte("Brand-specific rule."), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt(Options{Cwd: cwd})
	if !strings.Contains(prompt, "## Project guidelines (ZERO.md)") {
		t.Fatalf("expected ZERO.md fallback to be injected, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Brand-specific rule.") {
		t.Fatalf("expected ZERO.md content, got:\n%s", prompt)
	}
}

func TestBuildSystemPromptProjectGuidelinesProjectLocalFallback(t *testing.T) {
	cwd := t.TempDir()
	dot := filepath.Join(cwd, ".zero")
	if err := os.MkdirAll(dot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dot, "AGENTS.md"), []byte("Personal: use dark theme."), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt(Options{Cwd: cwd})
	// Without a git root, the label collapses to the basename; the test
	// confirms the .zero/AGENTS.md file's content is the one injected, not
	// any other file.
	if !strings.Contains(prompt, "Personal: use dark theme.") {
		t.Fatalf("expected .zero/AGENTS.md content, got:\n%s", prompt)
	}
	// The project guidelines block must be present (regardless of label).
	if !strings.Contains(prompt, "## Project guidelines (") {
		t.Fatalf("expected a project guidelines block, got:\n%s", prompt)
	}
}

func TestBuildSystemPromptProjectGuidelinesTruncatesAtTotalCap(t *testing.T) {
	// Root file fits in the per-file cap; leaf file is bigger than what's
	// left of the total budget, so it must be truncated. The truncation
	// marker must appear in the prompt and the full untruncated payload
	// must not.
	root := t.TempDir()
	leaf := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	rootContent := strings.Repeat("r", maxProjectContextBytes)        // exactly per-file cap
	leafContent := strings.Repeat("L", maxProjectContextTotalBytes+1) // over the total cap
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(rootContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "AGENTS.md"), []byte(leafContent), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt(Options{Cwd: leaf})
	if !strings.Contains(prompt, "truncated") {
		t.Fatalf("expected truncation marker on second file, prompt length=%d", len(prompt))
	}
	// Sanity: the leaf file's full payload must NOT be present untruncated.
	if strings.Contains(prompt, strings.Repeat("L", maxProjectContextTotalBytes-1)) {
		t.Fatalf("leaf file appears untruncated; total cap is not enforced")
	}
}

func TestProjectGuidelineDirsOrdersRootToLeaf(t *testing.T) {
	root := filepath.Join("r")
	leaf := filepath.Join(root, "a", "b")
	got := projectGuidelineDirs(leaf, root)
	want := []string{root, filepath.Join(root, "a"), leaf}
	if len(got) != len(want) {
		t.Fatalf("projectGuidelineDirs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("projectGuidelineDirs[%d] = %q, want %q (full %v)", i, got[i], want[i], got)
		}
	}
}

func TestProjectGuidelineDirsCollapsesToCwdWithoutGitRoot(t *testing.T) {
	got := projectGuidelineDirs(filepath.Join("some", "path"), "")
	if len(got) != 1 || got[0] != filepath.Join("some", "path") {
		t.Fatalf("projectGuidelineDirs = %v, want [some/path]", got)
	}
}

func TestFindProjectContextFileCaseInsensitiveBasename(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.MD"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := findProjectContextFile(cwd)
	if filepath.Base(got) != "AGENTS.MD" {
		t.Fatalf("findProjectContextFile = %q, want basename AGENTS.MD", got)
	}
}

func TestFindProjectGitRootIgnoresEmptyGitDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(root, "child")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findProjectGitRoot(leaf); got != "" {
		t.Fatalf("findProjectGitRoot = %q, want empty for invalid .git directory", got)
	}
}
