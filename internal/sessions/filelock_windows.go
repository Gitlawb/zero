//go:build windows

package sessions

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// acquireFileLock takes an exclusive OS lock (LockFileEx) on the session's .lock
// file so concurrent processes — and separate Store instances within one process
// — sharing the same RootDir serialize their session mutations, matching the
// flock behavior on unix. It blocks until the lock is available and returns a
// release function; closing the handle and unlocking the region releases it.
func (store *Store) acquireFileLock(sessionID string) (func(), error) {
	path := store.lockPath(sessionID)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open zero session lock: %w", err)
	}
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	// Lock a fixed 1-byte region. A blocking exclusive lock (no
	// LOCKFILE_FAIL_IMMEDIATELY) waits until any current holder releases.
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock zero session: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}

// tryAcquireFileLock attempts a non-blocking exclusive LockFileEx. ok is false
// when another process already holds session.lock.
func (store *Store) tryAcquireFileLock(sessionID string) (func(), bool, error) {
	path := store.lockPath(sessionID)
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return func() {}, true, nil
		}
		return nil, false, fmt.Errorf("open zero session lock: %w", err)
	}
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	flags := windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	if err := windows.LockFileEx(handle, flags, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, false, nil
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, true, nil
}
