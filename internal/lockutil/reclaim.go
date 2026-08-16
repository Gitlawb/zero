package lockutil

import (
	"errors"
	"os"
)

// restoreLockFile is swappable so tests can force the fail-closed path of
// ReclaimStaleLock, which requires both the fast restore and its no-replace
// fallback (with its own copy fallback) to fail; that cannot be provoked
// portably on a healthy filesystem.
var restoreLockFile = restoreLiveLock

// readSidelinedLock is swappable for the same reason: a healthy filesystem
// does not fail a read of a file this process just renamed, so the
// unreadable-lock path of ReclaimStaleLockRooted needs a seam to exercise.
var readSidelinedLock = func(root *os.Root, name string) ([]byte, error) {
	return root.ReadFile(name)
}

// restoreLiveLock puts a lock that turned out to be live back at path after
// ReclaimStaleLock moved it aside to inspect it. It first tries a fast,
// replacing rename straight from reclaimed to path: a single syscall, which
// keeps the window during which path does not exist (and so is open to a
// fourth process's unrelated O_EXCL create landing on it) as short as
// possible. RestoreLockFile's no-replace restore (with its slower copy
// fallback on some failures) is a correctness-preserving fallback for when
// the fast path itself fails (e.g. a cross-device sidelined name): it is a
// much longer version of the identical race, but still detects rather than
// silently clobbers a competing lock, which the fast path's replacing rename
// cannot do. Neither path makes the race impossible, only unlikely; see
// ReclaimStaleLock's doc comment.
func restoreLiveLock(reclaimed, path string) error {
	if err := os.Rename(reclaimed, path); err == nil {
		return nil
	}
	return RestoreLockFile(reclaimed, path)
}

// ReclaimStaleLock atomically reclaims a suspected-stale lock file. It renames
// lockPath aside to "<lockPath>.stale.<suffix>" (only one racer can win the
// rename of a given file, so two racers can never both reclaim the same lock),
// then consults isLive on the moved file; if the lock turns out to be live (a
// holder reacquired it in the gap between the caller's stale check and the
// rename) it is restored rather than stolen. The suffix must be unique per
// acquirer attempt. Returns true only when a genuinely stale lock was removed,
// so the caller knows it is safe to retry its exclusive create immediately; on
// a lost race it returns false. A non-nil error means either the rename aside
// failed for a reason that is not contention (so retrying cannot help and the
// caller should fail fast instead of spinning to its deadline), or a live
// holder's lock could not be restored (both restoreLiveLock's fast path and
// its no-replace fallback failed), so lockPath may be missing; callers must
// fail closed instead of re-acquiring. The sidelined file is removed on every
// restore failure: once the restore has failed it has no protocol function
// (release only consults the lock path), so keeping it would only leak files.
//
// The live-restore path has an inherent, unclosed race: between the rename
// aside above and restoreLiveLock putting the lock back, lockPath does not
// exist, so an unrelated caller's O_EXCL create can legitimately succeed
// there. restoreLiveLock's fast path then silently overwrites that new
// claimant's lock file, which does not corrupt release (it is
// ownership-aware, so the new claimant's later release safely no-ops against
// content it no longer owns) but does mean the new claimant can still run
// its critical section concurrently with the original live holder. Making
// this race actually impossible would need an OS-level advisory lock (flock
// / LockFileEx) held for a holder's whole critical section, checked
// non-destructively instead of by moving the file; restoreLiveLock only
// shrinks the window to roughly one syscall, it does not close it.
func ReclaimStaleLock(lockPath, suffix string, isLive func(reclaimedPath string) bool) (bool, error) {
	reclaimed := lockPath + ".stale." + suffix
	if err := os.Rename(lockPath, reclaimed); err != nil {
		if errors.Is(err, os.ErrNotExist) || isReclaimContended(err) {
			return false, nil // another racer already moved/removed it, or it vanished
		}
		return false, err
	}
	if isLive(reclaimed) {
		// Put the live lock back instead of stealing it, and let the caller wait.
		if rerr := restoreLockFile(reclaimed, lockPath); rerr != nil {
			_ = RemoveLockFile(reclaimed)
			if !errors.Is(rerr, os.ErrExist) {
				return false, rerr
			}
		}
		return false, nil
	}
	_ = RemoveLockFile(reclaimed)
	return true, nil
}

// linkRootedLock is swappable so tests can exercise the hard-link-incapable
// filesystem fallback in restoreRootedLock.
var linkRootedLock = func(root *os.Root, oldname, newname string) error {
	return root.Link(oldname, newname)
}

// ReclaimStaleLockRooted is ReclaimStaleLock for a lock file inside an
// already-opened *os.Root. Every rename/read/remove goes through the root
// handle, so a symlink or reparse point swapped in under lockName after the
// root was opened cannot redirect the operations (the path-based variant
// would re-walk root.Name()+lockName as plain paths). lockName must be a bare
// file name; the root supplies the directory. isLive receives the raw
// sidelined contents and is called only when they were read successfully, so
// it never has to invent a policy for contents it cannot see: an unreadable
// lock is restored here without consulting it.
func ReclaimStaleLockRooted(root *os.Root, lockName, suffix string, isLive func(raw []byte) bool) (bool, error) {
	reclaimed := lockName + ".stale." + suffix
	if err := root.Rename(lockName, reclaimed); err != nil {
		if errors.Is(err, os.ErrNotExist) || isReclaimContended(err) {
			return false, nil // another racer already moved/removed it, or it vanished
		}
		return false, err
	}
	raw, err := readSidelinedLock(root, reclaimed)
	if err != nil {
		// Decide here rather than handing nil to isLive. The callback answers
		// "do these contents describe a live holder", and a caller can very
		// reasonably treat empty or unparseable contents as dead so a holder
		// that crashed mid-write is recoverable: kimiidentity's lockHolderAlive
		// does exactly that. Passing nil then classified an unreadable lock as
		// dead and removed it, which is the opposite of failing closed. A read
		// failure is not proof of death, so restore and let the caller wait.
		if rerr := restoreRootedLock(root, reclaimed, lockName); rerr != nil {
			if errors.Is(rerr, os.ErrExist) {
				_ = root.Remove(reclaimed)
				return false, nil
			}
			return false, rerr
		}
		return false, nil
	}
	if isLive(raw) {
		// Put the live lock back instead of stealing it, and let the caller wait.
		if rerr := restoreRootedLock(root, reclaimed, lockName); rerr != nil {
			if errors.Is(rerr, os.ErrExist) {
				_ = root.Remove(reclaimed)
				return false, nil
			}
			return false, rerr
		}
		return false, nil
	}
	if err := root.Remove(reclaimed); err != nil {
		return false, err
	}
	return true, nil
}

// restoreRootedLock puts a lock that turned out to be live, or that could not
// be read, back at lockName after ReclaimStaleLockRooted moved it aside. It
// tries a no-replace hard link first, so a competing lock created in the gap
// wins (os.ErrExist) instead of being overwritten, then an O_EXCL probe and
// rename for filesystems without hard links without depending on reading contents.
func restoreRootedLock(root *os.Root, reclaimed, lockName string) error {
	if err := linkRootedLock(root, reclaimed, lockName); err == nil {
		// Restore is complete; the sidelined name is now a redundant link.
		// Cleanup is best-effort: a leftover .stale file is harmless, and
		// returning a cleanup error here would report the restore as failed
		// when it actually succeeded.
		_ = root.Remove(reclaimed)
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return err
	}
	// Hard-link incapable filesystem (FAT, some FUSE/network mounts): probe
	// lockName with O_EXCL so we never overwrite a racer. Prefer writing the
	// sidelined contents into the probe first so lockName is never observable
	// as empty; if the read fails, fall back to the empty probe + rename.
	raw, readErr := readSidelinedLock(root, reclaimed)
	probe, err := root.OpenFile(lockName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if readErr == nil && len(raw) > 0 {
		if _, werr := probe.Write(raw); werr == nil {
			_ = probe.Sync()
		}
	}
	_ = probe.Close()
	return root.Rename(reclaimed, lockName)
}
