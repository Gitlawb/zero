package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
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
		if !errors.Is(err, fs.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return nil, err
		}
		pid, perr := readPidFile(path)
		if perr == nil && pid > 0 && isAlive(pid) {
			return nil, fmt.Errorf("%w (pid %d)", ErrAlreadyRunning, pid)
		}
		// Stale lock (dead PID or unreadable) — reclaim it atomically, then retry the
		// O_EXCL create. A blind Remove here races: two daemons starting at once could
		// both read the stale PID, both Remove, and then one Removes the OTHER's
		// freshly-created lock — leaving both "holding" the single-instance lock.
		// reclaimStaleLock renames the file aside so only one racer wins the rename,
		// and restores it if a live holder reacquired in the gap (D6).
		reclaimStaleLock(path, isAlive)
	}
	return nil, ErrAlreadyRunning
}

// daemonLockSeq makes each reclaim attempt's sidelined filename unique per process.
var daemonLockSeq atomic.Uint64

// reclaimStaleLock atomically reclaims a single-instance lock whose recorded PID is
// dead. A blind Remove lets two racers both delete-and-recreate the lock and wind up
// BOTH holding it; instead this renames the lock file aside (only one racer can win
// the rename of a given inode), re-reads the moved file's PID, and removes it only if
// that PID is still dead. If a new holder reacquired the lock in the gap between the
// stale check and the rename, the moved file carries that LIVE pid, so it is renamed
// back rather than stolen. Returns true only when a genuinely stale lock was removed.
func reclaimStaleLock(path string, isAlive func(pid int) bool) bool {
	reclaimed := fmt.Sprintf("%s.stale.%d-%d", path, os.Getpid(), daemonLockSeq.Add(1))
	if err := os.Rename(path, reclaimed); err != nil {
		return false // another racer already moved/removed it, or it vanished
	}
	if pid, err := readPidFile(reclaimed); err == nil && pid > 0 && isAlive(pid) {
		// A holder reacquired the lock in the gap — its live PID is now in the moved
		// file. Restore it instead of stealing a live lock; let the caller refuse.
		_ = os.Rename(reclaimed, path)
		return false
	}
	_ = os.Remove(reclaimed)
	return true
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
