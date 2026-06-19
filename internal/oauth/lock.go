package oauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const (
	fileLockTimeout    = 5 * time.Second
	fileLockStaleAfter = 30 * time.Second
)

var lockSeq atomic.Uint64

// acquireFileLock takes a cross-process exclusive lock by creating lockPath with
// O_EXCL. It retries with a short backoff until a timeout, breaking a lock whose
// file is older than fileLockStaleAfter (so a crashed holder cannot deadlock the
// store). Release is ownership-aware: it removes the lock only if it still holds
// our token, so a stale-broken holder cannot delete a newer holder's lock.
func acquireFileLock(lockPath string, now func() time.Time) (func(), error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	token := fmt.Sprintf("%d-%d-%d", os.Getpid(), now().UnixNano(), lockSeq.Add(1))
	deadline := now().Add(fileLockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// A partial/failed write would leave a lock file without our token, so
			// the releaser below could never delete it — stranding the lock for
			// other processes. Fail closed: remove the file and surface the error.
			if _, werr := f.WriteString(token); werr != nil {
				_ = f.Close()
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("oauth: write token lock: %w", werr)
			}
			if cerr := f.Close(); cerr != nil {
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("oauth: close token lock: %w", cerr)
			}
			var released bool
			return func() {
				if released {
					return
				}
				released = true
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
			return nil, fmt.Errorf("oauth: acquire token lock: %w", err)
		}
		// Reclaim a stale lock left by a crashed holder. (The previous double-read
		// "stale == data" guard compared the file to itself read twice in a row, so
		// it was always true — a no-op; staleness is decided by the mtime check.)
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > fileLockStaleAfter {
			_ = os.Remove(lockPath)
			continue
		}
		if now().After(deadline) {
			return nil, fmt.Errorf("oauth: timed out acquiring token lock %s", filepath.Base(lockPath))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
