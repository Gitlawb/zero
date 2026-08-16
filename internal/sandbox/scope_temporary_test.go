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
	// The home directory, which no platform lists as a default write root —
	// Windows takes %TEMP%/%TMP% and the rest take /tmp plus $TMPDIR. The first
	// version reached for /Users/Shared and fell back to /var/empty, both of
	// which exist only on this side of the fence: on Windows neither resolves,
	// the helper skipped, and every test built on it reported green having
	// asserted nothing. A skip is not a pass. scope_extra_read_test.go already
	// places its grant under home for the same reason.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory to place a grant outside the default write roots: %v", err)
	}
	base, err := os.MkdirTemp(home, "zeromax-scope-")
	if err != nil {
		t.Skipf("cannot create a directory outside the default write roots: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	// PROVED, not assumed. The whole point of this helper is that a temporary
	// grant here is not already covered, and asserting it against the same list
	// production consults means a future default write root turns these tests
	// into an honest skip rather than a silent no-op.
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		resolved = base
	}
	for _, root := range defaultTempWriteRoots() {
		if pathWithinRoot(root, resolved) {
			t.Skipf("%s is already covered by the default write root %s, so a temporary grant here would prove nothing", resolved, root)
		}
	}
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
	// Signalled by EVERY holder once its own AddTemporaryRead has returned,
	// failure included: the gate below waits for all of them, so a holder that
	// returned early without signalling would hang this test rather than fail it.
	acquired := make(chan struct{}, holders)
	for i := 0; i < holders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, undo, err := scope.AddTemporaryRead(outside)
			acquired <- struct{}{}
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
	// EVERY holder, not the first one. Spinning on hasReadRoot only waited for
	// somebody to hold the root, and the spin is tight enough to win that race
	// against the goroutines still being scheduled: measured over 200 runs, 194
	// of them peaked at a single simultaneous holder and none ever reached
	// eight. A test named for concurrent holders was testing one holder at a
	// time, and would have passed against an implementation with no refcount at
	// all. Counting the acquisitions makes the overlap real instead of hoped for.
	for i := 0; i < holders; i++ {
		<-acquired
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
