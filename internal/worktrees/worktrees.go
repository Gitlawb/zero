package worktrees

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type GitRunner func(context.Context, string, ...string) (CommandResult, error)

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type Options struct {
	Cwd     string
	Name    string
	BaseDir string
	Env     map[string]string
	Now     func() time.Time
	RunGit  GitRunner
	// LeasePID, when positive, records the owning process in the worktree
	// lock reason. A lease carrying a PID is recoverable: if that process
	// dies without releasing (SIGKILL, crash, power loss), Clean can expire
	// the lease instead of skipping the locked worktree forever. Callers
	// whose worktree outlives their process (zero worktrees prepare hands
	// the path to an external owner) leave it zero for a persistent lock
	// that only an explicit release clears.
	LeasePID int
}

type Result struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	RepoRoot     string `json:"repoRoot"`
	SourceBranch string `json:"sourceBranch,omitempty"`
	SourceCommit string `json:"sourceCommit,omitempty"`
	Reused       bool   `json:"reused"`
	// LockAcquired reports whether this Prepare call took the worktree lock.
	// It is false when a reused worktree was already locked by another live
	// caller; releasing that lease is that caller's responsibility, not ours.
	LockAcquired bool `json:"lockAcquired"`
}

var worktreeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)

func Prepare(ctx context.Context, options Options) (Result, error) {
	cwd, err := resolveCwd(options.Cwd)
	if err != nil {
		return Result{}, err
	}
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	name := strings.TrimSpace(options.Name)
	if name == "" {
		name = defaultWorktreeName(now())
	}
	if err := validateName(name); err != nil {
		return Result{}, err
	}

	// Clean up stale worktrees (older than 24 hours) automatically to prevent
	// disk space leaks. Only after the request itself validated: a rejected
	// command (for example an invalid --name) must not have destructive
	// cleanup side effects before reporting its error.
	if options.RunGit == nil {
		_ = Clean(ctx, options, 24*time.Hour)
	}

	repoRoot, err := gitOutput(ctx, runGit, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Result{}, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot = filepath.Clean(repoRoot)
	branch, _ := gitOutput(ctx, runGit, repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	commit, _ := gitOutput(ctx, runGit, repoRoot, "rev-parse", "--short", "HEAD")

	baseDir := strings.TrimSpace(options.BaseDir)
	if baseDir == "" {
		baseDir, err = DefaultBaseDir(options.Env)
		if err != nil {
			return Result{}, err
		}
	}
	baseDir, err = filepath.Abs(baseDir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve worktree dir: %w", err)
	}

	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))
	target := filepath.Join(repoDir, name)
	result := Result{
		Name:         name,
		Path:         target,
		RepoRoot:     repoRoot,
		SourceBranch: branch,
		SourceCommit: commit,
	}
	reused, err := inspectTarget(target)
	if err != nil {
		return Result{}, err
	}
	if reused {
		sameRepo, err := sameGitCommonDir(ctx, runGit, repoRoot, target)
		if err != nil {
			return Result{}, fmt.Errorf("inspect existing worktree repository: %w", err)
		}
		if !sameRepo {
			return Result{}, fmt.Errorf("worktree path already exists for a different git repository: %s", target)
		}
		result.Reused = true
		// A reused worktree may have been released by a prior run's exit, and
		// an unlocked target is exposed to Clean's staleness heuristic while
		// this caller is still using it, so re-establish the lease here. A
		// lock already held means another run is still using the path: two
		// live runs must not share one supposedly isolated checkout (they
		// would edit the same tree, and whichever exits first would release
		// the single Git lock out from under the other), so reject it rather
		// than hand the second caller an unprotected shared workspace.
		acquired, err := lockWorktree(ctx, runGit, repoRoot, target, options.LeasePID)
		if err != nil {
			return Result{}, err
		}
		if !acquired {
			return Result{}, fmt.Errorf("worktree %s is locked by another active run; release it with `zero worktrees release %s` if that run is finished, or use a different --name", target, target)
		}
		result.LockAcquired = true
		return result, nil
	}
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create worktree directory: %w", err)
	}
	commandResult, err := runGit(ctx, repoRoot, "worktree", "add", "--detach", target, "HEAD")
	if err != nil {
		return Result{}, fmt.Errorf("create git worktree: %w", err)
	}
	if commandResult.ExitCode != 0 {
		message := strings.TrimSpace(firstNonEmpty(commandResult.Stderr, commandResult.Stdout))
		if message == "" {
			message = fmt.Sprintf("git worktree add exited with code %d", commandResult.ExitCode)
		}
		return Result{}, fmt.Errorf("create git worktree: %s", message)
	}
	// Lock the worktree so Clean's mtime+dirty staleness heuristic (which
	// cannot tell "abandoned" apart from "clean but waiting on a model,
	// network, or user for a long time") never force-removes a worktree zero
	// itself is still using. entry.locked already makes Clean skip a worktree
	// a human locked by hand; locking here extends that same protection to
	// the worktrees zero creates. An "already locked" answer on a worktree
	// this call just created means another process raced us to it: treat that
	// exactly like the reuse collision above rather than claiming a lease
	// this call never acquired.
	acquired, err := lockWorktree(ctx, runGit, repoRoot, target, options.LeasePID)
	if err != nil {
		return Result{}, err
	}
	if !acquired {
		return Result{}, fmt.Errorf("worktree %s is locked by another active run; release it with `zero worktrees release %s` if that run is finished, or use a different --name", target, target)
	}
	result.LockAcquired = true
	return result, nil
}

// leaseReasonPrefix marks a lock Zero itself created (vs a human `git
// worktree lock`); a "(pid N)" suffix makes the lease recoverable by Clean
// once the owning process is gone.
const leaseReasonPrefix = "zero: active task worktree"

// leaseReason renders the lock reason for a Zero lease, embedding the owning
// PID when the caller's worktree lifetime is bound to its process.
func leaseReason(pid int) string {
	if pid > 0 {
		return fmt.Sprintf("%s (pid %d)", leaseReasonPrefix, pid)
	}
	return leaseReasonPrefix
}

// leasePID extracts the owning PID from a Zero lease reason. ok=false for
// human locks and PID-less Zero leases (external prepare callers), which
// only an explicit release may clear.
func leasePID(reason string) (int, bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(reason), leaseReasonPrefix+" (pid ")
	if !found {
		return 0, false
	}
	digits, found := strings.CutSuffix(rest, ")")
	if !found {
		return 0, false
	}
	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// processAlive reports whether pid is a live process. It fails closed: any
// ambiguity (permission denied, platform limits) counts as alive, so an
// uncertain answer can only keep a lease, never expire one. Detection is
// platform-specific (see osProcessAlive in worktrees_posix.go /
// worktrees_windows.go): os.FindProcess alone cannot tell a dead PID from a
// live one on Windows, since OpenProcess can still succeed for a process that
// has already exited but whose handle has not been fully released.
func processAlive(pid int) bool {
	if pid <= 0 {
		return true
	}
	return osProcessAlive(pid)
}

// lockWorktree takes the Clean-protection lease on target via `git worktree
// lock`. It reports whether this call acquired the lease: a lock already held
// by someone else is left in place and reported as not acquired, so the
// caller knows the matching Release belongs to the lease's original owner.
func lockWorktree(ctx context.Context, runGit GitRunner, repoRoot string, target string, pid int) (bool, error) {
	lockResult, err := runGit(ctx, repoRoot, "worktree", "lock", "--reason", leaseReason(pid), target)
	if err != nil {
		return false, fmt.Errorf("lock git worktree: %w", err)
	}
	if lockResult.ExitCode == 0 {
		return true, nil
	}
	message := strings.TrimSpace(firstNonEmpty(lockResult.Stderr, lockResult.Stdout))
	if strings.Contains(message, "already locked") {
		return false, nil
	}
	if message == "" {
		message = fmt.Sprintf("git worktree lock exited with code %d", lockResult.ExitCode)
	}
	return false, fmt.Errorf("lock git worktree: %s", message)
}

// Release unlocks a worktree that Prepare locked, via `git worktree unlock`,
// making it eligible for Clean's staleness check again. Zero itself only
// knows a worktree's use is over when its own process created it and is now
// exiting (zero exec --worktree calls this via defer); zero worktrees
// prepare hands the path to an external caller with no defined end-of-life,
// so that caller must run `zero worktrees release <path>` itself once done.
// Until it does, the worktree stays locked and Clean will never touch it,
// which is the safe default over silently guessing at liveness from mtimes.
func Release(ctx context.Context, options Options, path string) error {
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	// git needs a working directory that still exists to run in. path itself
	// normally works (it's the worktree being unlocked), but if a caller
	// manually deleted a locked worktree directory instead of releasing it
	// first, path is gone; fall back to options.Cwd (the main repo) so the
	// leaked, now-orphaned lock can still be unlocked and later pruned.
	dir := path
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if cwd, cwdErr := resolveCwd(options.Cwd); cwdErr == nil {
			dir = cwd
		}
	}
	if err := verifyZeroOwnedWorktree(ctx, runGit, dir, path); err != nil {
		if errors.Is(err, errAlreadyUnlocked) {
			return nil
		}
		return err
	}
	if _, err := gitOutput(ctx, runGit, dir, "worktree", "unlock", path); err != nil {
		return fmt.Errorf("unlock git worktree: %w", err)
	}
	return nil
}

// verifyZeroOwnedWorktree confirms path has a zero-worktree-<repoKey> ancestor
// directory component, is a registered worktree of the repository, and (when
// locked) carries a lock reason Zero itself set, so Release cannot be used to
// clear the lock on a worktree a user (or another tool) manages by hand: the
// command is documented as releasing a worktree `prepare` created, not an
// arbitrary git worktree lock. The ancestor-component check stands in for
// reconstructing and comparing a full repoDir, because Release has no
// reliable way to know which --dir a long-gone Prepare call used (the CLI
// never threads BaseDir through to Release, and a custom --dir is not
// recorded anywhere the lock/unlock path can read back); the repoKey
// component is Prepare's actual ownership signature regardless of which
// directory it was created under. The repository root for that key, and the
// lock reason for the ownership check, both come from the same `git worktree
// list --porcelain` call, which works whether dir is the worktree itself or
// the main repository (its first entry is always the main working tree, from
// any worktree, regardless of git-dir layout), so this needs no branching on
// which of Release's two cwd cases is in play.
func verifyZeroOwnedWorktree(ctx context.Context, runGit GitRunner, dir string, path string) error {
	output, err := gitOutput(ctx, runGit, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return fmt.Errorf("resolve repository for %s: %w", path, err)
	}
	entries := parseWorktreeList(output)
	if len(entries) == 0 {
		return fmt.Errorf("resolve repository for %s: git worktree list returned no entries", path)
	}
	want := "zero-worktree-" + repoKey(entries[0].path)

	target := canonicalizePath(path)

	hasZeroComponent := false
	for _, component := range strings.Split(target, string(filepath.Separator)) {
		if component == want {
			hasZeroComponent = true
			break
		}
	}
	if !hasZeroComponent {
		return fmt.Errorf("refusing to release %s: not a zero-managed worktree (expected an ancestor directory named %q)", path, want)
	}

	// Require a registered porcelain entry before unlocking. Matching only
	// the public zero-worktree-<repoKey> path component would let Release
	// clear a manual lock on any same-named path under that directory.
	// Path comparison uses canonicalizePath so a lexical user argument
	// (macOS /var vs /private/var, symlink --worktree-dir) still matches
	// the physical spelling git worktree list reports.
	matched := false
	for _, entry := range entries {
		if canonicalizePath(entry.path) != target {
			continue
		}
		matched = true
		if !entry.locked {
			// Already unlocked: nothing to release. Treat as success so a
			// double release is a no-op rather than a git "not locked" error.
			return errAlreadyUnlocked
		}
		if !strings.HasPrefix(entry.lockReason, leaseReasonPrefix) {
			return fmt.Errorf("refusing to release %s: locked with reason %q, not a zero lease", path, entry.lockReason)
		}
		break
	}
	if !matched {
		return fmt.Errorf("refusing to release %s: not a registered worktree of this repository", path)
	}
	return nil
}

// errAlreadyUnlocked is a sentinel for a Zero-managed path whose git lock is
// already clear; Release returns nil without calling unlock.
var errAlreadyUnlocked = errors.New("worktree already unlocked")

// canonicalizePath returns the physical, cleaned form of path when it can be
// resolved (EvalSymlinks), otherwise Abs+Clean. Used so comparisons against
// `git worktree list --porcelain` (which reports physical paths) succeed for
// lexical or symlinked user spellings of the same location.
func canonicalizePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func DefaultBaseDir(env map[string]string) (string, error) {
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(envValue(env, "LOCALAPPDATA")); localAppData != "" {
			return filepath.Join(localAppData, "zero", "worktrees"), nil
		}
		if profile := strings.TrimSpace(envValue(env, "USERPROFILE")); profile != "" {
			return filepath.Join(profile, "AppData", "Local", "zero", "worktrees"), nil
		}
	}

	if stateHome := strings.TrimSpace(envValue(env, "XDG_STATE_HOME")); stateHome != "" {
		return filepath.Join(stateHome, "zero", "worktrees"), nil
	}
	home := strings.TrimSpace(firstNonEmpty(envValue(env, "HOME"), envValue(env, "USERPROFILE")))
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
	}
	return filepath.Join(home, ".local", "state", "zero", "worktrees"), nil
}

func resolveCwd(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
	}
	absolute, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("cwd must be an existing directory: %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

func validateName(name string) error {
	if !worktreeNamePattern.MatchString(name) || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid worktree name %q: use letters, numbers, dots, dashes, or underscores", name)
	}
	return nil
}

func inspectTarget(target string) (bool, error) {
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect worktree path: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("worktree path already exists and is not a directory: %s", target)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		return true, nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return false, fmt.Errorf("inspect worktree directory: %w", err)
	}
	if len(entries) != 0 {
		return false, fmt.Errorf("worktree path already exists and is not empty: %s", target)
	}
	return false, nil
}

func gitOutput(ctx context.Context, runGit GitRunner, dir string, args ...string) (string, error) {
	result, err := runGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout))
		if message == "" {
			message = fmt.Sprintf("git exited with code %d", result.ExitCode)
		}
		return "", fmt.Errorf("%s", message)
	}
	return strings.TrimSpace(result.Stdout), nil
}

func sameGitCommonDir(ctx context.Context, runGit GitRunner, sourceDir string, targetDir string) (bool, error) {
	sourceCommonDir, err := gitCommonDir(ctx, runGit, sourceDir)
	if err != nil {
		return false, err
	}
	targetCommonDir, err := gitCommonDir(ctx, runGit, targetDir)
	if err != nil {
		return false, err
	}
	return sourceCommonDir == targetCommonDir, nil
}

func gitCommonDir(ctx context.Context, runGit GitRunner, dir string) (string, error) {
	value, err := gitOutput(ctx, runGit, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(dir, value)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve git common dir: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve git common dir: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func defaultRunGit(ctx context.Context, dir string, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	// Capture stdout and stderr separately: callers parse Stdout for values
	// (rev-parse output) and prefer Stderr for error messages. CombinedOutput
	// merged the two, letting git's stderr warnings pollute parsed output and
	// leaving CommandResult.Stderr always empty.
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
			err = nil
		}
	}
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, err
}

func defaultWorktreeName(now time.Time) string {
	return "task-" + now.UTC().Format("20060102-150405")
}

func repoKey(repoRoot string) string {
	sum := sha1.Sum([]byte(filepath.Clean(repoRoot)))
	hash := hex.EncodeToString(sum[:])[:10]
	base := filepath.Base(repoRoot)
	base = strings.ToLower(base)
	base = strings.Trim(regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(base, "-"), "-._")
	if base == "" {
		base = "repo"
	}
	return base + "-" + hash
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// Clean prunes any zero-owned git worktrees older than maxAge to prevent disk space leaks.
func Clean(ctx context.Context, options Options, maxAge time.Duration) error {
	cwd, err := resolveCwd(options.Cwd)
	if err != nil {
		return err
	}
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	repoRoot, err := gitOutput(ctx, runGit, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot = filepath.Clean(repoRoot)

	baseDir := strings.TrimSpace(options.BaseDir)
	if baseDir == "" {
		baseDir, err = DefaultBaseDir(options.Env)
		if err != nil {
			return err
		}
	}
	baseDir, err = filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	// git worktree list --porcelain reports each worktree's PHYSICAL location,
	// resolving any symlink component (for example a --worktree-dir that is
	// itself a symlink, or a symlinked ancestor). Comparing entry paths against
	// a merely-absolute (but not symlink-resolved) baseDir would then reject
	// every worktree Prepare actually created under a symlinked base, leaving
	// them permanently unprunable. EvalSymlinks failing (most commonly because
	// the directory does not exist yet) just means there is nothing under it
	// to prune, so falling back to the plain absolute path is safe.
	if resolved, err := filepath.EvalSymlinks(baseDir); err == nil {
		baseDir = resolved
	}
	// Prepare only ever creates worktrees under this per-repository subtree
	// (mirroring the repoDir it computes). Scoping pruning to baseDir itself
	// would authorize deleting a worktree a user manages by hand in the same
	// directory, which Zero never created and has no business force-removing.
	repoDir := filepath.Join(baseDir, "zero-worktree-"+repoKey(repoRoot))

	output, err := gitOutput(ctx, runGit, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return fmt.Errorf("list git worktrees: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	var lastErr error
	for _, entry := range parseWorktreeList(output) {
		// Compare against the physical spelling of the entry path. Git's
		// porcelain listing is normally already physical, but test fixtures
		// and some symlink layouts can leave a logical spelling that would
		// otherwise fail the containment check against the resolved repoDir.
		// Git commands still receive entry.path (the registered spelling).
		entryPath := canonicalizePath(entry.path)
		// Only prune worktrees zero created for this repository (i.e. inside
		// repoDir), using a path-boundary-safe comparison so a sibling
		// directory that merely shares repoDir as a string prefix (e.g.
		// "<repoDir>-other") can't match.
		if !isUnderDir(entryPath, repoDir) {
			continue
		}

		// A locked worktree is never a prune candidate - with one recovery
		// carve-out: a Zero lease whose recorded owner process is provably
		// dead (SIGKILL, crash, power loss skipped the deferred release).
		// Without it, an abnormal exit leaves the lock in place forever and
		// Clean can never reclaim the disk. Human locks and PID-less Zero
		// leases (external prepare callers) are always honored.
		expiredLease := false
		if entry.locked {
			pid, ok := leasePID(entry.lockReason)
			if !ok || processAlive(pid) {
				continue
			}
			expiredLease = true
		}

		// Prefer the physical path for filesystem probes when the entry
		// resolves; fall back to the registered spelling if it does not
		// (for example a prunable entry whose directory is already gone).
		statPath := entryPath
		info, err := os.Stat(statPath)
		if err != nil && statPath != entry.path {
			statPath = entry.path
			info, err = os.Stat(statPath)
		}
		if err != nil {
			if os.IsNotExist(err) {
				_, _ = runGit(ctx, repoRoot, "worktree", "prune")
			}
			continue
		}
		if !info.IsDir() {
			continue
		}

		if worktreeIsStale(statPath, cutoff) {
			// An explicit release is the owner's completion signal, so a
			// worktree holding only gitignored residue (node_modules, build
			// output) after release is reclaimable - otherwise every released
			// worktree with such artifacts leaks forever. An expired lease is
			// NOT a completion signal (the task may have died mid-work), so
			// there ignored files still count as live data.
			if worktreeIsDirty(ctx, runGit, statPath, expiredLease) {
				// A stale mtime only means nothing changed at the worktree's
				// top level or below recently; it does not mean the task
				// holding it is done. Uncommitted or untracked changes are
				// still live work waiting on a model, network, or user, so
				// force-removing here would discard it. Skip until it either
				// gets committed/cleaned (no longer dirty) or unlocked.
				continue
			}
			if err := preserveUnreachableWorktreeHead(ctx, runGit, repoRoot, statPath); err != nil {
				lastErr = errors.Join(lastErr, fmt.Errorf("preserve worktree HEAD %s: %w", entry.path, err))
				continue
			}
			if expiredLease {
				// Recover the dead owner's lease only once every removal
				// guard has passed, so a failed unlock (or a later guard)
				// leaves the lock in place rather than half-recovered.
				if _, err := gitOutput(ctx, runGit, repoRoot, "worktree", "unlock", entry.path); err != nil {
					lastErr = errors.Join(lastErr, fmt.Errorf("recover expired lease %s: %w", entry.path, err))
					continue
				}
			}
			// gitOutput (not a raw runGit call) so a nonzero exit code is
			// reported as a failure: defaultRunGit deliberately returns a nil
			// error alongside a nonzero CommandResult.ExitCode for a failed git
			// invocation, so checking only the returned error would silently
			// treat a failed removal (busy, permission denied) as success.
			if _, err := gitOutput(ctx, runGit, repoRoot, "worktree", "remove", "--force", entry.path); err != nil {
				// errors.Join, not a plain overwrite: multiple stale worktrees can
				// fail removal in the same Clean pass (locking, permissions), and
				// only reporting the last one would hide the others from the caller.
				lastErr = errors.Join(lastErr, fmt.Errorf("remove worktree %s: %w", entry.path, err))
			}
		}
	}

	_, _ = runGit(ctx, repoRoot, "worktree", "prune")
	return lastErr
}

type worktreeEntry struct {
	path       string
	locked     bool
	lockReason string
}

// parseWorktreeList reads `git worktree list --porcelain` output into one
// entry per worktree. Entries are delimited by their own "worktree <path>"
// line rather than by blank-line blocks, so this tolerates both real git
// output (attribute lines plus a blank-line separator) and a minimal listing
// with no separators.
func parseWorktreeList(output string) []worktreeEntry {
	var entries []worktreeEntry
	var current *worktreeEntry
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current != nil {
				entries = append(entries, *current)
			}
			current = &worktreeEntry{path: filepath.Clean(strings.TrimPrefix(line, "worktree "))}
		case current != nil && (line == "locked" || strings.HasPrefix(line, "locked ")):
			current.locked = true
			current.lockReason = strings.TrimSpace(strings.TrimPrefix(line, "locked"))
		}
	}
	if current != nil {
		entries = append(entries, *current)
	}
	return entries
}

// isUnderDir reports whether path is dir itself or a descendant of it. Unlike
// a bare strings.HasPrefix(path, dir), this rejects a sibling that merely
// shares dir as a string prefix (e.g. dir "/a/base" must not match
// "/a/base-other"), and filepath.Rel makes the comparison Windows-correct.
func isUnderDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// preserveUnreachableWorktreeHead guards against `git worktree remove --force`
// silently discarding a commit: Prepare creates every worktree with `worktree
// add --detach`, so its HEAD is a plain commit, not a branch, and nothing
// outside that worktree's own administrative files points at it. If a task
// committed its result there and the worktree goes stale before that commit
// is otherwise referenced (merged, pushed, cherry-picked), force-removing it
// deletes the only ref keeping the commit reachable - it becomes immediately
// eligible for git gc, exactly as if it had never been committed. This checks
// whether some OTHER ref in the repository already contains worktreePath's
// HEAD; if none does, it creates a durable ref for it in the main
// repository's refs namespace before the caller proceeds to remove the
// worktree, so the commit survives (visible under refs/zero/orphaned-worktree)
// even after the worktree itself is gone.
func preserveUnreachableWorktreeHead(ctx context.Context, runGit GitRunner, repoRoot, worktreePath string) error {
	head, err := gitOutput(ctx, runGit, worktreePath, "rev-parse", "HEAD")
	if err != nil {
		// No commit to preserve (an empty/unborn worktree, or one already
		// gone) - nothing for this guard to do; let the caller proceed.
		return nil
	}
	head = strings.TrimSpace(head)
	if head == "" {
		return nil
	}
	contained, err := gitOutput(ctx, runGit, repoRoot, "for-each-ref", "--contains="+head, "--count=1", "--format=%(refname)")
	if err != nil {
		return fmt.Errorf("check ref reachability for %s: %w", head, err)
	}
	if strings.TrimSpace(contained) != "" {
		// Already reachable from some branch/tag; the worktree's own HEAD is
		// redundant and removal cannot orphan the commit.
		return nil
	}
	refName := "refs/zero/orphaned-worktree/" + head
	if _, err := gitOutput(ctx, runGit, repoRoot, "update-ref", refName, head); err != nil {
		return fmt.Errorf("preserve unreachable commit %s: %w", head, err)
	}
	return nil
}

// worktreeIsStale reports whether every file under root was last modified
// before cutoff. A directory's own mtime only changes when an entry is
// added, removed, or renamed directly inside it, not when a long-running
// task edits files deeper in the tree, so checking root's mtime alone can
// mistake an actively-used worktree for stale. Walking the tree and bailing
// out on the first recent entry avoids that false positive.
//
// Any inspection failure (a WalkDir error, or a DirEntry that can't report its
// own info) fails closed: it's treated the same as "not stale," never as
// "stale," so an incomplete inspection can't authorize a forced removal.
func worktreeIsStale(root string, cutoff time.Time) bool {
	stale := true
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			stale = false
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			stale = false
			return filepath.SkipAll
		}
		if info.ModTime().After(cutoff) {
			stale = false
			return filepath.SkipAll
		}
		return nil
	})
	return stale && walkErr == nil
}

// worktreeIsDirty reports whether a worktree has changes that block a forced
// removal, via `git status --porcelain` run inside it. A task can hold a
// worktree with live, unpushed work while it waits on a model, network, or
// user for far longer than the staleness window, without writing to the tree
// again in that time; mtime alone can't distinguish that from an abandoned
// one, but a dirty working tree still can.
//
// includeIgnored additionally counts files matched by .gitignore
// (credentials, generated drafts, task artifacts) as live data. It is set
// when the worktree's owner never signaled completion (an expired crashed
// lease): such files may be all a dead task left behind. It is clear for an
// explicitly released worktree, where ignored residue like node_modules or
// build output would otherwise make the released checkout unreclaimable at
// every age - release is the owner's statement that the task is done.
//
// An inspection failure fails closed, treating it as dirty rather than clean:
// an incomplete check must not authorize a forced removal.
func worktreeIsDirty(ctx context.Context, runGit GitRunner, path string, includeIgnored bool) bool {
	args := []string{"status", "--porcelain"}
	if includeIgnored {
		args = append(args, "--ignored")
	}
	output, err := gitOutput(ctx, runGit, path, args...)
	if err != nil {
		return true
	}
	return output != ""
}
