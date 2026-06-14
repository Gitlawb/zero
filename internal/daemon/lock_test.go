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

func TestProcessAliveSelfAndDead(t *testing.T) {
	if !osProcessAlive(os.Getpid()) {
		t.Fatal("osProcessAlive(self) = false, want true")
	}
	// PID 0 / negative are never live.
	if osProcessAlive(0) || osProcessAlive(-1) {
		t.Fatal("osProcessAlive must reject non-positive pids")
	}
}
