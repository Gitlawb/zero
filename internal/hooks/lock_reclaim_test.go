package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReclaimStaleLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "audit.lock")

	// A genuinely stale lock (old mtime) is reclaimed and removed.
	if err := os.WriteFile(lockPath, []byte("crashed-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * auditLockStaleAfter)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	if ok, err := reclaimStaleLock(lockPath, "tok-a", auditLockStaleAfter); err != nil || !ok {
		t.Fatalf("a stale lock should be reclaimed (ok=%v err=%v)", ok, err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("reclaimed stale lock should be gone, stat err=%v", err)
	}

	// A FRESH lock (someone reacquired in the gap) must be RESTORED intact, not
	// stolen: this is the mutual-exclusion protection.
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := reclaimStaleLock(lockPath, "tok-b", auditLockStaleAfter); err != nil || ok {
		t.Fatalf("a fresh lock must not be reclaimed (ok=%v err=%v)", ok, err)
	}
	if data, err := os.ReadFile(lockPath); err != nil || string(data) != "live-holder" {
		t.Fatalf("fresh lock must be left intact, got %q err %v", data, err)
	}

	// A missing lock reports no reclaim (nothing to steal).
	_ = os.Remove(lockPath)
	if ok, err := reclaimStaleLock(lockPath, "tok-c", auditLockStaleAfter); err != nil || ok {
		t.Fatalf("a missing lock should not report a reclaim (ok=%v err=%v)", ok, err)
	}
}
