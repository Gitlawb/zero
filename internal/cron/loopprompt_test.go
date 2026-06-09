package cron

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipesHaveValidSchedules(t *testing.T) {
	if len(Recipes()) == 0 {
		t.Fatal("expected preset recipes")
	}
	for _, r := range Recipes() {
		if _, err := Parse(r.Expr); err != nil {
			t.Fatalf("recipe %q has invalid expr %q: %v", r.ID, r.Expr, err)
		}
		if strings.TrimSpace(r.Prompt) == "" || strings.TrimSpace(r.ID) == "" {
			t.Fatalf("recipe %q missing id/prompt", r.ID)
		}
	}
	if _, ok := Recipe("git-recap"); !ok {
		t.Fatal("expected a git-recap recipe")
	}
	if _, ok := Recipe("nope"); ok {
		t.Fatal("unknown recipe must not resolve")
	}
}

func TestResolveLoopPromptPriority(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	// built-in fallback when nothing present
	got, err := ResolveLoopPrompt(cwd, home)
	if err != nil {
		t.Fatal(err)
	}
	if got != builtinLoopPrompt {
		t.Fatalf("expected built-in fallback, got %q", got)
	}
	// home/.zero/loop.md is used when cwd has none
	mustWrite(t, filepath.Join(home, ".zero", "loop.md"), "HOME PROMPT")
	if got, _ = ResolveLoopPrompt(cwd, home); got != "HOME PROMPT" {
		t.Fatalf("home loop.md not used, got %q", got)
	}
	// cwd/.zero/loop.md wins over home
	mustWrite(t, filepath.Join(cwd, ".zero", "loop.md"), "CWD PROMPT")
	if got, _ = ResolveLoopPrompt(cwd, home); got != "CWD PROMPT" {
		t.Fatalf("cwd loop.md should win, got %q", got)
	}
}

func TestResolveLoopPromptRejectsSymlinkAndCap(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	// oversize file -> error
	big := filepath.Join(cwd, ".zero", "loop.md")
	mustWrite(t, big, strings.Repeat("a", maxLoopPromptBytes+1))
	if _, err := ResolveLoopPrompt(cwd, home); err == nil {
		t.Fatal("expected error for oversize loop.md")
	}
	// symlink -> rejected (skipped), falls through to built-in
	cwd2 := t.TempDir()
	target := filepath.Join(cwd2, "real.md")
	mustWrite(t, target, "SECRET")
	link := filepath.Join(cwd2, ".zero", "loop.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unsupported")
	}
	got, err := ResolveLoopPrompt(cwd2, home)
	if err != nil {
		t.Fatal(err)
	}
	if got == "SECRET" {
		t.Fatal("symlinked loop.md must not be read")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
