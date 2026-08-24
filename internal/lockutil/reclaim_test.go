package lockutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

// Restoring a live lock uses no-replace semantics (RestoreLockFile).
// If a new claimant created lockPath in the gap between rename-aside and restore,
// the restore must NOT overwrite the new claimant's lock file; instead the restore
// fails with os.ErrExist, the new claimant's lock is preserved intact, and ReclaimStaleLock
// cleans up the sidelined file and reports a lost race (ok=false, err=nil).
func TestRestoreLiveLockPreservesCompetingHolder(t *testing.T) {
	dir := t.TempDir()
	reclaimed := filepath.Join(dir, "lock.stale.tok")
	path := filepath.Join(dir, "lock")

	if err := os.WriteFile(reclaimed, []byte("original-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new-claimant"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RestoreLockFile(reclaimed, path)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("RestoreLockFile on existing destination = %v, want os.ErrExist", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new-claimant" {
		t.Fatalf("path = %q, err %v; want the new claimant's content preserved intact", data, err)
	}
	if _, err := os.Stat(reclaimed); err != nil {
		t.Fatalf("expected reclaimed file to still exist after failed restore: %v", err)
	}
}

func TestReclaimStaleLockFailsClosedOnRestoreError(t *testing.T) {
	// When both the no-replace restore and its copy fallback fail (only
	// provokable via the seam; a healthy filesystem cannot produce it), the
	// caller must receive an error so it fails closed instead of re-acquiring a
	// missing lock path, and the sidelined file must not leak.
	restoreLockFile = func(reclaimed, path string) error { return errors.New("restore failed") }
	defer func() { restoreLockFile = RestoreLockFile }()

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
	defer func() { restoreLockFile = RestoreLockFile }()

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

func TestReclaimStaleLockConcurrentDeadReclaim(t *testing.T) {
	const goroutines = 24
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")

	if err := os.WriteFile(lockPath, []byte("crashed-process"), 0o600); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var reclaimWins atomic.Int64
	var errCount atomic.Int64

	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			ok, err := ReclaimStaleLock(lockPath, fmt.Sprintf("tok-%d", id), func(string) bool {
				return false // dead
			})
			if err != nil {
				errCount.Add(1)
				return
			}
			if ok {
				reclaimWins.Add(1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if errs := errCount.Load(); errs != 0 {
		t.Fatalf("unexpected errors during concurrent dead reclaim: %d", errs)
	}
	if wins := reclaimWins.Load(); wins != 1 {
		t.Fatalf("expected exactly 1 winner for dead lock reclaim, got %d", wins)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock file to be removed, stat = %v", err)
	}
	if matches, _ := filepath.Glob(lockPath + ".stale.*"); len(matches) != 0 {
		t.Fatalf("leaked sidelined files: %v", matches)
	}
}

func TestReclaimStaleLockConcurrentLiveRestoration(t *testing.T) {
	const goroutines = 24
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")

	if err := os.WriteFile(lockPath, []byte("live-holder-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var reclaimWins atomic.Int64
	var errCount atomic.Int64

	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			ok, err := ReclaimStaleLock(lockPath, fmt.Sprintf("tok-%d", id), func(string) bool {
				return true // live
			})
			if err != nil {
				errCount.Add(1)
				return
			}
			if ok {
				reclaimWins.Add(1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if errs := errCount.Load(); errs != 0 {
		t.Fatalf("unexpected errors during concurrent live restore: %d", errs)
	}
	if wins := reclaimWins.Load(); wins != 0 {
		t.Fatalf("expected 0 winners for live lock reclaim, got %d", wins)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil || string(data) != "live-holder-token" {
		t.Fatalf("expected live lock file intact at %q, got %q (err %v)", lockPath, data, err)
	}
	if matches, _ := filepath.Glob(lockPath + ".stale.*"); len(matches) != 0 {
		t.Fatalf("leaked sidelined files: %v", matches)
	}
}

func TestReclaimStaleLockRaceWithNewClaimant(t *testing.T) {
	// Exercise the exact race condition:
	// A live lock is sidelined for inspection.
	// Before restore completes, an O_EXCL claimant creates lockPath.
	// The restore must NOT overwrite the new claimant.
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")

	if err := os.WriteFile(lockPath, []byte("original-live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}

	ok, err := ReclaimStaleLock(lockPath, "race-tok", func(reclaimedPath string) bool {
		// Inside isLive callback (file is currently sidelined to reclaimedPath):
		// An unrelated process creates a new lock at lockPath via O_EXCL.
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.Errorf("new claimant OpenFile failed: %v", err)
			return false
		}
		if _, err := f.WriteString("new-concurrent-claimant"); err != nil {
			t.Errorf("new claimant WriteString failed: %v", err)
		}
		_ = f.Close()
		return true // original holder was also live
	})

	if err != nil {
		t.Fatalf("ReclaimStaleLock should handle ErrExist cleanly, got err: %v", err)
	}
	if ok {
		t.Fatalf("ReclaimStaleLock should return ok=false when live holder lost restore race, got ok=true")
	}

	// Verify the new claimant's file was NOT overwritten
	data, err := os.ReadFile(lockPath)
	if err != nil || string(data) != "new-concurrent-claimant" {
		t.Fatalf("lock file was overwritten or corrupted: got %q (err %v), want %q", data, err, "new-concurrent-claimant")
	}

	// Verify the sidelined file was cleaned up and not leaked
	if matches, _ := filepath.Glob(lockPath + ".stale.*"); len(matches) != 0 {
		t.Fatalf("sidelined file leaked after race: %v", matches)
	}
}
