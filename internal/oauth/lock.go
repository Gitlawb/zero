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
			_, _ = f.WriteString(token)
			_ = f.Close()
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
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("oauth: acquire token lock: %w", err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > fileLockStaleAfter {
			stale, _ := os.ReadFile(lockPath)
			if data, rerr := os.ReadFile(lockPath); rerr == nil && string(data) == string(stale) {
				_ = os.Remove(lockPath)
			}
			continue
		}
		if now().After(deadline) {
			return nil, fmt.Errorf("oauth: timed out acquiring token lock %s", filepath.Base(lockPath))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
