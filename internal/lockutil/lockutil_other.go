//go:build !windows

package lockutil

import (
	"errors"
	"io/fs"
	"os"
)

// RestoreLockFile restores a sidelined lock file on non-Windows platforms. It
// uses os.Link so a competing lock created at path in the meantime wins: the
// link fails with os.ErrExist instead of overwriting it. If the link fails for
// any other reason (hardlink-incapable filesystems such as FAT or some
// FUSE/network mounts, ENOSPC, EPERM), it falls back to an O_EXCL copy rather
// than leaving path missing and the live holder's lock stranded in reclaimed.
// Once the lock is back at path the restore has succeeded, so a failed cleanup
// of the sidelined name is not reported as an error; the leftover file is
// invisible to the lock protocol.
func RestoreLockFile(reclaimed, path string) error {
	err := os.Link(reclaimed, path)
	if err == nil {
		_ = RemoveLockFile(reclaimed)
		return nil
	}
	if errors.Is(err, os.ErrExist) {
		return err
	}
	return restoreByCopy(reclaimed, path, os.Link)
}

// isReclaimContended reports whether a failed rename-aside of a suspected
// stale lock means it was lost to a racer rather than a hard failure. POSIX
// rename has no contention errno (a lost race surfaces only as ENOENT, which
// ReclaimStaleLock already treats as benign), so nothing extra is benign here.
func isReclaimContended(error) bool { return false }

// RemoveLockFile removes a lock file on non-Windows platforms. Removing an
// already-missing file reports nil, matching the Windows implementation, so
// callers see one cross-platform contract.
func RemoveLockFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
