package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLockSingleInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	alive := func(int) bool { return true }

	l1, err := acquireLock(path, alive)
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	// Second acquire while the holder is "alive" must be refused.
	if _, err := acquireLock(path, alive); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second acquireLock err = %v, want ErrAlreadyRunning", err)
	}
	// After release, a new acquire succeeds.
	if err := l1.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	l2, err := acquireLock(path, alive)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = l2.release()
}

func TestLockStaleRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	// Simulate a stale lock from a crashed daemon: a PID file whose process is
	// dead.
	if err := os.WriteFile(path, []byte("4242\n"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
	dead := func(int) bool { return false }
	l, err := acquireLock(path, dead)
	if err != nil {
		t.Fatalf("stale-lock recovery failed: %v", err)
	}
	// The lock now records OUR pid, not the stale one.
	data, _ := os.ReadFile(path)
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("reclaimed lock pid = %q, want %d", strings.TrimSpace(string(data)), os.Getpid())
	}
	_ = l.release()
}

func TestLockReleaseRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	l, err := acquireLock(path, func(int) bool { return true })
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	if err := l.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock file still present after release: %v", err)
	}
}

func TestReclaimStaleLockRemovesDeadHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	if err := os.WriteFile(path, []byte("4242\n"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
	if !reclaimStaleLock(path, func(int) bool { return false }) {
		t.Fatal("reclaimStaleLock must report a genuinely stale (dead-PID) lock reclaimed")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a reclaimed stale lock must be removed, stat = %v", err)
	}
}

func TestReclaimStaleLockRestoresLiveHolder(t *testing.T) {
	// If a holder reacquires the lock in the gap between the stale check and the
	// rename, the moved file carries a LIVE pid; reclaim must restore it untouched
	// rather than steal it — otherwise two daemons both "hold" the lock (D6).
	path := filepath.Join(t.TempDir(), "daemon.lock")
	if err := os.WriteFile(path, []byte("4242\n"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if reclaimStaleLock(path, func(int) bool { return true }) {
		t.Fatal("reclaimStaleLock must NOT report a live-PID lock reclaimed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("live lock must be restored in place, read = %v", err)
	}
	if strings.TrimSpace(string(data)) != "4242" {
		t.Fatalf("restored lock content = %q, want %q (unchanged)", strings.TrimSpace(string(data)), "4242")
	}
	// No sidelined ".stale" leftovers.
	matches, _ := filepath.Glob(path + ".stale.*")
	if len(matches) != 0 {
		t.Fatalf("reclaim left sidelined files: %v", matches)
	}
}

func TestProcessAliveSelfAndDead(t *testing.T) {
	if !osProcessAlive(os.Getpid()) {
		t.Fatal("osProcessAlive(self) = false, want true")
	}
	// PID 0 / negative are never live.
	if osProcessAlive(0) || osProcessAlive(-1) {
		t.Fatal("osProcessAlive must reject non-positive pids")
	}
}
