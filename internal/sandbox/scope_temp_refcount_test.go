package sandbox

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// scopeOutsideDefaults builds a Scope directly instead of through NewScope.
//
// NewScope adds the system temp directory as a write root, so a target under
// t.TempDir() is already covered and AddTemporaryRead returns before it ever
// touches the refcount. A test built that way exercises none of this and passes
// against the bug — which is how the first version of this test passed.
func scopeOutsideDefaults(t *testing.T) (*Scope, string) {
	t.Helper()
	workspace := t.TempDir()
	target := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	// Built directly, NOT via NewScope: real directories are needed because
	// normalizeScopeRoot requires them to exist, but NewScope's default temp
	// write root would then cover the target and short-circuit the path.
	return &Scope{workspaceRoot: workspace}, target
}

// The refcount and the root list have to move together.
//
// Release used to delete the refcount, drop the lock, then strip the root in a
// second acquisition. In that window the root was still in readRoots but no
// longer in tempReads, so a concurrent AddTemporaryRead read it as a PERMANENT
// root and handed its caller a no-op undo — then the release stripped it and
// that caller silently lost access it believed it held.
func TestReleasingATemporaryReadCannotStripALiveGrant(t *testing.T) {
	scope, target := scopeOutsideDefaults(t)

	// First holder establishes the root.
	first, releaseFirst, err := scope.AddTemporaryRead(target)
	if err != nil {
		t.Fatalf("AddTemporaryRead: %v", err)
	}
	if len(scope.tempReads) == 0 {
		t.Fatal("precondition: the grant should be refcounted, not covered by a default root")
	}

	// Second holder takes a reference on the same root.
	second, releaseSecond, err := scope.AddTemporaryRead(target)
	if err != nil {
		t.Fatalf("AddTemporaryRead (second): %v", err)
	}

	// The first holder leaving must not revoke the second's access.
	releaseFirst()
	if block := scope.validateRead(second); block != nil {
		t.Fatalf("the second holder lost its grant when the first released: %v", block)
	}

	// Only the last release retires the root.
	releaseSecond()
	if block := scope.validateRead(first); block == nil {
		t.Error("the root outlived its last holder")
	}
}

// Under contention the same invariant has to hold: while a grant is live, its
// root is readable.
func TestTemporaryReadGrantsSurviveConcurrentReleases(t *testing.T) {
	scope, target := scopeOutsideDefaults(t)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string

	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				granted, undo, err := scope.AddTemporaryRead(target)
				if err != nil {
					mu.Lock()
					failures = append(failures, "AddTemporaryRead: "+err.Error())
					mu.Unlock()
					return
				}
				if block := scope.validateRead(granted); block != nil {
					mu.Lock()
					failures = append(failures, "root not readable while a grant was live")
					mu.Unlock()
				}
				undo()
			}
		}()
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("%d failures, first: %s", len(failures), failures[0])
	}
}

// A read covered by a TEMPORARY WRITE root must take a reference too.
//
// writeRootCoversLocked scans extraRoots, and AddTemporaryWrite appends
// temporary write roots there — so "covered by a write root" does not mean
// "covered permanently". Without a reference the write holder's cleanup silently
// revoked the reader's access: the same defect the refcount closes, reached
// across the read/write boundary instead of within one side of it.
func TestAReadCoveredByATemporaryWriteSurvivesItsRelease(t *testing.T) {
	workspace := t.TempDir()
	outer := filepath.Join(t.TempDir(), "outer")
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	// Built directly for the same reason scopeOutsideDefaults is: NewScope's
	// default temp write root would cover the fixture and short-circuit this.
	scope := &Scope{workspaceRoot: workspace}

	_, releaseWrite, err := scope.AddTemporaryWrite(outer)
	if err != nil {
		t.Fatalf("temporary write: %v", err)
	}
	readRoot, releaseRead, err := scope.AddTemporaryRead(inner)
	if err != nil {
		t.Fatalf("temporary read: %v", err)
	}

	covered := func() bool {
		for _, root := range scope.ReadRoots() {
			if pathWithinRoot(root, readRoot) {
				return true
			}
		}
		return false
	}
	if !covered() {
		t.Fatal("the reader did not hold access before any release")
	}

	// The WRITE holder finishes first; the reader has not released.
	releaseWrite()
	if !covered() {
		t.Errorf("the write holder's cleanup revoked a live read grant on %s", readRoot)
	}

	// And once the reader releases too, the root is genuinely gone.
	releaseRead()
	if covered() {
		t.Errorf("the root outlived its last holder: %v", scope.ReadRoots())
	}
}

func hasExtraRoot(scope *Scope, root string) bool {
	for _, existing := range scope.ExtraRoots() {
		if existing == root {
			return true
		}
	}
	return false
}

// A PERMANENT GRANT MUST OUTLIVE THE TEMPORARY ONE IT LANDED ON TOP OF.
//
// extraRoots holds temporary write roots alongside permanent ones, so Add asked
// only "is this already covered" — and a session-scoped grant made while a
// temporary holder happened to cover the path recorded nothing, then vanished
// when that holder released. Outliving the request that prompted it is the
// entire difference between Add and AddTemporaryWrite.
func TestAPermanentGrantSurvivesTheTemporaryOneItCovered(t *testing.T) {
	t.Run("write, same root", func(t *testing.T) {
		workspace, outside := scopeOutsideRoots(t)
		scope, err := NewScope(workspace, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, releaseTemp, err := scope.AddTemporaryWrite(outside)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := scope.Add(outside); err != nil {
			t.Fatal(err)
		}
		releaseTemp()
		if !hasExtraRoot(scope, outside) {
			t.Error("the temporary holder's release revoked a permanent write grant")
		}
	})

	t.Run("read, same root", func(t *testing.T) {
		workspace, outside := scopeOutsideRoots(t)
		scope, err := NewScope(workspace, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, releaseTemp, err := scope.AddTemporaryRead(outside)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := scope.AddRead(outside); err != nil {
			t.Fatal(err)
		}
		releaseTemp()
		if !hasReadRoot(scope, outside) {
			t.Error("the temporary holder's release revoked a permanent read grant")
		}
	})

	// The nested shape: a BROAD temporary root covering a NARROW permanent one.
	// Promoting in place cannot help here — the narrow root has to be recorded
	// in its own right, or it goes when the broad one does.
	t.Run("broad temporary over narrow permanent", func(t *testing.T) {
		workspace, outside := scopeOutsideRoots(t)
		inner := filepath.Join(outside, "inner")
		if err := os.MkdirAll(inner, 0o700); err != nil {
			t.Fatal(err)
		}
		scope, err := NewScope(workspace, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, releaseBroad, err := scope.AddTemporaryWrite(outside)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := scope.Add(inner); err != nil {
			t.Fatal(err)
		}
		releaseBroad()
		covered := false
		for _, existing := range scope.ExtraRoots() {
			if pathWithinRoot(existing, inner) {
				covered = true
			}
		}
		if !covered {
			t.Error("a narrower permanent grant died with the broader temporary root it sat under")
		}
	})
}

// ONE HOLDER'S UNDO DROPS ONE HOLDER'S REFERENCE, HOWEVER OFTEN IT IS CALLED.
//
// The count flooring at zero stops it going negative; it does not stop a
// holder's second call consuming somebody else's reference. With two readers,
// calling the first's undo twice took the count 2 -> 1 -> 0 and removed a root
// the second was still using — the exact revocation this refcount exists to
// prevent, reached through a duplicate call rather than a sibling's cleanup.
// Deferred cleanups in a retry path are how a real caller does this by accident.
func TestOneHoldersUndoIsIdempotent(t *testing.T) {
	workspace, outside := scopeOutsideRoots(t)
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, undoFirst, err := scope.AddTemporaryRead(outside)
	if err != nil {
		t.Fatal(err)
	}
	_, undoSecond, err := scope.AddTemporaryRead(outside)
	if err != nil {
		t.Fatal(err)
	}
	undoFirst()
	undoFirst()
	undoFirst()
	if !hasReadRoot(scope, outside) {
		t.Fatal("repeated calls to one holder's undo revoked a live holder's access")
	}
	undoSecond()
	if hasReadRoot(scope, outside) {
		t.Error("the root outlived its last holder")
	}

	// The write side takes the same rule.
	writeScope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, undoWriteFirst, err := writeScope.AddTemporaryWrite(outside)
	if err != nil {
		t.Fatal(err)
	}
	_, undoWriteSecond, err := writeScope.AddTemporaryWrite(outside)
	if err != nil {
		t.Fatal(err)
	}
	undoWriteFirst()
	undoWriteFirst()
	if !hasExtraRoot(writeScope, outside) {
		t.Fatal("repeated calls to one write holder's undo revoked a live holder's access")
	}
	undoWriteSecond()
	if hasExtraRoot(writeScope, outside) {
		t.Error("the write root outlived its last holder")
	}
}
