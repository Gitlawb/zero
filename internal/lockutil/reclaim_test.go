package lockutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReclaimStaleLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "lock")
	dead := func(string) bool { return false }
	live := func(string) bool { return true }

	// A lock the predicate reports dead is reclaimed and removed.
	if err := os.WriteFile(lockPath, []byte("crashed-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := ReclaimStaleLock(lockPath, "tok-a", dead); err != nil || !ok {
		t.Fatalf("a dead lock should be reclaimed (ok=%v err=%v)", ok, err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reclaimed dead lock should be gone, stat err=%v", err)
	}

	// A LIVE lock (a holder reacquired in the gap) must be RESTORED intact.
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := ReclaimStaleLock(lockPath, "tok-b", live); err != nil || ok {
		t.Fatalf("a live lock must not be reclaimed (ok=%v err=%v)", ok, err)
	}
	if data, err := os.ReadFile(lockPath); err != nil || string(data) != "live-holder" {
		t.Fatalf("live lock must be left intact, got %q err %v", data, err)
	}

	// A missing lock reports no reclaim (nothing to steal).
	_ = os.Remove(lockPath)
	if ok, err := ReclaimStaleLock(lockPath, "tok-c", live); err != nil || ok {
		t.Fatalf("a missing lock should not report a reclaim (ok=%v err=%v)", ok, err)
	}
}

// restoreLiveLock's fast path is a single replacing rename, chosen to
// minimize (not eliminate) the window during which lockPath is absent and
// open to an unrelated O_EXCL create; see ReclaimStaleLock's doc comment for
// why that residual race exists. This test pins the resulting behavior: the
// fast path overwrites a competing file at path rather than detecting it,
// unlike RestoreLockFile's own no-replace contract.
func TestRestoreLiveLockFastPathOverwritesCompetingFile(t *testing.T) {
	dir := t.TempDir()
	reclaimed := filepath.Join(dir, "lock.stale.tok")
	path := filepath.Join(dir, "lock")

	if err := os.WriteFile(reclaimed, []byte("original-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new-claimant"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreLiveLock(reclaimed, path); err != nil {
		t.Fatalf("restoreLiveLock failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original-holder" {
		t.Fatalf("path = %q, err %v; want the original holder's content restored", data, err)
	}
	if _, err := os.Stat(reclaimed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected reclaimed to be consumed by the rename: %v", err)
	}
}

func TestRestoreRootedLockPreservesCompetingFile(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("root.Close: %v", err)
		}
	})

	if err := os.WriteFile(filepath.Join(dir, "lock.stale.tok"), []byte("original-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lock"), []byte("new-claimant"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = restoreRootedLock(root, "lock.stale.tok", "lock")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("restoreRootedLock error = %v, want os.ErrExist", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "lock"))
	if err != nil || string(data) != "new-claimant" {
		t.Fatalf("competing lock = %q, err %v; want unchanged content", data, err)
	}
}
func TestReclaimStaleLockRooted(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("root.Close: %v", err)
		}
	})
	lockName := "lock"
	dead := func([]byte) bool { return false }
	live := func([]byte) bool { return true }

	// A lock the predicate reports dead is reclaimed and removed.
	if err := os.WriteFile(filepath.Join(dir, lockName), []byte("crashed-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := ReclaimStaleLockRooted(root, lockName, "tok-a", dead); err != nil || !ok {
		t.Fatalf("a dead lock should be reclaimed (ok=%v err=%v)", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, lockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reclaimed dead lock should be gone, stat err=%v", err)
	}

	// A LIVE lock must be RESTORED intact.
	if err := os.WriteFile(filepath.Join(dir, lockName), []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := ReclaimStaleLockRooted(root, lockName, "tok-b", live); err != nil || ok {
		t.Fatalf("a live lock must not be reclaimed (ok=%v err=%v)", ok, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, lockName)); err != nil || string(data) != "live-holder" {
		t.Fatalf("live lock must be left intact, got %q err %v", data, err)
	}

	// A missing lock reports no reclaim (nothing to steal).
	if ok, err := ReclaimStaleLockRooted(root, lockName, "tok-c", live); err != nil || ok {
		t.Fatalf("a missing lock should not report a reclaim (ok=%v err=%v)", ok, err)
	}
}

// TestReclaimStaleLockRootedRestoresUnreadableLock pins that a lock whose
// contents cannot be read is put back rather than deleted. The predicate here
// has the same shape as kimiidentity's lockHolderAlive, which deliberately
// treats empty or unparseable contents as dead so a holder that crashed
// mid-write stays recoverable. Passing it nil for a lock that merely could not
// be read therefore classified a possibly-live lock as dead and removed it, so
// the decision has to be made before the predicate is consulted.
func TestReclaimStaleLockRootedRestoresUnreadableLock(t *testing.T) {
	original := readSidelinedLock
	readSidelinedLock = func(*os.Root, string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	defer func() { readSidelinedLock = original }()

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("root.Close: %v", err)
		}
	})

	lockName := "lock"
	lockPath := filepath.Join(dir, lockName)
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Empty or unreadable contents look dead to this predicate, exactly like
	// the real caller's.
	deadWhenBlank := func(raw []byte) bool { return len(raw) > 0 }

	ok, err := ReclaimStaleLockRooted(root, lockName, "tok-unreadable", deadWhenBlank)
	if err != nil {
		t.Fatalf("an unreadable lock should be restored, not error: %v", err)
	}
	if ok {
		t.Fatal("an unreadable lock must not be reported as reclaimed: a read failure is not proof the holder is dead")
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("the unreadable lock must be restored at its original name: %v", err)
	}
	if string(data) != "live-holder" {
		t.Fatalf("restored lock contents = %q, want %q", data, "live-holder")
	}
	// The sidelined copy must not be left behind.
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("expected only the restored lock in the directory, got %d entries", len(entries))
	}
}

func TestReclaimStaleLockRootedFallbackWhenLinkFails(t *testing.T) {
	origLink := linkRootedLock
	linkRootedLock = func(*os.Root, string, string) error {
		return errors.New("function not implemented")
	}
	defer func() { linkRootedLock = origLink }()

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	lockName := "lock"
	lockPath := filepath.Join(dir, lockName)
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}

	ok, err := ReclaimStaleLockRooted(root, lockName, "tok-fallback", func(raw []byte) bool { return true })
	if err != nil {
		t.Fatalf("unexpected error during restore fallback: %v", err)
	}
	if ok {
		t.Fatal("expected live lock not to be reclaimed")
	}
	data, err := os.ReadFile(lockPath)
	if err != nil || string(data) != "live-holder" {
		t.Fatalf("restored lock contents = %q (err=%v), want %q", data, err, "live-holder")
	}
}

func TestReclaimStaleLockRootedFallbackWhenLinkAndReadFail(t *testing.T) {
	origLink := linkRootedLock
	linkRootedLock = func(*os.Root, string, string) error {
		return errors.New("function not implemented")
	}
	defer func() { linkRootedLock = origLink }()

	origRead := readSidelinedLock
	readSidelinedLock = func(*os.Root, string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	defer func() { readSidelinedLock = origRead }()

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	lockName := "lock"
	lockPath := filepath.Join(dir, lockName)
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}

	ok, err := ReclaimStaleLockRooted(root, lockName, "tok-unreadable-linkfail", func(raw []byte) bool { return true })
	if err != nil {
		t.Fatalf("unexpected error during unreadable restore fallback: %v", err)
	}
	if ok {
		t.Fatal("expected unreadable lock not to be reclaimed")
	}
	data, err := os.ReadFile(lockPath)
	if err != nil || string(data) != "live-holder" {
		t.Fatalf("restored lock contents = %q (err=%v), want %q", data, err, "live-holder")
	}
}

func TestReclaimStaleLockFailsClosedOnRestoreError(t *testing.T) {
	// When both the no-replace restore and its copy fallback fail (only
	// provokable via the seam; a healthy filesystem cannot produce it), the
	// caller must receive an error so it fails closed instead of re-acquiring a
	// missing lock path, and the sidelined file must not leak.
	restoreLockFile = func(reclaimed, path string) error { return errors.New("restore failed") }
	defer func() { restoreLockFile = restoreLiveLock }()

	lockPath := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := ReclaimStaleLock(lockPath, "tok", func(string) bool { return true })
	if err == nil || ok {
		t.Fatalf("a failed restore must surface an error (ok=%v err=%v)", ok, err)
	}
	if matches, _ := filepath.Glob(lockPath + ".stale.*"); len(matches) != 0 {
		t.Fatalf("a failed restore must not leak sidelined files: %v", matches)
	}
}

func TestReclaimStaleLockDropsSidelinedWhenNewHolderWins(t *testing.T) {
	// An os.ErrExist restore failure means a new holder recreated the lock
	// path; that is not an error for the caller, and the sidelined file is
	// dropped rather than leaked.
	restoreLockFile = func(reclaimed, path string) error { return os.ErrExist }
	defer func() { restoreLockFile = restoreLiveLock }()

	lockPath := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := ReclaimStaleLock(lockPath, "tok", func(string) bool { return true })
	if err != nil || ok {
		t.Fatalf("losing to a new holder is not an error (ok=%v err=%v)", ok, err)
	}
	if matches, _ := filepath.Glob(lockPath + ".stale.*"); len(matches) != 0 {
		t.Fatalf("the sidelined file must be dropped when a new holder wins: %v", matches)
	}
}
