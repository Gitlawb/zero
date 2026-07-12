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
// the destination existing. It stages a full copy under a private name next
// to reclaimed and publishes it to path with publish (the same no-replace
// primitive the caller's platform uses for the primary restore), so path
// never appears in a partially-copied state: a crash between staging and
// publish leaves path exactly as it was (missing or, if a competing holder
// created it, untouched), never a truncated file that a PID/content-based
// liveness check could mistake for dead. Leaving path missing here would let
// the next O_EXCL create succeed while the sidelined holder is still live,
// breaking mutual exclusion. publish keeps the no-overwrite guarantee: a new
// holder that appeared in the meantime wins and this returns os.ErrExist. The
// copy resets the lock's mtime to now, which only makes the restored lock
// look fresher; that is safe, since it is being handed back to a live holder.
func restoreByCopy(reclaimed, path string, publish func(from, to string) error) error {
	staged := reclaimed + ".copy"
	src, err := os.Open(reclaimed)
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = src.Close()
		return err
	}
	_, err = io.Copy(dst, src)
	// Close the source before removing anything below: Go opens files without
	// FILE_SHARE_DELETE on Windows, so deleting reclaimed or staged while src
	// is open would fail with a sharing violation.
	_ = src.Close()
	if err != nil {
		_ = dst.Close()
		_ = os.Remove(staged)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(staged)
		return err
	}
	if err := publish(staged, path); err != nil {
		_ = os.Remove(staged)
		return err
	}
	// publish may leave staged behind (a hard-link publish keeps both names),
	// so remove it explicitly; a move-based publish already made this a no-op.
	_ = os.Remove(staged)
	// The lock is back at path, so the restore has succeeded; failing to clean
	// up the sidelined name must not be reported as a failed restore.
	_ = RemoveLockFile(reclaimed)
	return nil
}
