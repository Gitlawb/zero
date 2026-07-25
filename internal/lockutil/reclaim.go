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
