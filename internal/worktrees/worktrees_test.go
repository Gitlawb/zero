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

	// A failing command's diagnostic must land on Stderr, not Stdout - the prior
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
	// autoAbsoluteGitDir answers Prepare's post-lock ownership-marker write
	// (`git rev-parse --absolute-git-dir`) for the newly created worktree
	// without a canned result: without it, the fake runner falls off the end
	// of results and returns an empty CommandResult, so writeOwnershipMarker
	// resolves gitDir to "" and os.WriteFile writes "zero-owner" as a
	// relative path into the test process's actual working directory instead
	// of failing loudly.
	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "worktree " + root + "\n"},
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
	if got := runner.commandLine(4); got != "git worktree add --detach "+result.Path+" HEAD" {
		t.Fatalf("git worktree command = %q", got)
	}
	// Prepare must lock every worktree it creates: this is what makes
	// entry.locked inside Clean protect zero's own worktrees, not just ones a
	// human locked by hand (see TestCleanSkipsLockedZeroOwnedWorktree).
	if got := runner.commandLine(5); got != "git worktree lock --reason zero: active task worktree "+result.Path {
		t.Fatalf("git worktree lock command = %q", got)
	}
	if !result.LockAcquired {
		t.Fatalf("LockAcquired = false, want true for a worktree this call created")
	}
	// Prepare must also persist the ownership marker so Release/Clean can
	// tell this worktree apart from one a user created and locked by hand
	// under the same predictable zero-worktree-<repoKey> path convention.
	if got := runner.commandLine(6); got != "git rev-parse --absolute-git-dir" {
		t.Fatalf("marker write command = %q", got)
	}
	if len(runner.calls) != 7 {
		t.Fatalf("expected metadata calls plus lock and marker write, got %#v", runner.calls)
	}
}

func TestReleaseUnlocksWorktree(t *testing.T) {
	// repoRoot must be resolved to its physical spelling up front: on a
	// platform where the temp dir is itself a symlink (macOS /var ->
	// /private/var), real `git worktree list --porcelain` reports the
	// physical spelling, so computing repoKey from the lexical spelling here
	// would produce a different key than verifyZeroOwnedWorktree derives in
	// production, and Release would reject this genuinely Zero-owned fixture
	// as not-zero-managed. The worktree path itself is physicalized for the
	// same reason: Release compares the registered entry against the
	// canonical user path, and a lexical /var spelling would not match.
	repoRoot := physicalTestPath(t, t.TempDir())
	// path must carry the zero-worktree-<repoKey> ancestor component Prepare
	// actually creates: Release now refuses to unlock anything else.
	base := physicalTestPath(t, t.TempDir())
	path := filepath.Join(base, "zero-worktree-"+repoKey(repoRoot), "task-a")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	plantOwnershipMarker(t, path)
	// The target entry carries Zero's own lease reason (as Prepare's lock
	// call sets it), so the lock-reason check added alongside the ancestor
	// check must let this release through rather than treating every locked
	// entry as a manual, non-zero lock (see
	// TestReleaseRejectsManuallyLockedWorktree for the rejecting case).
	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: "worktree " + repoRoot + "\nworktree " + path + "\nlocked " + leaseReasonPrefix + "\n"},
			{},
		},
	}

	if err := Release(context.Background(), Options{RunGit: runner.Run}, path); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	// list + absolute-git-dir (marker) + unlock
	if len(runner.calls) != 3 {
		t.Fatalf("expected list, marker check, then unlock, got %#v", runner.calls)
	}
	if got := runner.commandLine(0); got != "git worktree list --porcelain" {
		t.Fatalf("ownership-check command = %q", got)
	}
	if got := runner.commandLine(1); got != "git rev-parse --absolute-git-dir" {
		t.Fatalf("marker-check command = %q", got)
	}
	if runner.calls[2].dir != path {
		t.Fatalf("git worktree unlock dir = %q, want %q", runner.calls[2].dir, path)
	}
	if got := runner.commandLine(2); got != "git worktree unlock "+path {
		t.Fatalf("git worktree unlock command = %q", got)
	}
}

// TestReleaseRejectsForgedZeroLeaseWithoutOwnershipMarker pins the fix for
// ownership being inferred from the public zero-worktree-<repoKey> path
// convention plus a lock reason that merely starts with leaseReasonPrefix: a
// user (or another tool) can create a same-repository worktree at that
// predictable path and lock it by hand with a reason like "zero: active task
// worktree manually-owned", which passes both the ancestor-component and
// HasPrefix checks. Only the ownership marker Prepare itself writes can tell
// that apart from a genuine Zero lease, so Release must still refuse to
// unlock it when the worktree directory exists but carries no marker.
func TestReleaseRejectsForgedZeroLeaseWithoutOwnershipMarker(t *testing.T) {
	repoRoot := physicalTestPath(t, t.TempDir())
	base := physicalTestPath(t, t.TempDir())
	path := filepath.Join(base, "zero-worktree-"+repoKey(repoRoot), "manually-owned")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	// Deliberately no plantOwnershipMarker call: this worktree exists and is
	// locked with a reason that starts with leaseReasonPrefix, exactly what a
	// user could reproduce by hand, but Prepare never created it.
	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: "worktree " + repoRoot + "\nworktree " + path + "\nlocked " + leaseReasonPrefix + " manually-owned\n"},
		},
	}

	err := Release(context.Background(), Options{RunGit: runner.Run}, path)
	if err == nil || !strings.Contains(err.Error(), "missing zero ownership marker") {
		t.Fatalf("Release error = %v, want a missing-ownership-marker rejection", err)
	}
	// list + marker check, no unlock call.
	if len(runner.calls) != 2 {
		t.Fatalf("expected only the ownership check and marker read, no unlock call, got %#v", runner.calls)
	}
}

func TestReleaseFallsBackToCwdWhenWorktreeDirMissing(t *testing.T) {
	// A caller who deletes a locked worktree directory by hand instead of
	// releasing it first leaves path itself gone; Release must still be able
	// to run `git worktree unlock` (from the main repo, via options.Cwd) so
	// the orphaned lock can be cleared and the entry later pruned.
	// repoRoot is resolved to its physical spelling for the same reason as
	// TestReleaseUnlocksWorktree: real `git worktree list --porcelain`
	// reports the physical spelling, so a lexical temp-dir spelling here
	// would derive a different repoKey than production and reject this
	// fixture. The deleted path must still appear in the porcelain list
	// (git keeps a prunable entry after a manual rm -rf) with Zero's lease
	// reason, or the ownership check refuses the unlock.
	repoRoot := physicalTestPath(t, t.TempDir())
	missingPath := filepath.Join(t.TempDir(), "zero-worktree-"+repoKey(repoRoot), "already-deleted")
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: "worktree " + repoRoot + "\nworktree " + missingPath + "\nlocked " + leaseReasonPrefix + "\n"},
		{},
	}}

	if err := Release(context.Background(), Options{RunGit: runner.Run, Cwd: repoRoot}, missingPath); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected exactly two git calls (ownership check, then unlock), got %#v", runner.calls)
	}
	if runner.calls[0].dir != repoRoot || runner.calls[1].dir != repoRoot {
		t.Fatalf("git calls = %#v, want both to fall back to Cwd %q", runner.calls, repoRoot)
	}
	if got := runner.commandLine(1); got != "git worktree unlock "+missingPath {
		t.Fatalf("git worktree unlock command = %q, want the original path as the unlock target", got)
	}
}

// TestReleaseRejectsNonZeroOwnedWorktree pins the fix for Release being
// usable to clear the lock on a worktree a user (or another tool) manages by
// hand: the command is documented as releasing a worktree `prepare` created,
// not an arbitrary git worktree lock, so a path with no zero-worktree-<repoKey>
// ancestor component must be refused before any unlock is attempted.
func TestReleaseRejectsNonZeroOwnedWorktree(t *testing.T) {
	repoRoot := t.TempDir()
	manualWorktree := filepath.Join(t.TempDir(), "my-manual-worktree")
	if err := os.MkdirAll(manualWorktree, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: "worktree " + repoRoot + "\n"},
	}}

	err := Release(context.Background(), Options{RunGit: runner.Run}, manualWorktree)
	if err == nil || !strings.Contains(err.Error(), "not a zero-managed worktree") {
		t.Fatalf("Release error = %v, want a not-zero-managed rejection", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected only the ownership check, no unlock call, got %#v", runner.calls)
	}
}

// TestReleaseRejectsManuallyLockedWorktree pins the fix for Release being
// usable to clear a lock a user (or another tool) applied by hand to a
// worktree that otherwise sits inside Zero's zero-worktree-<repoKey>
// subtree: the ancestor-component check alone can't tell that lock apart
// from one of Zero's own leases, so Release must also refuse to unlock an
// entry whose recorded lock reason doesn't carry Zero's lease prefix.
func TestReleaseRejectsManuallyLockedWorktree(t *testing.T) {
	repoRoot := physicalTestPath(t, t.TempDir())
	// Use a physical base so the porcelain entry path matches the
	// canonicalizePath comparison Release uses against git's physical
	// spelling (macOS /var vs /private/var, symlink TMPDIR layouts).
	base := physicalTestPath(t, t.TempDir())
	path := filepath.Join(base, "zero-worktree-"+repoKey(repoRoot), "task-a")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: "worktree " + repoRoot + "\nworktree " + path + "\nlocked do not touch, in use\n"},
	}}

	err := Release(context.Background(), Options{RunGit: runner.Run}, path)
	if err == nil || !strings.Contains(err.Error(), "not a zero lease") {
		t.Fatalf("Release error = %v, want a not-a-zero-lease rejection", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected only the ownership check, no unlock call, got %#v", runner.calls)
	}
}

// TestReleaseRejectsUnregisteredWorktree pins the requirement that Release
// only unlocks a path git currently has registered for the repository. A
// same-looking zero-worktree-<repoKey> path that never appeared in
// `git worktree list` must not reach unlock.
func TestReleaseRejectsUnregisteredWorktree(t *testing.T) {
	repoRoot := physicalTestPath(t, t.TempDir())
	base := physicalTestPath(t, t.TempDir())
	path := filepath.Join(base, "zero-worktree-"+repoKey(repoRoot), "not-registered")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: "worktree " + repoRoot + "\n"},
	}}

	err := Release(context.Background(), Options{RunGit: runner.Run}, path)
	if err == nil || !strings.Contains(err.Error(), "not a registered worktree") {
		t.Fatalf("Release error = %v, want a not-registered rejection", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected only the ownership check, no unlock call, got %#v", runner.calls)
	}
}

func TestReleasePropagatesGitFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-a")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
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
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "worktree " + root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{Stdout: sourceGit + "\n"},
			{Stdout: sourceGit + "\n"},
			{},
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
	// metadata calls + lock + ownership marker write (absolute-git-dir)
	if len(runner.calls) != 8 {
		t.Fatalf("expected metadata git calls plus lock and marker write, got %#v", runner.calls)
	}
	// The original lock may have been released by a prior run's exit, which
	// would leave the reused worktree exposed to Clean's staleness heuristic
	// while this caller is still using it: reuse must re-establish the lease.
	if got := runner.commandLine(6); got != "git worktree lock --reason zero: active task worktree "+existing {
		t.Fatalf("git worktree lock command = %q", got)
	}
	if got := runner.commandLine(7); got != "git rev-parse --absolute-git-dir" {
		t.Fatalf("marker write command = %q", got)
	}
	if !result.LockAcquired {
		t.Fatalf("LockAcquired = false, want true for a lease this call took")
	}
}

func TestPrepareRejectsWorktreeLockedByAnotherRun(t *testing.T) {
	// A reused worktree that is still locked belongs to another live run.
	// Handing that checkout to a second caller would let two runs edit one
	// supposedly isolated tree, and whichever exits first would release the
	// single Git lock out from under the other, so Prepare must reject the
	// in-use lease instead of returning the path.
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
			{Stdout: "worktree " + root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{Stdout: sourceGit + "\n"},
			{Stdout: sourceGit + "\n"},
			{ExitCode: 128, Stderr: "fatal: '" + existing + "' is already locked, reason: zero: active task worktree"},
		},
	}

	_, err := Prepare(context.Background(), Options{
		Cwd:     root,
		Name:    "reuse-me",
		BaseDir: base,
		RunGit:  runner.Run,
	})
	if err == nil || !strings.Contains(err.Error(), "locked by another active run") {
		t.Fatalf("Prepare must reject an in-use lease, got %v", err)
	}
}

// TestPrepareRollsBackWorktreeOnLockFailure pins the fix for a newly created
// worktree being left behind, unleased, when the lock call after `git
// worktree add` fails for a reason other than a concurrent racer (an actual
// git failure, not "already locked"): nothing else can own a worktree this
// call just created, so Prepare must remove it rather than leaking it until
// Clean's staleness window eventually reclaims it.
func TestPrepareRollsBackWorktreeOnLockFailure(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: root + "\n"},
			{Stdout: "worktree " + root + "\n"},
			{Stdout: "main\n"},
			{Stdout: "abc1234\n"},
			{}, // worktree add
			{ExitCode: 1, Stderr: "fatal: disk full"}, // worktree lock
			{}, // worktree remove --force (rollback)
		},
	}

	_, err := Prepare(context.Background(), Options{
		Cwd:     root,
		Name:    "review-api",
		BaseDir: base,
		RunGit:  runner.Run,
	})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Prepare must surface the lock failure, got %v", err)
	}
	if len(runner.calls) != 7 {
		t.Fatalf("expected add, lock, and a rollback removal, got %#v", runner.calls)
	}
	lastCall := runner.calls[len(runner.calls)-1]
	if lastCall.args[0] != "worktree" || lastCall.args[1] != "remove" {
		t.Fatalf("last call = %#v, want a rollback `git worktree remove`", lastCall)
	}
}

// physicalTestPath resolves a test directory to its physical spelling
// (symlinks and Windows 8.3 short names), matching how git records paths.
func physicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

// TestPrepareFromLinkedWorktreeCanBeReleased pins the fix for Prepare and
// Release deriving different repoKeys when Prepare is invoked from a linked
// worktree instead of the main one. Before the fix, Prepare hashed
// --show-toplevel (the linked checkout's own root when run there), while
// Release always hashes the main worktree's root (via git worktree list
// --porcelain, whose first entry is always the main worktree); a worktree
// prepared from a linked checkout therefore failed its own ownership check
// on release and its lease could never be cleared.
func TestPrepareFromLinkedWorktreeCanBeReleased(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	mainRepo := physicalTestPath(t, t.TempDir())
	mustGit := func(dir string, args ...string) string {
		t.Helper()
		out, err := gitOutput(ctx, defaultRunGit, dir, args...)
		if err != nil {
			t.Skipf("git unavailable or failed (%v): %v", args, err)
		}
		return out
	}
	mustGit(mainRepo, "init")
	mustGit(mainRepo, "-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "--allow-empty", "-m", "seed")

	// A linked worktree of mainRepo, checked out elsewhere on disk. Its own
	// --show-toplevel is this directory, not mainRepo.
	linkedWorktree := filepath.Join(t.TempDir(), "linked")
	mustGit(mainRepo, "worktree", "add", "--detach", linkedWorktree, "HEAD")

	base := physicalTestPath(t, t.TempDir())
	result, err := Prepare(ctx, Options{Cwd: linkedWorktree, BaseDir: base, Name: "from-linked"})
	if err != nil {
		t.Fatalf("Prepare from a linked worktree: %v", err)
	}
	wantComponent := "zero-worktree-" + repoKey(mainRepo)
	if !strings.Contains(result.Path, wantComponent) {
		t.Fatalf("Path = %q, want it keyed off the main worktree %q (component %q)", result.Path, mainRepo, wantComponent)
	}

	if err := Release(ctx, Options{}, result.Path); err != nil {
		t.Fatalf("Release of a worktree Prepare created from a linked worktree: %v", err)
	}
}

// TestCleanFromLinkedWorktreePrunesStaleWorktree pins the fix for Clean
// keying its owned-subtree bucket off repoRoot (--show-toplevel for whatever
// worktree Clean itself runs from) instead of the main worktree's root:
// Prepare and Clean must agree on repoKey regardless of which checkout
// either call runs from, or a Clean invoked from a linked worktree computes
// a different repoDir than Prepare used and silently skips every
// zero-owned worktree for the repository.
func TestCleanFromLinkedWorktreePrunesStaleWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	mainRepo := physicalTestPath(t, t.TempDir())
	mustGit := func(dir string, args ...string) string {
		t.Helper()
		out, err := gitOutput(ctx, defaultRunGit, dir, args...)
		if err != nil {
			t.Skipf("git unavailable or failed (%v): %v", args, err)
		}
		return out
	}
	mustGit(mainRepo, "init")
	mustGit(mainRepo, "-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "--allow-empty", "-m", "seed")

	// A linked worktree of mainRepo, checked out elsewhere on disk. Its own
	// --show-toplevel is this directory, not mainRepo, so Clean run from here
	// would compute repoKey(linkedWorktree) unless it instead keys off the
	// main worktree's root the way Prepare does.
	linkedWorktree := filepath.Join(t.TempDir(), "linked")
	mustGit(mainRepo, "worktree", "add", "--detach", linkedWorktree, "HEAD")

	base := physicalTestPath(t, t.TempDir())
	result, err := Prepare(ctx, Options{Cwd: linkedWorktree, BaseDir: base, Name: "stale-from-linked"})
	if err != nil {
		t.Fatalf("Prepare from a linked worktree: %v", err)
	}
	if err := Release(ctx, Options{Cwd: linkedWorktree}, result.Path); err != nil {
		t.Fatalf("Release: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(result.Path, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}

	// Clean itself also runs from the linked worktree here, mirroring a
	// Prepare/Clean invocation started from a linked checkout.
	if err := Clean(ctx, Options{Cwd: linkedWorktree, BaseDir: base}, 24*time.Hour); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
		t.Fatalf("Clean run from a linked worktree should have pruned the stale zero-owned worktree, stat err: %v", err)
	}
}

// TestCleanPrunesStaleWorktreeUnderSymlinkedBaseDir pins the fix for a
// symlinked --worktree-dir: git worktree list --porcelain reports each
// worktree's PHYSICAL location, resolving any symlink component, so Clean
// comparing entries against a merely-absolute (not symlink-resolved) baseDir
// would reject every worktree created under a symlinked base and never prune
// it. Unlike the other Clean tests, base is deliberately handed to
// Prepare/Clean via its symlinked spelling, not its physical one (which
// physicalTestPath would normally produce), so this actually exercises the
// mismatch.
func TestCleanPrunesStaleWorktreeUnderSymlinkedBaseDir(t *testing.T) {
	ctx := context.Background()
	repo := physicalTestPath(t, t.TempDir())
	mustGit := func(args ...string) string {
		t.Helper()
		out, err := gitOutput(ctx, defaultRunGit, repo, args...)
		if err != nil {
			t.Skipf("git unavailable or failed (%v): %v", args, err)
		}
		return out
	}
	mustGit("init")
	mustGit("-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "--allow-empty", "-m", "seed")

	realBase := physicalTestPath(t, t.TempDir())
	base := filepath.Join(t.TempDir(), "base-link")
	if err := os.Symlink(realBase, base); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := Prepare(ctx, Options{Cwd: repo, BaseDir: base, Name: "stale-task"}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	staleDir := filepath.Join(realBase, "zero-worktree-"+repoKey(repo), "stale-task")
	if _, err := os.Stat(staleDir); err != nil {
		t.Fatalf("expected worktree created at physical base path %s: %v", staleDir, err)
	}
	// Release the lock Prepare took and age every entry past the cutoff, so
	// the worktree is both unlocked and stale.
	if err := Release(ctx, Options{Cwd: repo}, staleDir); err != nil {
		t.Fatalf("Release: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(staleDir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}

	if err := Clean(ctx, Options{Cwd: repo, BaseDir: base}, 24*time.Hour); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("Clean should have pruned the stale worktree under the symlinked base dir, stat err: %v", err)
	}
}

// TestReleaseRecoversDeletedWorktreeUnderSymlinkedBaseDir pins the fix for
// the documented `release -C` recovery path failing when --worktree-dir was
// a symlink and the worktree itself was then deleted by hand: EvalSymlinks
// has nothing to resolve on a path that no longer exists, so the prior
// Abs+Clean fallback kept the symlinked (logical) spelling, which never
// matched git's physical-path porcelain entry, and the orphaned lock could
// never be released. canonicalizePath must resolve through the nearest
// existing ancestor (the symlink itself, since everything under it is gone)
// and reattach the missing tail.
func TestReleaseRecoversDeletedWorktreeUnderSymlinkedBaseDir(t *testing.T) {
	ctx := context.Background()
	repo := physicalTestPath(t, t.TempDir())
	realBase := physicalTestPath(t, t.TempDir())
	base := filepath.Join(t.TempDir(), "base-link")
	if err := os.Symlink(realBase, base); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// The worktree directory under the symlinked base was deleted by hand;
	// nothing on disk exists below base itself.
	deletedPath := filepath.Join(base, "zero-worktree-"+repoKey(repo), "already-deleted")
	physicalPath := filepath.Join(realBase, "zero-worktree-"+repoKey(repo), "already-deleted")

	runner := &fakeRunner{results: []CommandResult{
		{Stdout: "worktree " + repo + "\nworktree " + physicalPath + "\nlocked " + leaseReasonPrefix + "\n"},
		{},
	}}

	if err := Release(ctx, Options{RunGit: runner.Run, Cwd: repo}, deletedPath); err != nil {
		t.Fatalf("Release must recover a deleted worktree under a symlinked base dir: %v", err)
	}
	if got := runner.commandLine(1); got != "git worktree unlock "+deletedPath {
		t.Fatalf("git worktree unlock command = %q", got)
	}
}

// TestPrepareValidatesRequestBeforeCleanup pins the order of validation and
// the automatic stale-worktree pruning: a rejected request (an invalid
// --name) must not have destructive cleanup side effects before it reports
// its error. The second half proves the assertion has teeth: the same stale
// worktree IS pruned once a valid request runs.
func TestPrepareValidatesRequestBeforeCleanup(t *testing.T) {
	ctx := context.Background()
	// Canonicalize both roots up front: git records worktree paths in
	// physical spelling, so a lexically different spelling of the same
	// directory (macOS /var vs /private/var, Windows 8.3 short names on CI
	// runners) would make Clean's containment check skip the entry.
	repo := physicalTestPath(t, t.TempDir())
	mustGit := func(args ...string) string {
		t.Helper()
		out, err := gitOutput(ctx, defaultRunGit, repo, args...)
		if err != nil {
			t.Skipf("git unavailable or failed (%v): %v", args, err)
		}
		return out
	}
	mustGit("init")
	mustGit("-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "--allow-empty", "-m", "seed")
	toplevel := filepath.Clean(mustGit("rev-parse", "--show-toplevel"))

	base := physicalTestPath(t, t.TempDir())
	staleDir := filepath.Join(base, "zero-worktree-"+repoKey(toplevel), "stale-task")
	if err := os.MkdirAll(filepath.Dir(staleDir), 0o700); err != nil {
		t.Fatal(err)
	}
	mustGit("worktree", "add", "--detach", staleDir)
	// Plant the ownership marker Clean requires before it will force-remove a
	// path: without it, a hand-created worktree under the predictable
	// zero-worktree-* layout must not be pruned. Real linked worktrees keep
	// their admin dir behind a .git file (gitdir: ...), so resolve it the
	// same way writeOwnershipMarker does rather than mkdir path/.git.
	gitDir, err := gitOutput(ctx, defaultRunGit, staleDir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatalf("resolve stale worktree git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, zeroOwnerMarkerFile), []byte(zeroOwnerMarkerContent), 0o600); err != nil {
		t.Fatalf("plant ownership marker: %v", err)
	}
	// Age every filesystem entry past the 24h staleness cutoff.
	old := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(staleDir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}
	// Also age the admin dir (outside the worktree tree) so nested activity
	// under the marker file does not keep the worktree "fresh".
	if err := filepath.WalkDir(gitDir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := Prepare(ctx, Options{Cwd: repo, BaseDir: base, Name: "../escape"}); err == nil {
		t.Fatal("expected invalid-name error")
	}
	if _, err := os.Stat(staleDir); err != nil {
		t.Fatalf("rejected request must not prune worktrees, stale dir: %v", err)
	}

	if _, err := Prepare(ctx, Options{Cwd: repo, BaseDir: base, Name: "fresh-task"}); err != nil {
		t.Fatalf("valid Prepare: %v", err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("valid request should have pruned the stale worktree, stat err: %v", err)
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
			{Stdout: "worktree " + root + "\n"},
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
			{Stdout: "worktree " + root + "\n"},
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
	// autoAbsoluteGitDir answers `rev-parse --absolute-git-dir` without
	// consuming a queued result: it ensures dir/.git exists and returns that
	// path. Tests plant zero-owner markers under that dir when they want
	// Release/Clean to treat the worktree as Prepare-created.
	autoAbsoluteGitDir bool
}

func (runner *fakeRunner) Run(ctx context.Context, dir string, args ...string) (CommandResult, error) {
	runner.calls = append(runner.calls, gitCall{dir: dir, args: append([]string{}, args...)})
	if runner.autoAbsoluteGitDir && len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--absolute-git-dir" {
		gitDir := filepath.Join(dir, ".git")
		if err := os.MkdirAll(gitDir, 0o700); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{Stdout: gitDir + "\n"}, nil
	}
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

// plantOwnershipMarker writes Prepare's ownership marker under path's .git
// admin dir so Release/Clean treat the fixture as zero-created.
func plantOwnershipMarker(t *testing.T, path string) {
	t.Helper()
	gitDir := filepath.Join(path, ".git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, zeroOwnerMarkerFile), []byte(zeroOwnerMarkerContent), 0o600); err != nil {
		t.Fatal(err)
	}
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
	// Markers first, then age the whole tree: planting after Chtimes would
	// refresh the worktree mtime and make worktreeIsStale skip the prune.
	plantOwnershipMarker(t, youngPath)
	plantOwnershipMarker(t, stalePath)

	// Change mtime of stale-task (and its marker files) to be in the past.
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(stalePath, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, twoDaysAgo, twoDaysAgo)
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: repoRoot}, // rev-parse --show-toplevel
			// Main worktree must be listed first: Clean keys repoDir off entries[0].
			{Stdout: "worktree " + repoRoot + "\nworktree " + youngPath + "\nworktree " + stalePath + "\n"},
			{ExitCode: 0},               // status --porcelain <stalePath> (clean)
			{Stdout: "deadbeef"},        // rev-parse HEAD <stalePath>
			{Stdout: "refs/heads/main"}, // for-each-ref --contains=deadbeef (already reachable)
			{ExitCode: 0},               // worktree remove --force <stalePath>
			{ExitCode: 0},               // worktree prune
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

	// toplevel + list + status(stale) + marker + HEAD + for-each-ref + remove + prune
	// young is skipped as not-stale before any status/marker work.
	if len(runner.calls) != 8 {
		t.Fatalf("expected 8 git calls, got %d: %#v", len(runner.calls), runner.calls)
	}
	if runner.commandLine(0) != "git rev-parse --show-toplevel" {
		t.Errorf("call 0 = %q", runner.commandLine(0))
	}
	if runner.commandLine(1) != "git worktree list --porcelain" {
		t.Errorf("call 1 = %q", runner.commandLine(1))
	}
	expectedStatusCall := "git status --porcelain"
	if runner.commandLine(2) != expectedStatusCall {
		t.Errorf("call 2 = %q, want %q", runner.commandLine(2), expectedStatusCall)
	}
	if runner.commandLine(3) != "git rev-parse --absolute-git-dir" {
		t.Errorf("call 3 = %q, want ownership marker check", runner.commandLine(3))
	}
	if runner.commandLine(4) != "git rev-parse HEAD" {
		t.Errorf("call 4 = %q, want the pre-removal HEAD-preservation check", runner.commandLine(4))
	}
	expectedRemoveCall := "git worktree remove --force " + filepath.Clean(stalePath)
	if runner.commandLine(6) != expectedRemoveCall {
		t.Errorf("call 6 = %q, want %q", runner.commandLine(6), expectedRemoveCall)
	}
	if runner.commandLine(7) != "git worktree prune" {
		t.Errorf("call 7 = %q", runner.commandLine(7))
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
	plantOwnershipMarker(t, stalePath)
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(stalePath, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, twoDaysAgo, twoDaysAgo)
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + repoRoot + "\nworktree " + stalePath + "\n"},
			{ExitCode: 0},               // status --porcelain <stalePath> (clean)
			{Stdout: "deadbeef"},        // rev-parse HEAD <stalePath>
			{Stdout: "refs/heads/main"}, // for-each-ref --contains=deadbeef (already reachable)
			{ExitCode: 1, Stderr: "fatal: unable to remove worktree: in use"}, // worktree remove --force <stalePath>
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

func TestCleanAggregatesMultipleFailedRemovals(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	stalePathA := filepath.Join(repoDir, "stale-task-a")
	stalePathB := filepath.Join(repoDir, "stale-task-b")
	for _, path := range []string{stalePathA, stalePathB} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{stalePathA, stalePathB} {
		plantOwnershipMarker(t, path)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	for _, path := range []string{stalePathA, stalePathB} {
		if err := filepath.WalkDir(path, func(p string, _ os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Chtimes(p, twoDaysAgo, twoDaysAgo)
		}); err != nil {
			t.Fatal(err)
		}
	}

	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + repoRoot + "\nworktree " + stalePathA + "\n\nworktree " + stalePathB + "\n"},
			{ExitCode: 0},               // status --porcelain <stalePathA> (clean)
			{Stdout: "deadbeefa"},       // rev-parse HEAD <stalePathA>
			{Stdout: "refs/heads/main"}, // for-each-ref --contains=deadbeefa (already reachable)
			{ExitCode: 1, Stderr: "fatal: unable to remove worktree A"}, // remove stalePathA
			{ExitCode: 0},               // status --porcelain <stalePathB> (clean)
			{Stdout: "deadbeefb"},       // rev-parse HEAD <stalePathB>
			{Stdout: "refs/heads/main"}, // for-each-ref --contains=deadbeefb (already reachable)
			{ExitCode: 1, Stderr: "fatal: unable to remove worktree B"}, // remove stalePathB
			{ExitCode: 0}, // final prune
		},
	}

	err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour)
	if err == nil {
		t.Fatal("expected Clean to report both failed removals")
	}
	// Both failures must survive in the returned error, not just the last one
	// to occur - overwriting lastErr instead of joining would silently drop
	// worktree A's failure once worktree B's removal is also attempted.
	if !strings.Contains(err.Error(), "unable to remove worktree A") {
		t.Errorf("error = %q, missing worktree A's failure", err.Error())
	}
	if !strings.Contains(err.Error(), "unable to remove worktree B") {
		t.Errorf("error = %q, missing worktree B's failure", err.Error())
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
// TestCleanPreservesUnreachableCommitBeforeRemoval pins the fix for a real
// data-loss case: Prepare creates every worktree with `worktree add --detach`,
// so a commit made there is reachable only through that worktree's own HEAD.
// If the worktree goes stale and clean before the commit is otherwise
// referenced, force-removing it must not let the commit become unreachable -
// Clean has to preserve it under a durable ref first.
func TestCleanPreservesUnreachableCommitBeforeRemoval(t *testing.T) {
	ctx := context.Background()
	repo := physicalTestPath(t, t.TempDir())
	mustGit := func(dir string, args ...string) string {
		t.Helper()
		out, err := gitOutput(ctx, defaultRunGit, dir, args...)
		if err != nil {
			t.Skipf("git unavailable or failed (%v): %v", args, err)
		}
		return out
	}
	mustGit(repo, "init")
	mustGit(repo, "-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "--allow-empty", "-m", "seed")

	base := physicalTestPath(t, t.TempDir())
	if _, err := Prepare(ctx, Options{Cwd: repo, BaseDir: base, Name: "orphan-task"}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	staleDir := filepath.Join(base, "zero-worktree-"+repoKey(repo), "orphan-task")

	// Commit inside the worktree: this HEAD is not on any branch, so nothing
	// outside the worktree itself points at it yet.
	mustGit(staleDir, "-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "--allow-empty", "-m", "orphaned work")
	orphanSHA := mustGit(staleDir, "rev-parse", "HEAD")

	if err := Release(ctx, Options{Cwd: repo}, staleDir); err != nil {
		t.Fatalf("Release: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(staleDir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}

	if err := Clean(ctx, Options{Cwd: repo, BaseDir: base}, 24*time.Hour); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("Clean should have pruned the stale worktree, stat err: %v", err)
	}
	if _, err := gitOutput(ctx, defaultRunGit, repo, "cat-file", "-e", orphanSHA); err != nil {
		t.Fatalf("commit %s is no longer reachable after Clean: %v", orphanSHA, err)
	}
	preserved := mustGit(repo, "for-each-ref", "--contains="+orphanSHA, "--count=1", "--format=%(refname)")
	if strings.TrimSpace(preserved) == "" {
		t.Fatalf("commit %s survived only by luck (not yet GC'd); expected a durable ref to contain it", orphanSHA)
	}
}

func TestCleanReclaimsReleasedWorktreeWithOnlyIgnoredFiles(t *testing.T) {
	// An explicit release is the owner's completion signal: an unlocked,
	// stale worktree holding only gitignored residue (node_modules, build
	// output) must be reclaimable, or every released worktree with such
	// artifacts leaks disk forever. The dirty probe for unlocked entries
	// therefore omits --ignored.
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
	plantOwnershipMarker(t, ignoredOnlyPath)
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(ignoredOnlyPath, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, twoDaysAgo, twoDaysAgo)
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + repoRoot + "\nworktree " + ignoredOnlyPath + "\n"},
			{ExitCode: 0},               // status --porcelain: ignored files invisible => clean
			{Stdout: "deadbeef"},        // rev-parse HEAD
			{Stdout: "refs/heads/main"}, // for-each-ref --contains (reachable)
			{ExitCode: 0},               // worktree remove --force
			{ExitCode: 0},               // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	if got, want := runner.commandLine(2), "git status --porcelain"; got != want {
		t.Fatalf("status call = %q, want %q (released worktrees must not count ignored residue)", got, want)
	}
	removed := false
	for _, call := range runner.calls {
		if len(call.args) > 1 && call.args[0] == "worktree" && call.args[1] == "remove" {
			removed = true
		}
	}
	if !removed {
		t.Fatal("Clean did not reclaim a released, stale worktree holding only ignored residue")
	}
}

// TestCleanRecoversExpiredLease: a Zero lease that records its owning PID is
// recoverable - if that process died without releasing (SIGKILL, crash), the
// lock must not protect the worktree forever. A stale, clean worktree behind
// a dead-owner lease is unlocked and removed; the dirty probe there keeps
// --ignored because a crashed task never signaled completion.
func TestCleanRecoversExpiredLease(t *testing.T) {
	deadPID := deadProcessPID(t)
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	crashedPath := filepath.Join(repoDir, "crashed-task")
	if err := os.MkdirAll(crashedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	plantOwnershipMarker(t, crashedPath)
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(crashedPath, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, twoDaysAgo, twoDaysAgo)
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + repoRoot + "\nworktree " + crashedPath + "\nlocked " + leaseReason(deadPID) + "\n"},
			{ExitCode: 0},               // status --porcelain --ignored: clean
			{Stdout: "deadbeef"},        // rev-parse HEAD
			{Stdout: "refs/heads/main"}, // for-each-ref --contains (reachable)
			{ExitCode: 0},               // worktree unlock (lease recovery)
			{ExitCode: 0},               // worktree remove --force
			{ExitCode: 0},               // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	// status is still call 2; ownership marker check is call 3; then HEAD,
	// for-each-ref, unlock, remove, prune.
	if got, want := runner.commandLine(2), "git status --porcelain --ignored"; got != want {
		t.Fatalf("status call = %q, want %q (a crashed lease never signaled completion)", got, want)
	}
	if got, want := runner.commandLine(6), "git worktree unlock "+filepath.Clean(crashedPath); got != want {
		t.Fatalf("call 6 = %q, want lease recovery %q", got, want)
	}
	if got, want := runner.commandLine(7), "git worktree remove --force "+filepath.Clean(crashedPath); got != want {
		t.Fatalf("call 7 = %q, want %q", got, want)
	}
}

// TestCleanSkipsExpiredLeaseWithIgnoredData: even behind a dead-owner lease,
// ignored files (credentials, generated drafts) may be all the crashed task
// left behind, so they still block removal.
func TestCleanSkipsExpiredLeaseWithIgnoredData(t *testing.T) {
	deadPID := deadProcessPID(t)
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	crashedPath := filepath.Join(repoDir, "crashed-task")
	if err := os.MkdirAll(crashedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(crashedPath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + crashedPath + "\nlocked " + leaseReason(deadPID) + "\n"},
			{Stdout: "!! ignored-data\n"}, // status --porcelain --ignored
			{ExitCode: 0},                 // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}
	for _, call := range runner.calls {
		if len(call.args) > 1 && call.args[0] == "worktree" && (call.args[1] == "remove" || call.args[1] == "unlock") {
			t.Fatalf("Clean touched a crashed worktree holding ignored data: %v", call.args)
		}
	}
}

// TestCleanHonorsLiveLease: a lease whose recorded owner is still running is
// never expired, regardless of staleness.
func TestCleanHonorsLiveLease(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	livePath := filepath.Join(repoDir, "live-task")
	if err := os.MkdirAll(livePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(livePath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + livePath + "\nlocked " + leaseReason(os.Getpid()) + "\n"},
			{ExitCode: 0}, // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}
	for _, call := range runner.calls {
		if len(call.args) > 0 && (call.args[0] == "remove" || call.args[0] == "status") {
			t.Fatalf("Clean touched a worktree behind a live lease: %v", call.args)
		}
	}
}

// deadProcessPID returns the PID of a process that has already exited, for
// exercising lease expiry. PID reuse in the instant between exit and the
// assertion is vanishingly unlikely.
func deadProcessPID(t *testing.T) int {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command(gitPath, "version")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run git version: %v", err)
	}
	return cmd.Process.Pid
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

	if worktreeIsDirty(context.Background(), defaultRunGit, dir, true) {
		t.Fatal("expected a clean worktree with no ignored files present to report clean")
	}

	if err := os.WriteFile(filepath.Join(dir, "ignored-data"), []byte("secret task artifact"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !worktreeIsDirty(context.Background(), defaultRunGit, dir, true) {
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
	if !worktreeIsDirty(context.Background(), runner.Run, t.TempDir(), true) {
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
	if entries[0].path != canonicalizePath("/a/one") || entries[0].locked {
		t.Errorf("entries[0] = %#v", entries[0])
	}
	if entries[1].path != canonicalizePath("/a/two") || !entries[1].locked {
		t.Errorf("entries[1] = %#v", entries[1])
	}
	if entries[2].path != canonicalizePath("/a/three") || !entries[2].locked {
		t.Errorf("entries[2] = %#v", entries[2])
	}
}

func TestCleanUnlocksExpiredLeaseBeforePruningMissingDir(t *testing.T) {
	deadPID := deadProcessPID(t)
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))
	missingPath := filepath.Join(repoDir, "missing-dead-task")

	runner := &fakeRunner{
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + repoRoot + "\nworktree " + missingPath + "\nlocked " + leaseReason(deadPID) + "\n"},
			{ExitCode: 0}, // unlock
			{ExitCode: 0}, // prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	unlocked := false
	pruned := false
	for _, call := range runner.calls {
		if len(call.args) >= 2 && call.args[0] == "worktree" {
			if call.args[1] == "unlock" {
				unlocked = true
			}
			if call.args[1] == "prune" {
				pruned = true
			}
		}
	}
	if !unlocked || !pruned {
		t.Errorf("expected unlock and prune for missing expired lease, unlocked=%v, pruned=%v, calls=%#v", unlocked, pruned, runner.calls)
	}
}

func TestCleanMigratesAndReclaimsLegacyZeroWorktrees(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "zero-worktrees")
	repoRoot := filepath.Join(tempDir, "repo")
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	legacyPath := filepath.Join(repoDir, "legacy-task")
	if err := os.MkdirAll(legacyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// Note: plantOwnershipMarker is explicitly NOT called here, simulating a pre-upgrade worktree
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(legacyPath, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, twoDaysAgo, twoDaysAgo)
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		autoAbsoluteGitDir: true,
		results: []CommandResult{
			{Stdout: repoRoot},
			{Stdout: "worktree " + repoRoot + "\nworktree " + legacyPath + "\n"},
			{ExitCode: 0},               // status --porcelain --ignored: clean
			{Stdout: "deadbeef"},        // rev-parse HEAD
			{Stdout: "refs/heads/main"}, // for-each-ref --contains (reachable)
			{ExitCode: 0},               // worktree remove --force
			{ExitCode: 0},               // worktree prune
		},
	}

	if err := Clean(context.Background(), Options{Cwd: repoRoot, BaseDir: baseDir, RunGit: runner.Run}, 24*time.Hour); err != nil {
		t.Fatalf("Clean failed: %v", err)
	}

	removed := false
	for _, call := range runner.calls {
		if len(call.args) >= 2 && call.args[0] == "worktree" && call.args[1] == "remove" {
			removed = true
		}
	}
	if !removed {
		t.Fatal("Clean did not reclaim a legacy pre-upgrade Zero worktree lacking an ownership marker")
	}
}
