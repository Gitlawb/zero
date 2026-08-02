package oauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/Gitlawb/zero/internal/lockutil"
)

// Lock timing knobs (vars so tests can shorten absolute ceilings without
// changing production defaults).
var (
	// fileLockTimeout is how long acquisition waits after the last sign of a
	// healthy holder (or when the lock path cannot be stated). A multi-entry
	// keyring pass can legitimately run several 10s OS commands while refreshing
	// the lease; contenders must not give up while that lease stays healthy.
	// While the holder's mtime stays within fileLockStaleAfter, this idle
	// deadline is extended so a fixed 5s window cannot fail a healthy peer.
	fileLockTimeout = 5 * time.Second
	// fileLockStaleAfter is how old a lock file's mtime must be before a waiter
	// may reclaim it as abandoned. Must stay above one keyring command timeout
	// plus lease refresh slack (holders refresh every fileLockRefreshInterval).
	fileLockStaleAfter = 30 * time.Second
)

var lockSeq atomic.Uint64

// acquireFileLock takes a cross-process exclusive lock by creating lockPath with
// O_EXCL. It retries with a short backoff while a live holder's lease remains
// healthy (mtime refreshed within fileLockStaleAfter), reclaiming only a lock
// older than that threshold so a crashed holder cannot deadlock the store.
// Release is ownership-aware: it removes the lock only if it still holds our
// token, so a stale-broken holder cannot delete a newer holder's lock.
//
// Timing always uses the real wall clock, never the now parameter: now is
// StoreOptions.Now, which callers may legitimately fix (e.g. a test or an
// embedded clock). Measuring the deadline with that clock would either never
// fire (fixed clock) or diverge from the mtime lease stamps (wall-clock).
func acquireFileLock(lockPath string, now func() time.Time) (func(), error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	token := fmt.Sprintf("%d-%d-%d", os.Getpid(), now().UnixNano(), lockSeq.Add(1))
	idleDeadline := time.Now().Add(fileLockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// A partial/failed write would leave a lock file without our token, so
			// the releaser below could never delete it — stranding the lock for
			// other processes. Fail closed: remove the file and surface the error.
			if _, werr := f.WriteString(token); werr != nil {
				_ = f.Close()
				_ = lockutil.RemoveLockFile(lockPath)
				return nil, fmt.Errorf("oauth: write token lock: %w", werr)
			}
			if cerr := f.Close(); cerr != nil {
				_ = lockutil.RemoveLockFile(lockPath)
				return nil, fmt.Errorf("oauth: close token lock: %w", cerr)
			}
			var released bool
			return func() {
				if released {
					return
				}
				released = true
				if data, rerr := os.ReadFile(lockPath); rerr == nil && string(data) == token {
					_ = lockutil.RemoveLockFile(lockPath)
				}
			}, nil
		}
		// On Windows a concurrent holder's os.Remove leaves the lock file in a
		// "delete pending" state, so an O_EXCL create races it with
		// ERROR_ACCESS_DENIED (os.ErrPermission) rather than ErrExist. Treat that
		// as contention and retry, exactly like ErrExist — otherwise the lock
		// spuriously fails under concurrency on Windows.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("oauth: acquire token lock: %w", err)
		}
		// Reclaim a stale lock left by a crashed holder — atomically (H3). A blind
		// Remove lets two racers both reclaim + recreate and so both hold the lock;
		// reclaimStaleLock renames the file aside (only one rename wins) and restores
		// it if it turns out fresh, so a live lock is never deleted out from under it.
		if info, statErr := os.Stat(lockPath); statErr == nil {
			if time.Since(info.ModTime()) > fileLockStaleAfter {
				cleared, rerr := lockutil.ReclaimStaleLock(lockPath, token, func(reclaimedPath string) bool {
					info, err := os.Stat(reclaimedPath)
					return err == nil && time.Since(info.ModTime()) <= fileLockStaleAfter
				})
				if rerr != nil {
					// Reclaim hit a hard failure: the rename aside failed outright, or a
					// live holder's lock could not be put back (the lock path may be
					// missing, so re-acquiring would break mutual exclusion). Fail closed
					// instead of spinning to the deadline.
					return nil, fmt.Errorf("oauth: reclaim stale token lock: %w", rerr)
				}
				if cleared {
					continue
				}
				// Lost the reclaim race (or it was actually fresh) — fall through.
			} else {
				// Holder looks healthy (lease refreshed recently). Keep waiting for
				// the critical section to finish rather than timing out after a fixed
				// window shorter than a legitimate multi-entry keyring pass.
				idleDeadline = time.Now().Add(fileLockTimeout)
			}
		}
		if time.Now().After(idleDeadline) {
			return nil, fmt.Errorf("oauth: timed out acquiring token lock %s", filepath.Base(lockPath))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
