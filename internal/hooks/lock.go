package hooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Cross-process lock tuning for the audit log. The lock is held only across a
// single read-then-append (milliseconds), so the timeout is generous and the
// stale threshold sits far above any real hold.
const (
	auditLockTimeout    = 10 * time.Second
	auditLockStaleAfter = 60 * time.Second
	auditLockRetryDelay = 20 * time.Millisecond
)

var auditLockSeq atomic.Uint64

// lockAudit takes a cross-process exclusive lock on the audit log by
// O_EXCL-creating a sibling "<auditPath>.lock" file (removed on release). It makes
// the lastSequence()+append in append() atomic across processes; the audit log is
// a shared global file, so without it two processes can read the same last
// sequence and write duplicate numbers. A stale lock from a crashed holder (older
// than auditLockStaleAfter) is reclaimed. Wall-clock time is used deliberately so
// lock timing never depends on the store's injectable clock and the stale check
// compares against real file mtimes. This mirrors internal/cron/lock.go and
// internal/oauth/lock.go (a shared filelock helper would DRY all three — left as a
// follow-up to avoid churning those already-green packages here).
func (store *AuditStore) lockAudit() (func(), error) {
	lockPath := store.auditPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	token := fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), auditLockSeq.Add(1))
	deadline := time.Now().Add(auditLockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// A partial write would leave a lock file without our token, so the
			// releaser could never delete it — stranding the lock. Fail closed.
			if _, werr := f.WriteString(token); werr != nil {
				_ = f.Close()
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("hooks: write audit lock: %w", werr)
			}
			if cerr := f.Close(); cerr != nil {
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("hooks: close audit lock: %w", cerr)
			}
			var released bool
			return func() {
				if released {
					return
				}
				released = true
				// Only remove if the file still carries OUR token, so a lock
				// reclaimed as stale by another process is not deleted under it.
				if data, rerr := os.ReadFile(lockPath); rerr == nil && string(data) == token {
					_ = os.Remove(lockPath)
				}
			}, nil
		}
		// On Windows a concurrent holder's os.Remove leaves the lock file in a
		// "delete pending" state, so an O_EXCL create races it with
		// ERROR_ACCESS_DENIED (os.ErrPermission) rather than ErrExist. Treat both
		// as contention and retry.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("hooks: acquire audit lock: %w", err)
		}
		// Reclaim a stale lock left by a crashed holder — atomically (H3). A blind
		// Remove lets two racers both reclaim + recreate and so both hold the lock;
		// reclaimStaleLock renames the file aside (only one rename wins) and restores
		// it if it turns out fresh, so a live lock is never deleted out from under it.
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > auditLockStaleAfter {
			if reclaimStaleLock(lockPath, token, auditLockStaleAfter) {
				continue
			}
			// Lost the reclaim race (or it was actually fresh) — fall through to the
			// bounded wait rather than hot-spinning on a reclaim that never wins.
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("hooks: timed out acquiring audit lock")
		}
		time.Sleep(auditLockRetryDelay)
	}
}

// reclaimStaleLock atomically reclaims a stale lock file: it renames the lock to a
// per-acquirer unique name (only one racer can rename a given file), then verifies
// the moved file is still stale; if a holder reacquired it in the gap it is
// restored rather than stolen. Returns true only when a genuinely stale lock was
// removed. Mirrors internal/cron/lock.go and internal/oauth/lock.go.
func reclaimStaleLock(lockPath, token string, staleAfter time.Duration) bool {
	reclaimed := fmt.Sprintf("%s.stale.%s", lockPath, token)
	if err := os.Rename(lockPath, reclaimed); err != nil {
		return false
	}
	if info, err := os.Stat(reclaimed); err == nil && time.Since(info.ModTime()) <= staleAfter {
		_ = os.Rename(reclaimed, lockPath)
		return false
	}
	_ = os.Remove(reclaimed)
	return true
}
