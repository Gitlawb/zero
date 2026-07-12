//go:build windows

package lockutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReclaimStaleLockTreatsSharingViolationAsLostRace pins the errno
// classification the Windows acquire loops depend on: a rename aside that
// fails because the lock file is concurrently open (Go opens files without
// FILE_SHARE_DELETE, so the rename fails with ERROR_SHARING_VIOLATION or
// ERROR_ACCESS_DENIED) is contention, not a hard failure, and must report a
// benign lost race so the caller keeps waiting.
func TestReclaimStaleLockTreatsSharingViolationAsLostRace(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(lockPath, []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if ok, rerr := ReclaimStaleLock(lockPath, "tok", func(string) bool { return true }); rerr != nil || ok {
		t.Fatalf("a sharing-violation rename must report a benign lost race (ok=%v err=%v)", ok, rerr)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("the lock file must be left in place: %v", err)
	}
}

// TestReclaimStaleLockPropagatesRenameFailure pins the fail-fast contract of
// the rename aside on Windows: a failure that is not contention must reach
// the caller instead of masquerading as a lost race. A quote is an invalid
// filename character, so the sidelined name fails with ERROR_INVALID_NAME.
func TestReclaimStaleLockPropagatesRenameFailure(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(lockPath, []byte("stale-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := ReclaimStaleLock(lockPath, `bad"name`, func(string) bool { return false }); err == nil || ok {
		t.Fatalf("a hard rename failure must surface an error (ok=%v err=%v)", ok, err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("the lock file must be left in place on a failed rename: %v", err)
	}
}
