package zerogit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/redaction"
)

func TestInspectSummarizesChangesAndRedactsDiff(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},
		{Stdout: "feature/m5\n"},
		{Stdout: "abc1234\n"},
		{Stdout: " M internal/verify/verify.go\n?? internal/zerogit/zerogit.go\n"},
		{Stdout: "abc1234\n"},
		{},
		{},
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
	if got := runner.commandLine(6); got != "git add -A" {
		t.Fatalf("preview stage command = %q", got)
	}
	if got := runner.commandLine(7); got != "git diff --cached --stat --" {
		t.Fatalf("preview diff stat command = %q", got)
	}
}

func TestCommitStagesAllChangesAndUsesGeneratedMessage(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},
		{Stdout: "main\n"},
		{Stdout: "abc1234\n"},
		{Stdout: " M internal/verify/verify.go\n?? internal/zerogit/zerogit.go\n"},
		{Stdout: "abc1234\n"},
		{},
		{},
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
	if got := runner.commandLine(9); got != "git add -A" {
		t.Fatalf("stage command = %q", got)
	}
	if got := runner.commandLine(10); !strings.HasPrefix(got, "git commit -m ") {
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
		{Stdout: "abc1234\n"},
		{},
		{},
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
	if len(runner.calls) != 9 {
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
		{Stdout: "abc1234\n"},
		{},
		{},
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

func TestInspectPreviewIncludesUntrackedOnlyChanges(t *testing.T) {
	root := initGitRepo(t, true)
	writeTestFile(t, filepath.Join(root, "notes.md"), "hello zero\n")

	summary, err := Inspect(context.Background(), InspectOptions{Cwd: root})
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}

	if summary.Clean {
		t.Fatalf("Clean = true, want false")
	}
	if len(summary.Files) != 1 || summary.Files[0].Path != "notes.md" || !summary.Files[0].Untracked {
		t.Fatalf("unexpected untracked summary: %#v", summary.Files)
	}
	if !strings.Contains(summary.DiffStat, "notes.md") {
		t.Fatalf("diff stat does not include untracked file: %q", summary.DiffStat)
	}
	if !strings.Contains(summary.Diff, "diff --git a/notes.md b/notes.md") || !strings.Contains(summary.Diff, "+hello zero") {
		t.Fatalf("diff does not include untracked file content: %q", summary.Diff)
	}
	if staged := runGitCommand(t, root, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "" {
		t.Fatalf("Inspect mutated the real index, staged files: %q", staged)
	}
}

func TestInspectPreviewWorksWithUnbornHead(t *testing.T) {
	root := initGitRepo(t, false)
	writeTestFile(t, filepath.Join(root, "README.md"), "new repository\n")

	summary, err := Inspect(context.Background(), InspectOptions{Cwd: root})
	if err != nil {
		t.Fatalf("Inspect returned error for unborn HEAD: %v", err)
	}

	if summary.Clean {
		t.Fatalf("Clean = true, want false")
	}
	if len(summary.Files) != 1 || summary.Files[0].Path != "README.md" || !summary.Files[0].Untracked {
		t.Fatalf("unexpected unborn HEAD summary: %#v", summary.Files)
	}
	if !strings.Contains(summary.DiffStat, "README.md") || !strings.Contains(summary.Diff, "+new repository") {
		t.Fatalf("unborn HEAD preview did not include README: stat=%q diff=%q", summary.DiffStat, summary.Diff)
	}
	if staged := runGitCommand(t, root, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "" {
		t.Fatalf("Inspect mutated the real unborn index, staged files: %q", staged)
	}
}

func TestInspectBaseRefEmptyUsesSnapshotPath(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},
		{Stdout: "main\n"},
		{Stdout: "abc1234\n"},
		{Stdout: " M README.md\n"},
		{Stdout: "abc1234\n"},
		{},
		{},
		{Stdout: " README.md | 1 +\n"},
		{Stdout: "diff --git a/README.md b/README.md\n"},
	}}

	summary, err := Inspect(context.Background(), InspectOptions{Cwd: root, RunGit: runner.Run})
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}
	if summary.Base != "" {
		t.Fatalf("Base = %q, want empty for default path", summary.Base)
	}
	if got := runner.commandLine(3); got != "git status --short --untracked-files=all" {
		t.Fatalf("default path must use git status, got %q", got)
	}
	if got := runner.commandLine(6); got != "git add -A" {
		t.Fatalf("default path must use snapshot index, got %q", got)
	}
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "...HEAD") {
			t.Fatalf("default path must not issue a three-dot diff, saw %q", joined)
		}
	}
}

func TestInspectBaseRefRealGitDiffsBranchAgainstBase(t *testing.T) {
	root := initGitRepo(t, true)
	baseRef := runGitCommand(t, root, "rev-parse", "HEAD")
	runGitCommand(t, root, "checkout", "-q", "-b", "feature")
	writeTestFile(t, filepath.Join(root, "feature.md"), "branch only\n")
	runGitCommand(t, root, "add", "feature.md")
	runGitCommand(t, root, "-c", "user.name=Zero", "-c", "user.email=zero@example.invalid", "commit", "-m", "Add feature")

	summary, err := Inspect(context.Background(), InspectOptions{Cwd: root, BaseRef: strings.TrimSpace(baseRef)})
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}
	if summary.Clean {
		t.Fatalf("Clean = true, want false")
	}
	if len(summary.Files) != 1 || summary.Files[0].Path != "feature.md" || summary.Files[0].Status != "added" {
		t.Fatalf("unexpected base diff files: %#v", summary.Files)
	}
	if summary.Branch != "feature" {
		t.Fatalf("Branch = %q, want feature (HEAD branch preserved)", summary.Branch)
	}
	if !strings.Contains(summary.Diff, "+branch only") {
		t.Fatalf("diff missing branch content: %q", summary.Diff)
	}
	if staged := runGitCommand(t, root, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "" {
		t.Fatalf("Inspect mutated the real index, staged files: %q", staged)
	}
}

func TestInspectBaseRefUsesThreeDotDiff(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},                                                   // rev-parse --show-toplevel
		{Stdout: "feature/m5\n"},                                                // rev-parse --abbrev-ref HEAD
		{Stdout: "abc1234\n"},                                                   // rev-parse --short HEAD
		{Stdout: "M\ta.txt\nA\tb.txt\n"},                                        // diff --name-status main...HEAD
		{Stdout: " a.txt | 1 +\n b.txt | 1 +\n 2 files changed, 2 insertions(+)\n"}, // diff --stat main...HEAD
		{Stdout: "diff --git a/internal/changes/changes.go b/internal/changes/changes.go\n+token sk-proj-abcdefghijklmnopqrstuvwxyz\n"}, // diff main...HEAD
	}}

	summary, err := Inspect(context.Background(), InspectOptions{
		Cwd:          root,
		BaseRef:      "main",
		MaxDiffBytes: 80,
		RunGit:       runner.Run,
	})
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}

	if summary.Base != "main" {
		t.Fatalf("Base = %q, want main", summary.Base)
	}
	if summary.Branch != "feature/m5" {
		t.Fatalf("Branch = %q, want feature/m5 (HEAD branch must be preserved)", summary.Branch)
	}
	if summary.Clean {
		t.Fatalf("Clean = true, want false")
	}
	if len(summary.Files) != 2 {
		t.Fatalf("expected two files from name-status, got %#v", summary.Files)
	}
	if summary.Files[0].Path != "a.txt" || summary.Files[0].Status != "modified" {
		t.Fatalf("unexpected first file: %#v", summary.Files[0])
	}
	if summary.Files[1].Path != "b.txt" || summary.Files[1].Status != "added" {
		t.Fatalf("unexpected second file: %#v", summary.Files[1])
	}
	if strings.Contains(summary.Diff, "sk-proj-abcdefghijklmnopqrstuvwxyz") || !strings.Contains(summary.Diff, "[REDACTED]") {
		t.Fatalf("expected redacted diff, got %q", summary.Diff)
	}
	if !summary.Truncated {
		t.Fatalf("expected diff to be marked truncated")
	}
	if got := runner.commandLine(3); got != "git diff --name-status main...HEAD --" {
		t.Fatalf("name-status command = %q", got)
	}
	if got := runner.commandLine(4); got != "git diff --stat main...HEAD --" {
		t.Fatalf("stat command = %q", got)
	}
	if got := runner.commandLine(5); got != "git diff main...HEAD --" {
		t.Fatalf("diff command = %q", got)
	}
}

func TestInspectBaseRefEmptyDiffIsClean(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: root + "\n"},
		{Stdout: "main\n"},
		{Stdout: "abc1234\n"},
		{Stdout: ""}, // diff --name-status (no changes vs base)
		{Stdout: ""}, // diff --stat
		{Stdout: ""}, // diff
	}}

	summary, err := Inspect(context.Background(), InspectOptions{Cwd: root, BaseRef: "main", RunGit: runner.Run})
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}
	if !summary.Clean || len(summary.Files) != 0 {
		t.Fatalf("expected clean base diff, got %#v", summary)
	}
	if summary.Base != "main" {
		t.Fatalf("Base = %q, want main", summary.Base)
	}
}

func TestParseNameStatusRenameAndCopy(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantPath   string
		wantStatus string
	}{
		{
			name:       "rename uses new path",
			line:       "R100\told.txt\tnew.txt",
			wantPath:   "new.txt",
			wantStatus: "renamed",
		},
		{
			name:       "copy uses destination path",
			line:       "C75\tsrc.txt\tdst.txt",
			wantPath:   "dst.txt",
			wantStatus: "copied",
		},
		{
			name:       "modify two-field no regression",
			line:       "M\ta.txt",
			wantPath:   "a.txt",
			wantStatus: "modified",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := parseNameStatus(tc.line)
			if len(files) != 1 {
				t.Fatalf("expected 1 file entry, got %d: %#v", len(files), files)
			}
			if files[0].Path != tc.wantPath {
				t.Fatalf("Path = %q, want %q", files[0].Path, tc.wantPath)
			}
			if files[0].Status != tc.wantStatus {
				t.Fatalf("Status = %q, want %q", files[0].Status, tc.wantStatus)
			}
		})
	}
}

func TestTruncateStringHonorsMaxBytesWithRedactionMarker(t *testing.T) {
	value := strings.Repeat("a", 32) + redaction.RedactedSecret + strings.Repeat("b", 32)
	for maxBytes := 1; maxBytes < len(redaction.RedactedSecret)+len("\n[truncated]"); maxBytes++ {
		truncated, ok := truncateString(value, maxBytes)
		if !ok {
			t.Fatalf("truncateString truncated = false for maxBytes=%d", maxBytes)
		}
		if len(truncated) > maxBytes {
			t.Fatalf("truncateString returned %d bytes for maxBytes=%d: %q", len(truncated), maxBytes, truncated)
		}
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

func initGitRepo(t *testing.T, withCommit bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root := t.TempDir()
	runGitCommand(t, root, "init")
	if withCommit {
		writeTestFile(t, filepath.Join(root, "README.md"), "initial\n")
		runGitCommand(t, root, "add", "README.md")
		runGitCommand(t, root, "-c", "user.name=Zero", "-c", "user.email=zero@example.invalid", "commit", "-m", "Initial commit")
	}
	return root
}

func runGitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return string(output)
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
