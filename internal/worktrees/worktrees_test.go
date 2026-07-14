package worktrees

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDefaultRunGitSeparatesStdoutAndStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()

	// A successful command writes to Stdout, leaving Stderr clean.
	ok, err := defaultRunGit(context.Background(), dir, "--version")
	if err != nil {
		t.Fatalf("git --version returned error: %v", err)
	}
	if !strings.Contains(ok.Stdout, "git version") {
		t.Fatalf("Stdout = %q, want a git version line", ok.Stdout)
	}
	if strings.TrimSpace(ok.Stderr) != "" {
		t.Fatalf("Stderr should be empty on success, got %q", ok.Stderr)
	}

	// A failing command's diagnostic must land on Stderr, not Stdout — the prior
	// CombinedOutput merged them and left Stderr empty.
	bad, err := defaultRunGit(context.Background(), dir, "not-a-real-subcommand")
	if err != nil {
		t.Fatalf("a non-zero git exit must not be a runner error, got %v", err)
	}
	if bad.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code for a bad subcommand")
	}
	if strings.TrimSpace(bad.Stderr) == "" {
		t.Fatalf("expected the git error on Stderr, got Stdout=%q Stderr=%q", bad.Stdout, bad.Stderr)
	}
}

func TestPrepareCreatesDetachedGitWorktree(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{},
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
	// Prepare must lock every worktree it creates: this is what makes
	// entry.locked inside Clean protect zero's own worktrees, not just ones a
	// human locked by hand (see TestCleanSkipsLockedZeroOwnedWorktree).
	if got := runner.commandLine(4); got != "git worktree lock --reason zero: active task worktree "+result.Path {
		t.Fatalf("git worktree lock command = %q", got)
	}
}

func TestReleaseUnlocksWorktree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-a")
	runner := &fakeRunner{results: []CommandResult{{}}}

	if err := Release(context.Background(), Options{RunGit: runner.Run}, path); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected exactly one git call, got %#v", runner.calls)
	}
	if runner.calls[0].dir != path {
		t.Fatalf("git worktree unlock dir = %q, want %q", runner.calls[0].dir, path)
	}
	if got := runner.commandLine(0); got != "git worktree unlock "+path {
		t.Fatalf("git worktree unlock command = %q", got)
	}
}

func TestReleasePropagatesGitFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-a")
	runner := &fakeRunner{results: []CommandResult{{ExitCode: 1, Stderr: "fatal: not a working tree"}}}

	err := Release(context.Background(), Options{RunGit: runner.Run}, path)
	if err == nil || !strings.Contains(err.Error(), "not a working tree") {
		t.Fatalf("Release error = %v, want it to surface the git failure", err)
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

func TestCleanPrunesStaleWorktrees(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	// Create directories representing two worktrees: one young, one stale.
	youngPath := filepath.Join(repoDir, "young-task")
	stalePath := filepath.Join(repoDir, "stale-task")
	if err := os.MkdirAll(youngPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// Change mtime of stale-task to be in the past (e.g. 2 days ago).
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stalePath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot}, // rev-parse --show-toplevel
			{Stdout: "worktree " + youngPath + "\nworktree " + stalePath + "\n"}, // worktree list --porcelain
			{ExitCode: 0}, // status --porcelain <stalePath> (clean)
			{ExitCode: 0}, // worktree remove --force <stalePath>
			{ExitCode: 0}, // worktree prune
		},
	}

	options := Options{
		Cwd:     repoRoot,
		BaseDir: baseDir,
		RunGit:  runner.Run,
	}

	err := Clean(context.Background(), options, 24*time.Hour)
	if err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	// Verify the calls made by Clean
	if len(runner.calls) != 5 {
		t.Fatalf("expected 5 git calls, got %d", len(runner.calls))
	}
	if runner.commandLine(0) != "git rev-parse --show-toplevel" {
		t.Errorf("call 0 = %q", runner.commandLine(0))
	}
	if runner.commandLine(1) != "git worktree list --porcelain" {
		t.Errorf("call 1 = %q", runner.commandLine(1))
	}
	expectedStatusCall := "git status --porcelain --ignored"
	if runner.commandLine(2) != expectedStatusCall {
		t.Errorf("call 2 = %q, want %q", runner.commandLine(2), expectedStatusCall)
	}
	expectedRemoveCall := "git worktree remove --force " + filepath.Clean(stalePath)
	if runner.commandLine(3) != expectedRemoveCall {
		t.Errorf("call 3 = %q, want %q", runner.commandLine(3), expectedRemoveCall)
	}
	if runner.commandLine(4) != "git worktree prune" {
		t.Errorf("call 4 = %q", runner.commandLine(4))
	}
}

// defaultRunGit deliberately returns a nil error alongside a nonzero
// CommandResult.ExitCode for a failed git invocation (see its comment), so
// Clean must check ExitCode itself rather than trusting a nil error to mean
// the removal succeeded.
func TestCleanReportsErrorOnFailedRemoval(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	stalePath := filepath.Join(repoDir, "stale-task")
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stalePath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + stalePath + "\n"},
			{ExitCode: 0}, // status --porcelain <stalePath> (clean)
			{ExitCode: 1, Stderr: "fatal: unable to remove worktree: in use"},
			{ExitCode: 0},
		},
	}

	err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour)
	if err == nil {
		t.Fatal("expected Clean to report the failed removal")
	}
	if !strings.Contains(err.Error(), "in use") {
		t.Errorf("error = %q, want it to include the git failure message", err.Error())
	}
}

// An inspection failure (the root can't be stat'd or walked) must fail
// closed: worktreeIsStale reports false rather than true, so an incomplete
// inspection can never authorize a forced removal.
func TestWorktreeIsStaleFailsClosedOnInspectionError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if worktreeIsStale(missing, time.Now()) {
		t.Fatal("expected worktreeIsStale to fail closed for an uninspectable root")
	}
}

func TestWorktreeIsStaleTrueForOldUntouchedTree(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if !worktreeIsStale(dir, time.Now().Add(-24*time.Hour)) {
		t.Fatal("expected an old, untouched directory to be reported stale")
	}
}

// A worktree with a stale top-level mtime but a file that was written deep
// inside the tree more recently must not be pruned: the directory's own mtime
// only changes when an entry is added/removed/renamed directly inside it, not
// when a long-running task edits an existing nested file.
func TestCleanSkipsWorktreeWithRecentNestedActivity(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	activePath := filepath.Join(repoDir, "active-task")
	nestedDir := filepath.Join(activePath, "internal", "pkg")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(activePath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(nestedDir, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	// The file itself is freshly written (default mtime is now), simulating a
	// task actively editing code deep in the worktree.
	nestedFile := filepath.Join(nestedDir, "handler.go")
	if err := os.WriteFile(nestedFile, []byte("package pkg"), 0o644); err != nil {
		t.Fatal(err)
	}

	// activePath/internal was created alongside nestedDir by the MkdirAll
	// above and never backdated, so it would otherwise still carry a fresh
	// mtime; WalkDir would hit it and report "not stale" before ever reaching
	// nestedFile, so the test would pass even if the recursive walk stopped
	// checking after the first directory level.
	if err := os.Chtimes(filepath.Join(activePath, "internal"), twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + activePath + "\n"},
			{ExitCode: 0}, // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	for _, call := range runner.calls {
		if len(call.args) > 0 && call.args[0] == "remove" {
			t.Fatalf("Clean removed an actively-edited worktree: %v", call.args)
		}
	}
}

// A worktree explicitly locked via `git worktree lock` must never be pruned,
// regardless of how stale its mtime looks.
func TestCleanSkipsLockedWorktree(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")

	lockedPath := filepath.Join(baseDir, "locked-task")
	if err := os.MkdirAll(lockedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(lockedPath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + lockedPath + "\nHEAD abc1234\nlocked in use by zero\n"},
			{ExitCode: 0}, // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	for _, call := range runner.calls {
		if len(call.args) > 0 && call.args[0] == "remove" {
			t.Fatalf("Clean removed a locked worktree: %v", call.args)
		}
	}
}

// A worktree zero created and locked at Prepare time (see the "worktree lock"
// call added there) must survive Clean even though it looks idle-but-clean: a
// task can finish committing and then sit waiting on a model, network, or
// user for far longer than the staleness window without touching the tree
// again, and mtime alone can't distinguish that from an abandoned worktree.
// Before this fix, Prepare never locked its own worktrees, so entry.locked
// only ever protected worktrees a human locked by hand.
func TestCleanSkipsLockedZeroOwnedWorktree(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	idlePath := filepath.Join(repoDir, "idle-task")
	if err := os.MkdirAll(idlePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(idlePath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + idlePath + "\nlocked zero: active task worktree\n"},
			{ExitCode: 0}, // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	for _, call := range runner.calls {
		if len(call.args) > 0 && (call.args[0] == "remove" || call.args[0] == "status") {
			t.Fatalf("Clean touched a locked zero-owned worktree: %v", call.args)
		}
	}
}

// A worktree whose only content is matched by .gitignore (credentials,
// generated drafts, task artifacts) must still block force-removal: plain
// `git status --porcelain` reports such a worktree as clean, but
// worktreeIsDirty now also passes --ignored, so Clean must treat it as dirty.
func TestCleanSkipsWorktreeWithOnlyIgnoredFiles(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	ignoredOnlyPath := filepath.Join(repoDir, "ignored-only-task")
	if err := os.MkdirAll(ignoredOnlyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(ignoredOnlyPath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + ignoredOnlyPath + "\n"},
			{Stdout: "!! ignored-data\n"}, // status --porcelain --ignored: ignored file present
			{ExitCode: 0},                 // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	for i, call := range runner.calls {
		if i == 2 {
			want := "status --porcelain --ignored"
			if got := strings.Join(call.args, " "); got != want {
				t.Fatalf("status call args = %q, want %q", got, want)
			}
		}
		if len(call.args) > 0 && call.args[0] == "remove" {
			t.Fatalf("Clean removed a worktree with only ignored files: %v", call.args)
		}
	}
}

// worktreeIsDirty must count files matched by .gitignore as dirty content: a
// worktree holding only ignored task data (credentials, generated drafts) has
// nothing to show in plain `git status --porcelain` and would otherwise pass
// as clean and be force-removed by Clean's staleness heuristic. This exercises
// the real git binary rather than the fake runner, so it also verifies
// --ignored actually changes git's answer, not just the command we send.
func TestWorktreeIsDirtyCountsIgnoredFilesAsDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet")
	run("config", "user.email", "zero@example.com")
	run("config", "user.name", "zero")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored-data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".gitignore")
	run("commit", "--quiet", "-m", "initial")

	if worktreeIsDirty(context.Background(), defaultRunGit, dir) {
		t.Fatal("expected a clean worktree with no ignored files present to report clean")
	}

	if err := os.WriteFile(filepath.Join(dir, "ignored-data"), []byte("secret task artifact"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !worktreeIsDirty(context.Background(), defaultRunGit, dir) {
		t.Fatal("expected an ignored-but-present file to count as dirty")
	}
}

// A sibling directory that merely shares the per-repository repoDir as a
// string prefix (e.g. "<repoDir>-other") must not be treated as zero-owned.
// This also covers a manually managed worktree for the SAME repository that a
// user placed directly under a shared baseDir rather than inside zero's
// repoDir subtree: it must not be treated as zero-owned either.
func TestCleanRejectsSiblingDirWithSharedPrefix(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))
	siblingDir := repoDir + "-other"

	siblingPath := filepath.Join(siblingDir, "not-ours")
	if err := os.MkdirAll(siblingPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(siblingPath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + siblingPath + "\n"},
			{ExitCode: 0}, // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	for _, call := range runner.calls {
		if len(call.args) > 0 && call.args[0] == "remove" {
			t.Fatalf("Clean removed a sibling directory outside baseDir: %v", call.args)
		}
	}
}

// A manually managed worktree that a user placed directly under a shared
// baseDir, outside zero's own "zero-worktree-<repoKey>" subtree, must never
// be pruned even though it is technically inside baseDir.
func TestCleanIgnoresWorktreeOutsideOwnedSubtree(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")

	manualPath := filepath.Join(baseDir, "hand-managed-checkout")
	if err := os.MkdirAll(manualPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(manualPath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + manualPath + "\n"},
			{ExitCode: 0}, // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	for _, call := range runner.calls {
		if len(call.args) > 0 && (call.args[0] == "remove" || call.args[0] == "status") {
			t.Fatalf("Clean touched a worktree outside its owned subtree: %v", call.args)
		}
	}
}

// A worktree with a stale top-level mtime but uncommitted or untracked
// changes must not be force-removed: a task can hold live work in a worktree
// while waiting on a model, network, or user for far longer than the
// staleness window, without ever writing to the tree again in that time.
func TestCleanSkipsDirtyStaleWorktree(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	dirtyPath := filepath.Join(repoDir, "dirty-task")
	if err := os.MkdirAll(dirtyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dirtyPath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + dirtyPath + "\n"},
			{Stdout: " M internal/pkg/handler.go\n"}, // status --porcelain: dirty
			{ExitCode: 0},                            // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	for _, call := range runner.calls {
		if len(call.args) > 0 && call.args[0] == "remove" {
			t.Fatalf("Clean removed a dirty worktree: %v", call.args)
		}
	}
}

// An inspection failure (git status errors out) must fail closed: treat the
// worktree as dirty rather than clean, so a broken status check can never
// authorize a forced removal.
func TestWorktreeIsDirtyFailsClosedOnInspectionError(t *testing.T) {
	runner := &fakeRunner{results: []CommandResult{{ExitCode: 1, Stderr: "fatal: not a git repository"}}}
	if !worktreeIsDirty(context.Background(), runner.Run, t.TempDir()) {
		t.Fatal("expected worktreeIsDirty to fail closed on a status error")
	}
}

func TestIsUnderDir(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "a", "base")
	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(base, "child"), true},
		{filepath.Join(base, "nested", "deeper"), true},
		{base, true},
		{base + "-other", false},
		{base + "-other" + string(filepath.Separator) + "child", false},
		{filepath.Join(string(filepath.Separator), "a", "elsewhere"), false},
	}
	for _, c := range cases {
		if got := isUnderDir(c.path, base); got != c.want {
			t.Errorf("isUnderDir(%q, %q) = %v, want %v", c.path, base, got, c.want)
		}
	}
}

func TestParseWorktreeListTracksLockedState(t *testing.T) {
	output := "worktree /a/one\nHEAD abc\nbranch refs/heads/main\n\n" +
		"worktree /a/two\nHEAD def\nlocked\n\n" +
		"worktree /a/three\nHEAD ghi\nlocked some reason\ndetached\n"

	entries := parseWorktreeList(output)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %#v", len(entries), entries)
	}
	if entries[0].path != filepath.Clean("/a/one") || entries[0].locked {
		t.Errorf("entries[0] = %#v", entries[0])
	}
	if entries[1].path != filepath.Clean("/a/two") || !entries[1].locked {
		t.Errorf("entries[1] = %#v", entries[1])
	}
	if entries[2].path != filepath.Clean("/a/three") || !entries[2].locked {
		t.Errorf("entries[2] = %#v", entries[2])
	}
}
