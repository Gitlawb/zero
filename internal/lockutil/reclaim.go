package lockutil

import (
	"errors"
	"os"
)

// restoreLockFile is swappable so tests can force the fail-closed path of
// ReclaimStaleLock, which requires both the no-replace restore and its copy
// fallback to fail; that cannot be provoked portably on a healthy filesystem.
var restoreLockFile = RestoreLockFile

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
// holder's lock could not be restored (both the no-replace restore and its
// copy fallback failed), so lockPath may be missing; callers must fail closed
// instead of re-acquiring. The sidelined file is removed on every restore
// failure: once the restore has failed it has no protocol function (release
// only consults the lock path), so keeping it would only leak files.
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
