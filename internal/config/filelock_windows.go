//go:build windows

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// acquireConfigLock takes an exclusive OS lock (LockFileEx) serializing config
// read-modify-write transactions, matching the flock behaviour on unix. The
// lock file is SEPARATE from the data file because writeConfigData publishes
// via rename, and a lock on the renamed file would attach to an inode the next
// writer has already replaced. Mirrors credstore's acquireFileLock, which
// documents the same invariant for the same reason. Blocking (no
// LOCKFILE_FAIL_IMMEDIATELY) so a concurrent writer queues rather than failing
// the operation.
func acquireConfigLock(configPath string) (func() error, error) {
	dir := filepath.Dir(configPath)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config directory %s: %w", dir, err)
	}
	file, err := os.OpenFile(configPath+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock: %w", err)
	}
	handle := windows.Handle(file.Fd())
	overlapped := new(windows.Overlapped)
	// Fixed 1-byte region, exclusive, blocking: a waiter queues rather than
	// failing its config write.
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if err := windows.LockFileEx(handle, flags, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock config: %w", err)
	}
	return func() error {
		// Reported rather than swallowed: a release that did not complete must
		// not look identical to one that did, and on Windows the handle staying
		// open is what blocks the next writer's rename.
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		closeErr := file.Close()
		if err := errors.Join(unlockErr, closeErr); err != nil {
			return fmt.Errorf("release config lock: %w", err)
		}
		return nil
	}, nil
}
