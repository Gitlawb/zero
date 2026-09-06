//go:build !windows

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// acquireConfigLock takes an exclusive advisory lock (flock) on a lock file
// SEPARATE from the config file, serializing config read-modify-write
// transactions across processes AND goroutines (flock is held per open file
// description, so two opens in one process contend exactly as two processes
// do). The lock file is never renamed or removed: writeConfigData publishes
// via os.Rename, which replaces the data file's inode, so a lock taken on the
// data file itself would attach to an inode the next writer has already
// replaced and every writer would appear to hold it. Mirrors credstore's
// acquireFileLock, which documents the same invariant for the same reason.
//
// Blocking, not try-lock: a caller mid-transaction is expected to finish in
// milliseconds, and a failed `zero config notify --mode off` because a
// concurrent write held the lock would be worse than waiting briefly.
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
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock config: %w", err)
	}
	return func() error {
		// Close alone drops the flock; the explicit unlock is belt-and-braces,
		// and its failure is reported rather than swallowed so a cleanup that
		// did not complete is distinguishable from one that did.
		unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeErr := file.Close()
		if err := errors.Join(unlockErr, closeErr); err != nil {
			return fmt.Errorf("release config lock: %w", err)
		}
		return nil
	}, nil
}
