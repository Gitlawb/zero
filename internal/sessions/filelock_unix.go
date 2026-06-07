//go:build !windows

package sessions

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// acquireFileLock takes an exclusive OS advisory lock (flock) on the session's
// .lock file so concurrent processes sharing the same RootDir (e.g. CLI rewind
// vs TUI) serialize their session mutations. It blocks until the lock is
// available and returns a release function. The lock is held via an open file
// descriptor; closing it releases the lock.
func (store *Store) acquireFileLock(sessionID string) (func(), error) {
	path := store.lockPath(sessionID)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open zero session lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock zero session: %w", err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
