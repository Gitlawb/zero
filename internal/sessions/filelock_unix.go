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

// tryAcquireFileLock attempts a non-blocking exclusive flock. ok is false when
// another process already holds session.lock.
func (store *Store) tryAcquireFileLock(sessionID string) (func(), bool, error) {
	path := store.lockPath(sessionID)
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			// No session.lock means nobody holds it. Do not create one: prune
			// --dry-run must not write, and a missing lock is not a holder.
			return func() {}, true, nil
		}
		return nil, false, fmt.Errorf("open zero session lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock zero session: %w", err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, true, nil
}
