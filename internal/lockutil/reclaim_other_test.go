//go:build !windows

package lockutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReclaimStaleLockPropagatesRenameFailure pins the fail-fast contract of
// the rename aside: a failure that is not a lost race must reach the caller.
// Mapping it to the benign lost-race result would leave acquirers spinning
// until their deadline and reporting a timeout instead of the real cause. A
// directory squatting on the sidelined name makes os.Rename fail with EISDIR,
// which matches neither os.ErrNotExist nor a contention errno.
func TestReclaimStaleLockPropagatesRenameFailure(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(lockPath, []byte("stale-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath+".stale.tok", 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := ReclaimStaleLock(lockPath, "tok", func(string) bool { return false }); err == nil || ok {
		t.Fatalf("a hard rename failure must surface an error (ok=%v err=%v)", ok, err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("the lock file must be left in place on a failed rename: %v", err)
	}
}
