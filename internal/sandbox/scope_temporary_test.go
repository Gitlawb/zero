package sandbox

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// scopeOutsideRoots builds a workspace and an unrelated directory that no
// default write root already covers.
//
// NOT t.TempDir(). /tmp and $TMPDIR are default write roots, so a temporary
// grant for a path under one is a no-op — every assertion here would pass
// against code that grants nothing at all. That is RULES §2.3 in its exact
// form, and it caught the first version of this test.
func scopeOutsideRoots(t *testing.T) (workspace string, outside string) {
	t.Helper()
	base, err := os.MkdirTemp("/Users/Shared", "zeromax-scope-")
	if err != nil {
		base, err = os.MkdirTemp("/var/empty", "zeromax-scope-")
		if err != nil {
			t.Skipf("no directory outside a default write root is available: %v", err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	workspace = filepath.Join(base, "ws")
	outside = filepath.Join(base, "outside")
	for _, dir := range []string{workspace, outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Skipf("cannot prepare %s: %v", dir, err)
		}
	}
	return workspace, outside
}

func hasReadRoot(scope *Scope, root string) bool {
	for _, existing := range scope.ReadRoots() {
		if existing == root {
			return true
		}
	}
	return false
}

// THE DEFECT: one holder's cleanup revoked another's access. Two read-only
// tools in the same parallel batch, both blocked on the same directory, is
// exactly this — and read-only tools are the ones the batch runs concurrently.
func TestATemporaryReadSurvivesASiblingsCleanup(t *testing.T) {
	workspace, outside := scopeOutsideRoots(t)
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The probe that the fix must make unnecessary: without it this reads
	// true -> false.
	if hasReadRoot(scope, outside) {
		t.Fatal("the outside root is already covered; this test proves nothing")
	}

	_, undoA, err := scope.AddTemporaryRead(outside)
	if err != nil {
		t.Fatal(err)
	}
	_, undoB, err := scope.AddTemporaryRead(outside)
	if err != nil {
		t.Fatal(err)
	}
	if !hasReadRoot(scope, outside) {
		t.Fatal("the grant did not take effect")
	}

	undoA()
	if !hasReadRoot(scope, outside) {
		t.Fatal("A's cleanup revoked the root while B still held a grant")
	}
	undoB()
	if hasReadRoot(scope, outside) {
		t.Fatal("the root outlived its last holder")
	}
}

// A BROADER temporary grant covering a narrower request is the same defect one
// level up: the narrower caller gets no root of its own, so it must hold a
// reference on whatever covers it.
func TestANarrowerRequestHoldsTheCoveringGrant(t *testing.T) {
	workspace, outside := scopeOutsideRoots(t)
	nested := filepath.Join(outside, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Skip(err)
	}
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, undoBroad, err := scope.AddTemporaryRead(outside)
	if err != nil {
		t.Fatal(err)
	}
	_, undoNarrow, err := scope.AddTemporaryRead(nested)
	if err != nil {
		t.Fatal(err)
	}
	undoBroad()
	if !hasReadRoot(scope, outside) {
		t.Fatal("the broad holder's cleanup revoked coverage the narrow holder still needs")
	}
	undoNarrow()
	if hasReadRoot(scope, outside) {
		t.Fatal("the root outlived its last holder")
	}
}

// A PERMANENT root is not refcounted and must never be removed by a temporary
// holder's cleanup — the undo for a request it already covers is genuinely
// nothing.
func TestATemporaryGrantNeverRevokesAPermanentRoot(t *testing.T) {
	workspace, outside := scopeOutsideRoots(t)
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.AddRead(outside); err != nil {
		t.Fatal(err)
	}
	_, undo, err := scope.AddTemporaryRead(outside)
	if err != nil {
		t.Fatal(err)
	}
	undo()
	if !hasReadRoot(scope, outside) {
		t.Fatal("a temporary holder's cleanup removed a permanent read root")
	}
}

// The same rule for WRITE roots, or a write grant keeps the bug reads no longer
// have.
func TestATemporaryWriteSurvivesASiblingsCleanup(t *testing.T) {
	workspace, outside := scopeOutsideRoots(t)
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	covered := func() bool {
		for _, existing := range scope.Roots() {
			if existing == outside {
				return true
			}
		}
		return false
	}
	_, undoA, err := scope.AddTemporaryWrite(outside)
	if err != nil {
		t.Fatal(err)
	}
	_, undoB, err := scope.AddTemporaryWrite(outside)
	if err != nil {
		t.Fatal(err)
	}
	if !covered() {
		t.Fatal("the write grant did not take effect")
	}
	undoA()
	if !covered() {
		t.Fatal("A's cleanup revoked the write root while B still held a grant")
	}
	undoB()
	if covered() {
		t.Fatal("the write root outlived its last holder")
	}
}

// UNDER REAL CONCURRENCY, which is the shape the parallel tool batch produces:
// eight holders taking and releasing the same root in overlapping windows. The
// root must be present the whole time at least one holder has it, and gone once
// the last releases.
func TestConcurrentHoldersOfOneRoot(t *testing.T) {
	workspace, outside := scopeOutsideRoots(t)
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}

	const holders = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	release := make(chan struct{})
	failures := make(chan string, holders)
	for i := 0; i < holders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, undo, err := scope.AddTemporaryRead(outside)
			if err != nil {
				failures <- err.Error()
				return
			}
			// Every holder must SEE its own grant for as long as it holds it.
			if !hasReadRoot(scope, outside) {
				failures <- "a holder could not see the root it had just been granted"
			}
			<-release
			if !hasReadRoot(scope, outside) {
				failures <- "a holder lost the root while still holding it"
			}
			undo()
		}()
	}
	close(start)
	// Let every holder acquire before any releases, so the windows overlap.
	for {
		if !hasReadRoot(scope, outside) {
			continue
		}
		break
	}
	close(release)
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	if hasReadRoot(scope, outside) {
		t.Fatal("the root outlived every holder")
	}
}
