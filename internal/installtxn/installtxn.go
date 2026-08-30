// Package installtxn provides the cross-process filesystem transaction used by
// plugin and skill installation. Callers stage content before taking the lock,
// then commit the content swap and lockfile update together while holding it.
package installtxn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const lockFileName = ".zero-install.lock"

// workspacePrefix names the per-transaction workspaces created inside an install
// root. Dot-prefixed so it is never mistaken for an installed plugin or skill.
const workspacePrefix = ".zero-install-txn-"

// targetFileName records, inside a workspace, which install the backup beside it
// belongs to. Without it a workspace left by a killed process holds a tree
// nothing can attribute, and so nothing can put back.
const targetFileName = "target"

// Lock takes the per-install-root cross-process lock. It blocks until any other
// installer or remover using dir has completed.
func Lock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create install dir: %w", err)
	}
	return lockFile(filepath.Join(dir, lockFileName))
}

// StageDir creates an install workspace on the target filesystem. Content must
// be built and validated in the returned stage directory before CommitDir is
// called. cleanup is always safe to call.
func StageDir(dir string) (stage string, cleanup func(), err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", func() {}, fmt.Errorf("create install dir: %w", err)
	}
	workspace, err := os.MkdirTemp(dir, workspacePrefix)
	if err != nil {
		return "", func() {}, fmt.Errorf("create install staging dir: %w", err)
	}
	return filepath.Join(workspace, "staged"), func() { cleanupWorkspace(workspace) }, nil
}

// CommitDir replaces target with staged and runs publish while retaining the
// previous target. If either the swap or publish fails, the previous target is
// restored (or the new target is removed for a first install).
//
// The caller must hold the install-root lock returned by Lock.
func CommitDir(target string, staged string, publish func() error) error {
	workspace := filepath.Dir(staged)
	backup := filepath.Join(workspace, "previous")
	hadPrevious := false
	if _, err := os.Stat(target); err == nil {
		// Record the target before moving its tree. The two renames below cannot
		// be made atomic, so a process killed between them leaves the only copy
		// in the backup, and without this nothing could tell which install it is.
		if err := os.WriteFile(filepath.Join(workspace, targetFileName), []byte(filepath.Base(target)), 0o600); err != nil {
			return fmt.Errorf("record install target: %w", err)
		}
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("retain previous install: %w", err)
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect previous install: %w", err)
	}

	if err := os.Rename(staged, target); err != nil {
		if hadPrevious {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				return errors.Join(fmt.Errorf("publish staged install: %w", err), fmt.Errorf("restore previous install: %w", restoreErr))
			}
		}
		return fmt.Errorf("publish staged install: %w", err)
	}
	if err := publish(); err != nil {
		return rollback(target, backup, hadPrevious, err)
	}
	if hadPrevious {
		_ = os.RemoveAll(backup)
	}
	cleanupWorkspace(workspace)
	return nil
}

// RemoveDir removes target and runs publish while retaining the target until
// publish succeeds. A publish failure restores the directory.
//
// The caller must hold the install-root lock returned by Lock.
func RemoveDir(target string, publish func() error) error {
	workspace, err := os.MkdirTemp(filepath.Dir(target), workspacePrefix)
	if err != nil {
		return fmt.Errorf("create removal staging dir: %w", err)
	}
	defer cleanupWorkspace(workspace)
	backup := filepath.Join(workspace, "previous")
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("retain removed install: %w", err)
	}
	if err := publish(); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore removed install: %w", restoreErr))
		}
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}

// Recover puts back an install that CommitDir set aside but never replaced,
// which is what a process killed between its two renames leaves: the target
// absent and its only copy retained in a workspace nothing else reads. Anything
// already at the target wins, and a workspace whose recorded target it has no
// business naming is left alone rather than acted on. Best effort, since the
// caller can still reinstall from source.
//
// The caller must hold the install-root lock returned by Lock, and EVERY caller
// that takes that lock must call this first. Recovering only on the install
// path is worse than not recovering at all: a removal would then report success
// while the backup it never saw stayed on disk, and the next install would
// publish it again, reinstating something the user deleted. Recovery is
// deliberately an explicit call rather than a side effect of Lock, matching how
// the other staged-swap transactions in this repo invoke their repair pass.
func Recover(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), workspacePrefix) {
			continue
		}
		workspace := filepath.Join(dir, entry.Name())
		backup := filepath.Join(workspace, "previous")
		if _, err := os.Stat(backup); err != nil {
			continue
		}
		name, err := os.ReadFile(filepath.Join(workspace, targetFileName))
		if err != nil {
			continue
		}
		target, ok := recoverableTarget(dir, string(name))
		if !ok {
			continue
		}
		// An install already in place is the newer one by construction: the
		// backup only ever holds the tree that was live before it.
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Rename(backup, target); err != nil {
			continue
		}
		cleanupWorkspace(workspace)
	}
}

// recoverableTarget resolves a recorded target name to a path directly inside
// dir. A name that is not a single path element could name anything on the
// filesystem, so it is refused rather than restored over.
func recoverableTarget(dir string, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return "", false
	}
	return filepath.Join(dir, name), true
}

func rollback(target string, backup string, hadPrevious bool, cause error) error {
	if err := os.RemoveAll(target); err != nil {
		return errors.Join(cause, fmt.Errorf("remove failed install: %w", err))
	}
	if hadPrevious {
		if err := os.Rename(backup, target); err != nil {
			return errors.Join(cause, fmt.Errorf("restore previous install: %w", err))
		}
	}
	return cause
}

// cleanupWorkspace never removes a retained previous install. If rollback was
// unable to restore it (for example because Windows still has a target file
// open), preserving the workspace is safer than turning a recoverable error
// into data loss.
func cleanupWorkspace(workspace string) {
	if _, err := os.Stat(filepath.Join(workspace, "previous")); err == nil {
		return
	}
	_ = os.RemoveAll(workspace)
}

// WriteFileAtomically publishes data by renaming a complete sibling temporary
// file over path. The caller is responsible for any surrounding transaction
// lock.
func WriteFileAtomically(path string, data []byte, perm os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".zero-lockfile-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return replaceFile(tempPath, path)
}
