// Package lockutil provides the platform-specific file primitives behind the
// O_EXCL lock files in cron, daemon, hooks, oauth, and swarm: a no-overwrite
// restore for locks that were sidelined during a stale-reclaim attempt, and a
// lock file remover with one cross-platform contract (missing files are a
// no-op; Windows retries transient sharing violations).
package lockutil

import (
	"io"
	"os"
)

// restoreByCopy restores reclaimed to path without overwriting an existing
// path, as a fallback for when the platform's primary no-replace primitive
// (hard link on POSIX, MoveFileEx on Windows) fails for a reason other than
// the destination existing. Leaving path missing there would let the next
// O_EXCL create succeed while the sidelined holder is still live, breaking
// mutual exclusion. The O_EXCL create keeps the no-overwrite guarantee: a new
// holder that appeared in the meantime wins and this fails with os.ErrExist.
// The copy resets the lock's mtime to now, which only makes the restored lock
// look fresher; that is safe, since it is being handed back to a live holder.
func restoreByCopy(reclaimed, path string) error {
	src, err := os.Open(reclaimed)
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = src.Close()
		return err
	}
	_, err = io.Copy(dst, src)
	// Close the source before removing it below: Go opens files without
	// FILE_SHARE_DELETE on Windows, so deleting reclaimed while src is open
	// would fail with a sharing violation.
	_ = src.Close()
	if err != nil {
		_ = dst.Close()
		_ = os.Remove(path)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	// The lock is back at path, so the restore has succeeded; failing to clean
	// up the sidelined name must not be reported as a failed restore.
	_ = RemoveLockFile(reclaimed)
	return nil
}
