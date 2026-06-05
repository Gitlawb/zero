package zerogit

import (
	"context"
	"strings"
	"testing"
)

func TestInspectSummarizesChangesAndRedactsDiff(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},
		{Stdout: "feature/m5\n"},
		{Stdout: "abc1234\n"},
		{Stdout: " M internal/verify/verify.go\n?? internal/zerogit/zerogit.go\n"},
		{Stdout: " internal/verify/verify.go | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n"},
		{Stdout: "diff --git a/internal/verify/verify.go b/internal/verify/verify.go\n+token sk-proj-abcdefghijklmnopqrstuvwxyz\n"},
	}}

	summary, err := Inspect(context.Background(), InspectOptions{
		Cwd:          root,
		MaxDiffBytes: 80,
		RunGit:       runner.Run,
	})
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}

	if summary.Root != root || summary.Branch != "feature/m5" || summary.Commit != "abc1234" {
		t.Fatalf("unexpected git metadata: %#v", summary)
	}
	if summary.Clean {
		t.Fatalf("Clean = true, want false")
	}
	if len(summary.Files) != 2 {
		t.Fatalf("expected two changed files, got %#v", summary.Files)
	}
	if summary.Files[0].Path != "internal/verify/verify.go" || summary.Files[0].Status != "modified" || !summary.Files[0].Unstaged {
		t.Fatalf("unexpected modified file summary: %#v", summary.Files[0])
	}
	if summary.Files[1].Path != "internal/zerogit/zerogit.go" || summary.Files[1].Status != "untracked" || !summary.Files[1].Untracked {
		t.Fatalf("unexpected untracked file summary: %#v", summary.Files[1])
	}
	if strings.Contains(summary.Diff, "sk-proj-abcdefghijklmnopqrstuvwxyz") || !strings.Contains(summary.Diff, "[REDACTED]") {
		t.Fatalf("expected redacted diff, got %q", summary.Diff)
	}
	if !summary.Truncated {
		t.Fatalf("expected diff to be marked truncated")
	}
	if got := runner.commandLine(3); got != "git status --short --untracked-files=all" {
		t.Fatalf("status command = %q", got)
	}
}

func TestCommitStagesAllChangesAndUsesGeneratedMessage(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},
		{Stdout: "main\n"},
		{Stdout: "abc1234\n"},
		{Stdout: " M internal/verify/verify.go\n?? internal/zerogit/zerogit.go\n"},
		{Stdout: " 2 files changed, 10 insertions(+)\n"},
		{Stdout: "diff --git a/internal/verify/verify.go b/internal/verify/verify.go\n"},
		{},
		{Stdout: "[main def5678] Update 2 files\n"},
		{Stdout: "def5678\n"},
	}}

	result, err := Commit(context.Background(), CommitOptions{
		Cwd:    root,
		RunGit: runner.Run,
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}

	if !result.Committed || result.CommitHash != "def5678" {
		t.Fatalf("unexpected commit result: %#v", result)
	}
	if result.Message == "" || len(result.Message) > 72 || !strings.Contains(result.Message, "2 files") {
		t.Fatalf("unexpected generated commit message: %q", result.Message)
	}
	if got := runner.commandLine(6); got != "git add -A" {
		t.Fatalf("stage command = %q", got)
	}
	if got := runner.commandLine(7); !strings.HasPrefix(got, "git commit -m ") {
		t.Fatalf("commit command = %q", got)
	}
}

func TestCommitDryRunDoesNotMutateRepository(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},
		{Stdout: "main\n"},
		{Stdout: "abc1234\n"},
		{Stdout: " M README.md\n"},
		{Stdout: " README.md | 1 +\n"},
		{Stdout: "diff --git a/README.md b/README.md\n"},
	}}

	result, err := Commit(context.Background(), CommitOptions{
		Cwd:     root,
		Message: "Update README",
		DryRun:  true,
		RunGit:  runner.Run,
	})
	if err != nil {
		t.Fatalf("Commit dry-run returned error: %v", err)
	}

	if result.Committed || !result.DryRun || result.Message != "Update README" {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	if len(runner.calls) != 6 {
		t.Fatalf("dry-run should only inspect changes, got calls %#v", runner.calls)
	}
}

func TestCommitRejectsCleanTreeAndInvalidMessage(t *testing.T) {
	root := t.TempDir()
	cleanRunner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},
		{Stdout: "main\n"},
		{Stdout: "abc1234\n"},
		{Stdout: ""},
		{Stdout: ""},
		{Stdout: ""},
	}}
	if _, err := Commit(context.Background(), CommitOptions{Cwd: root, Message: "Update", RunGit: cleanRunner.Run}); err == nil || !strings.Contains(err.Error(), "no changes") {
		t.Fatalf("expected clean tree error, got %v", err)
	}
	if err := ValidateMessage("   "); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected message validation error, got %v", err)
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
