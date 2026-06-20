package cron

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReclaimStaleLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "job.lock")

	// A genuinely stale lock (old mtime) is reclaimed and removed.
	if err := os.WriteFile(lockPath, []byte("crashed-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * cronLockStaleAfter)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	if !reclaimStaleLock(lockPath, "tok-a", cronLockStaleAfter) {
		t.Fatal("a stale lock should be reclaimed")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("reclaimed stale lock should be gone, stat err=%v", err)
	}

	// A FRESH lock (someone reacquired in the gap) must be RESTORED intact, not
	// stolen — this is the H3 mutual-exclusion protection.
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reclaimStaleLock(lockPath, "tok-b", cronLockStaleAfter) {
		t.Fatal("a fresh lock must not be reclaimed")
	}
	if data, err := os.ReadFile(lockPath); err != nil || string(data) != "live-holder" {
		t.Fatalf("fresh lock must be left intact, got %q err %v", data, err)
	}

	// A missing lock reports no reclaim (nothing to steal).
	_ = os.Remove(lockPath)
	if reclaimStaleLock(lockPath, "tok-c", cronLockStaleAfter) {
		t.Fatal("a missing lock should not report a reclaim")
	}
}
