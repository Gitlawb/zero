package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Single-instance lock. Mirrors reference-daemon-code-agent-js/supervisor.js's
// lock file: a PID file created with O_EXCL. A second start fails; a STALE lock
// left by a dead daemon (the recorded PID is no longer alive) is reclaimed so the
// daemon recovers from an unclean shutdown without manual cleanup.

// ErrAlreadyRunning is returned when a live daemon already holds the lock.
var ErrAlreadyRunning = errors.New("daemon: another instance is already running")

// fileLock is an acquired single-instance lock.
type fileLock struct {
	path string
}

// processAlive reports whether pid is a live process. Implemented per-platform
// (lock_posix.go / lock_windows.go). It is a package var so tests can stub it.
var processAlive = osProcessAlive

// acquireLock takes the single-instance lock at path, reclaiming a stale lock
// whose recorded PID is dead. isAlive overrides the liveness check (tests pass a
// stub); nil uses the real processAlive.
func acquireLock(path string, isAlive func(pid int) bool) (*fileLock, error) {
	if isAlive == nil {
		isAlive = processAlive
	}
	// At most two passes: create, or detect-stale-then-retry once.
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// A failed PID write would leave a malformed lock file that another
			// process reads as stale (unparsable PID) and wrongly reclaims, breaking
			// the single-instance guarantee — so on write failure, remove it and fail.
			if _, werr := fmt.Fprintf(f, "%d\n", os.Getpid()); werr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, werr
			}
			if cerr := f.Close(); cerr != nil {
				_ = os.Remove(path)
				return nil, cerr
			}
			return &fileLock{path: path}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		pid, perr := readPidFile(path)
		if perr == nil && pid > 0 && isAlive(pid) {
			return nil, fmt.Errorf("%w (pid %d)", ErrAlreadyRunning, pid)
		}
		// Stale lock (dead PID or unreadable) — remove and retry once.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return nil, rmErr
		}
	}
	return nil, ErrAlreadyRunning
}

// release removes the lock file. Safe to call once.
func (l *fileLock) release() error {
	if l == nil || l.path == "" {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// readPidFile reads and parses the PID recorded in a lock file.
func readPidFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
