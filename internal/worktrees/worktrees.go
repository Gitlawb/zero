package worktrees

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
}

type Result struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	RepoRoot     string `json:"repoRoot"`
	SourceBranch string `json:"sourceBranch,omitempty"`
	SourceCommit string `json:"sourceCommit,omitempty"`
	Reused       bool   `json:"reused"`
}

var worktreeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)

func Prepare(ctx context.Context, options Options) (Result, error) {
	// Clean up stale worktrees (older than 24 hours) automatically to prevent disk space leaks.
	if options.RunGit == nil {
		_ = Clean(ctx, options, 24*time.Hour)
	}

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
	// the worktrees zero creates.
	lockResult, err := runGit(ctx, repoRoot, "worktree", "lock", "--reason", "zero: active task worktree", target)
	if err != nil {
		return Result{}, fmt.Errorf("lock git worktree: %w", err)
	}
	if lockResult.ExitCode != 0 {
		message := strings.TrimSpace(firstNonEmpty(lockResult.Stderr, lockResult.Stdout))
		if message == "" {
			message = fmt.Sprintf("git worktree lock exited with code %d", lockResult.ExitCode)
		}
		return Result{}, fmt.Errorf("lock git worktree: %s", message)
	}
	return result, nil
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
		// A worktree a caller has explicitly locked (git worktree lock) is
		// never a prune candidate, regardless of its mtime.
		if entry.locked {
			continue
		}

		// Only prune worktrees zero created for this repository (i.e. inside
		// repoDir), using a path-boundary-safe comparison so a sibling
		// directory that merely shares repoDir as a string prefix (e.g.
		// "<repoDir>-other") can't match.
		if !isUnderDir(entry.path, repoDir) {
			continue
		}

		info, err := os.Stat(entry.path)
		if err != nil {
			if os.IsNotExist(err) {
				_, _ = runGit(ctx, repoRoot, "worktree", "prune")
			}
			continue
		}
		if !info.IsDir() {
			continue
		}

		if worktreeIsStale(entry.path, cutoff) {
			if worktreeIsDirty(ctx, runGit, entry.path) {
				// A stale mtime only means nothing changed at the worktree's
				// top level or below recently; it does not mean the task
				// holding it is done. Uncommitted or untracked changes are
				// still live work waiting on a model, network, or user, so
				// force-removing here would discard it. Skip until it either
				// gets committed/cleaned (no longer dirty) or unlocked.
				continue
			}
			// gitOutput (not a raw runGit call) so a nonzero exit code is
			// reported as a failure: defaultRunGit deliberately returns a nil
			// error alongside a nonzero CommandResult.ExitCode for a failed git
			// invocation, so checking only the returned error would silently
			// treat a failed removal (busy, permission denied) as success.
			if _, err := gitOutput(ctx, runGit, repoRoot, "worktree", "remove", "--force", entry.path); err != nil {
				lastErr = fmt.Errorf("remove worktree %s: %w", entry.path, err)
			}
		}
	}

	_, _ = runGit(ctx, repoRoot, "worktree", "prune")
	return lastErr
}

type worktreeEntry struct {
	path   string
	locked bool
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

// worktreeIsDirty reports whether a worktree has uncommitted, untracked, or
// ignored changes, via `git status --porcelain --ignored` run inside it. A
// task can hold a worktree with live, unpushed work while it waits on a
// model, network, or user for far longer than the staleness window, without
// writing to the tree again in that time; mtime alone can't distinguish that
// from an abandoned one, but a dirty working tree still can. --ignored is
// included because files matched by .gitignore (credentials, generated
// drafts, task artifacts) are real data a task can leave behind; without it,
// plain `git status --porcelain` reports a worktree holding only such files
// as clean, and Clean would force-remove it and silently discard them.
//
// An inspection failure fails closed, treating it as dirty rather than clean:
// an incomplete check must not authorize a forced removal.
func worktreeIsDirty(ctx context.Context, runGit GitRunner, path string) bool {
	output, err := gitOutput(ctx, runGit, path, "status", "--porcelain", "--ignored")
	if err != nil {
		return true
	}
	return output != ""
}
