//go:build windows

package credstore

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// acquireFileLock takes an exclusive OS lock (LockFileEx) so a read-modify-write
// of the credential file is serialized, matching the flock behaviour on unix.
//
// The lock file is SEPARATE from the data file for the same reason: write
// publishes by rename, and a lock on the renamed file would be attached to
// something the next writer has already replaced.
func (s *Store) acquireFileLock() (func(), error) {
	path := s.lockPath()
	if err := os.MkdirAll(filepathDir(path), 0o700); err != nil {
		return nil, fmt.Errorf("credstore: lock dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("credstore: open lock: %w", err)
	}
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	// A fixed 1-byte region, blocking (no LOCKFILE_FAIL_IMMEDIATELY) so a
	// waiter queues rather than failing the write.
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("credstore: lock: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}
