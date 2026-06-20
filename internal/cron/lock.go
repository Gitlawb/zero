package cron

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Cross-process lock tuning. The lock is held only for a single metadata
// read-modify-write (milliseconds), never across a job's exec, so the timeout is
// generous and the stale threshold sits far above any real hold.
const (
	cronLockTimeout    = 10 * time.Second
	cronLockStaleAfter = 60 * time.Second
	cronLockRetryDelay = 20 * time.Millisecond
)

var cronLockSeq atomic.Uint64

// lockJob takes a cross-process exclusive lock for one job by O_EXCL-creating a
// sibling "<id>.lock" file next to the job directory (so Remove's RemoveAll of
// the job dir never deletes a live lock). It serializes the read-modify-write of
// a job's metadata across concurrent schedulers and commands. The lock file is
// removed on release; a stale lock from a crashed holder (older than
// cronLockStaleAfter) is reclaimed. Wall-clock time.Now is used deliberately
// (not the injectable Store.now) so lock timing never depends on a frozen test
// clock and the stale check compares against real file mtimes.
func (s *Store) lockJob(id string) (func(), error) {
	if !validID(id) {
		return nil, fmt.Errorf("invalid cron job id %q", id)
	}
	lockPath := s.jobDir(id) + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	token := fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), cronLockSeq.Add(1))
	deadline := time.Now().Add(cronLockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// A partial write would leave a lock file without our token, so the
			// releaser could never delete it — stranding the lock. Fail closed.
			if _, werr := f.WriteString(token); werr != nil {
				_ = f.Close()
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("cron: write job lock: %w", werr)
			}
			if cerr := f.Close(); cerr != nil {
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("cron: close job lock: %w", cerr)
			}
			var released bool
			return func() {
				if released {
					return
				}
				released = true
				// Only remove the file if it still carries OUR token, so a lock
				// reclaimed as stale by another process is not deleted out from under it.
				if data, rerr := os.ReadFile(lockPath); rerr == nil && string(data) == token {
					_ = os.Remove(lockPath)
				}
			}, nil
		}
		// On Windows a concurrent holder's os.Remove leaves the lock file in a
		// "delete pending" state, so an O_EXCL create races it with
		// ERROR_ACCESS_DENIED (os.ErrPermission) rather than ErrExist. Treat that
		// as contention and retry, exactly like ErrExist — otherwise the lock
		// spuriously fails under concurrency on Windows.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("cron: acquire job lock: %w", err)
		}
		// Reclaim a stale lock left by a crashed holder — atomically (H3). A blind
		// Remove lets two racers both "reclaim" and recreate, so both hold the lock;
		// reclaimStaleLock renames the file aside (only one rename of a given file
		// wins) and restores it if it turns out fresh, so a live lock is never deleted
		// out from under its holder.
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > cronLockStaleAfter {
			if reclaimStaleLock(lockPath, token, cronLockStaleAfter) {
				continue // cleared a genuinely stale lock; retry the O_EXCL create now
			}
			// Lost the reclaim race (or it was actually fresh) — fall through to the
			// bounded wait instead of hot-spinning on a reclaim that never wins (L13).
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("cron: timed out acquiring job lock for %q", id)
		}
		time.Sleep(cronLockRetryDelay)
	}
}

// reclaimStaleLock atomically reclaims a stale lock file. It renames the lock to a
// per-acquirer unique name (only one racer can rename a given file, so two racers
// can never both reclaim the same lock), then verifies the moved file is still
// stale; if it became fresh (another holder reacquired in the gap between the
// stale check and the rename) it is restored rather than stolen. Returns true only
// when a genuinely stale lock was removed, so the caller knows it is safe to retry
// the exclusive create immediately; on a lost race it returns false and the caller
// falls through to its bounded wait.
func reclaimStaleLock(lockPath, token string, staleAfter time.Duration) bool {
	reclaimed := fmt.Sprintf("%s.stale.%s", lockPath, token)
	if err := os.Rename(lockPath, reclaimed); err != nil {
		return false // another racer already moved/removed it, or it vanished
	}
	if info, err := os.Stat(reclaimed); err == nil && time.Since(info.ModTime()) <= staleAfter {
		// Not actually stale anymore — a holder reacquired it in the gap. Put it
		// back instead of stealing a live lock, and let the caller wait.
		_ = os.Rename(reclaimed, lockPath)
		return false
	}
	_ = os.Remove(reclaimed)
	return true
}
