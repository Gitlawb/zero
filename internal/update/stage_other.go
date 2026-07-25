//go:build !windows

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stagingDirPrefix names the private directories createStagedBinary makes next
// to the target binary. CleanupStaleBinary sweeps stale ones.
const stagingDirPrefix = ".zero-stage-"

// createStagedBinary stages inside a private directory next to targetPath rather
// than directly beside the binary. os.MkdirTemp creates that directory with mode
// 0700 under a random name it also creates exclusively, so a lower-privileged
// principal who can write in the installation directory can neither pre-create
// it nor create, replace, or list entries inside it afterwards. That is what
// keeps the staged pathname bound to the object holding the verified bytes right
// through the rename: POSIX has no rename-by-descriptor, so the only way to stop
// the entry from being substituted between the write and the swap is to put it
// somewhere the attacker cannot reach.
//
// The directory sits in the target's own directory so the promoting rename stays
// on one filesystem and therefore atomic.
func createStagedBinary(targetPath string) (*stagedBinary, error) {
	dir, err := os.MkdirTemp(filepath.Dir(targetPath), stagingDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	path := filepath.Join(dir, filepath.Base(targetPath))
	file, err := createStagingFile(path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &stagedBinary{file: file, path: path, dir: dir}, nil
}

// createStagingFile creates path exclusively so a pre-existing hard link or
// symlink at that path (which a lower-privileged attacker may have staged in
// a writable installation directory) can never be opened through: per POSIX,
// O_CREAT|O_EXCL fails with EEXIST if path already exists — including a
// dangling symlink — without following it.
func createStagingFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
}

// promote makes the staged object the installed binary. Renaming over a running
// executable is safe on POSIX: the process executing it keeps its open inode,
// and the rename is atomic within one filesystem.
//
// The executable bit is set through the HANDLE, not the pathname: os.Chmod would
// re-resolve the staging path and follow whatever it names at that moment. The
// identity check that follows is defense in depth — the private 0700 staging
// directory should already make substitution impossible — and it fails closed if
// the entry ever stops naming the object whose bytes were verified.
func (staged *stagedBinary) promote(targetPath string) error {
	if err := staged.file.Chmod(0o755); err != nil {
		return err
	}
	if err := staged.verifyStagedIdentity(); err != nil {
		return err
	}
	if err := os.Rename(staged.path, targetPath); err != nil {
		return err
	}
	staged.path = targetPath
	staged.promoted = true
	return nil
}

// verifyStagedIdentity reports whether the staging pathname still names the very
// object the handle refers to.
func (staged *stagedBinary) verifyStagedIdentity() error {
	handleInfo, err := staged.file.Stat()
	if err != nil {
		return fmt.Errorf("stat staged binary: %w", err)
	}
	pathInfo, err := os.Lstat(staged.path)
	if err != nil {
		return fmt.Errorf("stat staged binary path %s: %w", staged.path, err)
	}
	if !os.SameFile(handleInfo, pathInfo) {
		return fmt.Errorf("staged binary %s was replaced after it was written", staged.path)
	}
	return nil
}

// CleanupStaleBinary removes staging directories a crashed or killed update left
// behind next to targetPath. Outside Windows there is no ".old" file to reclaim —
// POSIX replaces the running binary directly — but the private staging
// directories would otherwise accumulate, since each attempt uses a fresh random
// name. Only directories older than stagingLeftoverMinAge are touched so a
// concurrently running update is never disturbed. Callers invoke this once at
// startup for the current executable.
func CleanupStaleBinary(targetPath string) {
	removeStaleStagingLeftovers(targetPath, time.Now())
}

// stagingLeftoverMinAge is how long a leftover must sit untouched before it is
// treated as abandoned rather than as another process's work in progress.
const stagingLeftoverMinAge = time.Hour

func removeStaleStagingLeftovers(targetPath string, now time.Time) {
	dir := filepath.Dir(targetPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), stagingDirPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < stagingLeftoverMinAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
	}
}
