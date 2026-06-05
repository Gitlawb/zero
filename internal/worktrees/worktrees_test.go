package worktrees

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPrepareCreatesDetachedGitWorktree(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{},
		},
	}

	result, err := Prepare(context.Background(), Options{
		Cwd:     root,
		Name:    "review-api",
		BaseDir: base,
		Now:     fixedTime("2026-06-05T10:30:00Z"),
		RunGit:  runner.Run,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	if result.Name != "review-api" {
		t.Fatalf("Name = %q, want review-api", result.Name)
	}
	if result.RepoRoot != root || result.SourceBranch != "main" || result.SourceCommit != "abc1234" {
		t.Fatalf("unexpected result metadata: %#v", result)
	}
	if !strings.HasPrefix(result.Path, filepath.Join(base, "zero-worktree-")) {
		t.Fatalf("Path = %q, want under base %q", result.Path, base)
	}
	if got := runner.commandLine(3); got != "git worktree add --detach "+result.Path+" HEAD" {
		t.Fatalf("git worktree command = %q", got)
	}
}

func TestPrepareReusesExistingGitWorktree(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	sourceGit := filepath.Join(root, ".git")
	if err := os.MkdirAll(sourceGit, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(base, "zero-worktree-"+repoKey(root), "reuse-me")
	if err := os.MkdirAll(filepath.Join(existing, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{Stdout: sourceGit + "\n"},
			{Stdout: sourceGit + "\n"},
		},
	}

	result, err := Prepare(context.Background(), Options{
		Cwd:     root,
		Name:    "reuse-me",
		BaseDir: base,
		RunGit:  runner.Run,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	if !result.Reused {
		t.Fatalf("Reused = false, want true")
	}
	if result.Path != existing {
		t.Fatalf("Path = %q, want existing %q", result.Path, existing)
	}
	if len(runner.calls) != 5 {
		t.Fatalf("expected metadata git calls only, got %#v", runner.calls)
	}
}

func TestPrepareRejectsExistingWorktreeFromDifferentRepo(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	sourceGit := filepath.Join(root, ".git")
	otherGit := filepath.Join(t.TempDir(), ".git")
	for _, dir := range []string{sourceGit, otherGit} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	existing := filepath.Join(base, "zero-worktree-"+repoKey(root), "other-repo")
	if err := os.MkdirAll(filepath.Join(existing, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{Stdout: sourceGit + "\n"},
			{Stdout: otherGit + "\n"},
		},
	}

	_, err := Prepare(context.Background(), Options{
		Cwd:     root,
		Name:    "other-repo",
		BaseDir: base,
		RunGit:  runner.Run,
	})
	if err == nil || !strings.Contains(err.Error(), "different git repository") {
		t.Fatalf("expected different repository reuse error, got %v", err)
	}
}

func TestPrepareValidatesNameAndExistingDirectory(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
		},
	}

	if _, err := Prepare(context.Background(), Options{Cwd: root, Name: "../escape", BaseDir: base, RunGit: runner.Run}); err == nil || !strings.Contains(err.Error(), "worktree name") {
		t.Fatalf("expected invalid name error, got %v", err)
	}

	blocked := filepath.Join(base, "zero-worktree-"+repoKey(root), "blocked")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "file.txt"), []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), Options{Cwd: root, Name: "blocked", BaseDir: base, RunGit: runner.Run}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected non-empty directory error, got %v", err)
	}
}

func TestDefaultBaseDirUsesStateHome(t *testing.T) {
	home := t.TempDir()
	stateHome := filepath.Join(home, "state")
	got, err := DefaultBaseDir(map[string]string{
		"HOME":           home,
		"XDG_STATE_HOME": stateHome,
	})
	if err != nil {
		t.Fatalf("DefaultBaseDir returned error: %v", err)
	}
	if got != filepath.Join(stateHome, "zero", "worktrees") {
		t.Fatalf("DefaultBaseDir = %q", got)
	}
}

func TestDefaultBaseDirFallsBackForWindowsUserProfile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("USERPROFILE fallback is Windows-specific")
	}
	profile := `C:\Users\zero`
	got, err := DefaultBaseDir(map[string]string{"USERPROFILE": profile})
	if err != nil {
		t.Fatalf("DefaultBaseDir returned error: %v", err)
	}
	expected := filepath.Join(profile, "AppData", "Local", "zero", "worktrees")
	if filepath.Clean(got) != filepath.Clean(expected) {
		t.Fatalf("DefaultBaseDir = %q, want %q", got, expected)
	}
}

type fakeRunner struct {
	calls   []gitCall
	results []CommandResult
}

func (runner *fakeRunner) Run(ctx context.Context, dir string, args ...string) (CommandResult, error) {
	runner.calls = append(runner.calls, gitCall{dir: dir, args: append([]string{}, args...)})
	if len(runner.results) == 0 {
		return CommandResult{}, nil
	}
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result, nil
}

func (runner *fakeRunner) commandLine(index int) string {
	if index >= len(runner.calls) {
		return ""
	}
	return "git " + strings.Join(runner.calls[index].args, " ")
}

type gitCall struct {
	dir  string
	args []string
}

func fixedTime(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}
