//go:build !windows

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
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
//
// The directory is also opened here and kept open for promote's final rename.
// A writable-parent principal cannot write INSIDE the 0700 directory, but they
// can still rename the directory ENTRY itself out of the way and recreate a
// look-alike at the same path — a pathname lookup at rename time would then
// resolve through the impostor. The open descriptor is bound to the directory's
// inode, not its current name, so promote's renameat call keeps finding this
// directory's own child no matter what a writable-parent principal does to the
// pathname in between.
func createStagedBinary(targetPath string) (*stagedBinary, error) {
	dir, err := os.MkdirTemp(filepath.Dir(targetPath), stagingDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("open staging directory: %w", err)
	}
	path := filepath.Join(dir, filepath.Base(targetPath))
	file, err := createStagingFile(path)
	if err != nil {
		_ = dirHandle.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &stagedBinary{file: file, path: path, dir: dir, dirHandle: dirHandle}, nil
}

// refreshLiveness bumps the staging directory's mtime, which
// removeStaleStagingLeftovers reads as a liveness signal. Writing INTO an
// already-created file (what copyFrom's io.CopyN loop does) never touches the
// PARENT directory's own mtime — only creating/removing/renaming a directory
// ENTRY does — so without this a large copy or a slow disk can leave the
// directory looking abandoned while it is still being written, and a second
// `zero upgrade` running concurrently would delete it out from under the first.
func (staged *stagedBinary) refreshLiveness() {
	if staged.dir == "" {
		return
	}
	now := time.Now()
	_ = os.Chtimes(staged.dir, now, now)
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
//
// The final rename goes through staged.dirHandle (renameat), not a plain
// pathname rename: a pathname rename re-resolves staged.path's PARENT directory
// fresh, so a principal who can write in the installation directory could
// rename the staging directory out of the way and recreate a look-alike at the
// same path in the gap between verifyStagedIdentity returning and the rename
// running — this closes that gap by binding the rename to the exact directory
// inode identity was already checked against.
func (staged *stagedBinary) promote(targetPath string) error {
	if err := staged.file.Chmod(0o755); err != nil {
		return err
	}
	if err := staged.verifyStagedIdentity(); err != nil {
		return err
	}
	dirFd := int(staged.dirHandle.Fd())
	base := filepath.Base(staged.path)
	// targetPath is absolute, so the newdirfd argument is ignored per renameat(2)
	// and AT_FDCWD is only a conventional placeholder.
	if err := unix.Renameat(dirFd, base, unix.AT_FDCWD, targetPath); err != nil {
		return fmt.Errorf("rename staged binary onto %s: %w", targetPath, err)
	}
	staged.path = targetPath
	staged.promoted = true
	return nil
}

// verifyStagedIdentity reports whether the staging directory's child still
// names the very object the handle refers to. It resolves that child through
// staged.dirHandle (fstatat), not by re-walking staged.path from the
// filesystem root, so the check itself cannot be fooled by an ancestor
// directory swap the same way a plain Lstat could be.
func (staged *stagedBinary) verifyStagedIdentity() error {
	var handleStat unix.Stat_t
	if err := unix.Fstat(int(staged.file.Fd()), &handleStat); err != nil {
		return fmt.Errorf("stat staged binary: %w", err)
	}
	var childStat unix.Stat_t
	base := filepath.Base(staged.path)
	if err := unix.Fstatat(int(staged.dirHandle.Fd()), base, &childStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("stat staged binary path %s: %w", staged.path, err)
	}
	if uint64(childStat.Ino) != uint64(handleStat.Ino) || uint64(childStat.Dev) != uint64(handleStat.Dev) {
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
		if !entry.IsDir() || !isGeneratedStagingDirName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < stagingLeftoverMinAge {
			continue
		}
		// Re-check identity immediately before deleting: entry.Info() (and the
		// ReadDir that produced entry) can be arbitrarily old by the time this
		// loop reaches it, and a principal who can write in dir could have
		// swapped path for something else in the meantime. This does not close
		// the race outright — POSIX has no portable recursive-remove-by-
		// descriptor — but it shrinks the window from "since the last sweep" to
		// the gap between these two syscalls.
		path := filepath.Join(dir, entry.Name())
		recheck, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, recheck) {
			continue
		}
		_ = os.RemoveAll(path)
	}
}

// isGeneratedStagingDirName reports whether name is exactly the shape
// createStagedBinary's os.MkdirTemp call generates: stagingDirPrefix followed
// by os.MkdirTemp's random suffix, which is always 1-10 ASCII digits (the
// decimal encoding of a uint32 — see os.nextRandom in the standard library).
// Matching only this exact shape, not just the prefix, keeps a user's own
// similarly-named entry (".zero-stage-backup", say) from ever being swept up
// as an abandoned artifact.
func isGeneratedStagingDirName(name string) bool {
	suffix, ok := strings.CutPrefix(name, stagingDirPrefix)
	if !ok || suffix == "" || len(suffix) > 10 {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
