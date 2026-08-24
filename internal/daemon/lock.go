package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/lockutil"
)

// Single-instance lock. The stable PID file carries a kernel advisory lock for
// the daemon's full lifetime. Process exit releases the lock automatically, so
// stale metadata needs no rename/remove recovery protocol.

// ErrAlreadyRunning is returned when a live daemon already holds the lock.
var ErrAlreadyRunning = errors.New("daemon: another instance is already running")

// fileLock is an acquired single-instance lock.
type fileLock struct {
	lock *lockutil.FileLock
}

// processAlive reports whether pid is a live process. Implemented per-platform
// (lock_posix.go / lock_windows.go). It is a package var so tests can stub it.
var processAlive = osProcessAlive

// acquireLock takes the single-instance advisory lock at path. isAlive is used
// only to enrich a contention error with the current holder's PID; the kernel
// lock, not PID metadata, is authoritative.
func acquireLock(path string, isAlive func(pid int) bool) (*fileLock, error) {
	if isAlive == nil {
		isAlive = processAlive
	}
	lock, err := lockutil.TryAcquireFileLock(path)
	if err != nil {
		if !errors.Is(err, lockutil.ErrLockHeld) {
			return nil, err
		}
		pid, perr := readPidFile(path)
		if perr == nil && pid > 0 && isAlive(pid) {
			return nil, fmt.Errorf("%w (pid %d)", ErrAlreadyRunning, pid)
		}
		return nil, ErrAlreadyRunning
	}
	if err := lock.WriteMetadata([]byte(fmt.Sprintf("%d\n", os.Getpid()))); err != nil {
		return nil, errors.Join(err, lock.Release())
	}
	return &fileLock{lock: lock}, nil
}

// release drops the kernel lock and closes its handle. The stable PID file is
// deliberately retained so contenders always address the same inode.
func (l *fileLock) release() error {
	if l == nil || l.lock == nil {
		return nil
	}
	return l.lock.Release()
}

// readPidFile reads and parses the PID recorded in a lock file.
func readPidFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
