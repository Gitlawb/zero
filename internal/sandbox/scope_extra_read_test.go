package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// THE ANTI-ESCALATION PROPERTY. A read grant (AddRead, where --add-read-dir lands)
// is READABLE but NOT WRITABLE. This is what makes propagating a parent's
// request_permissions read grant to a child safe: the child can audit the granted
// path but cannot modify it. Emitting the grant as a WRITE root (--add-dir) would
// break exactly this.
func TestAReadGrantIsReadableButNotWritable(t *testing.T) {
	granted := homeGrantOutsideTempRoots(t, "zero-scope-ro-")

	scope, err := NewScope(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	root, err := scope.AddRead(granted)
	if err != nil {
		t.Fatalf("AddRead: %v", err)
	}
	target := filepath.Join(root, "audit-me.go")

	if block := scope.validateRead(target); block != nil {
		t.Fatalf("a read grant did not allow READING %q: %v — the audit would fail 'outside the workspace'", target, block)
	}
	if block := scope.validate(target); block == nil {
		t.Fatalf("a read grant ALLOWED WRITING %q — a read grant escalated to write", target)
	}
}

// ExtraReadRoots carries a read grant that ExtraRoots() omits, and never the
// workspace root. This is the bug: a request_permissions READ grant lands in
// readRoots, which ExtraRoots() (write grants only) does not return — so a child
// handed only ExtraRoots() cannot read a path the parent was granted.
//
// The grant is created OUTSIDE the default temp roots (/tmp, $TMPDIR), which
// NewScope seeds as write roots: a t.TempDir() grant would collapse into them and
// prove nothing.
func TestExtraReadRootsCarriesAReadGrantThatExtraRootsOmits(t *testing.T) {
	readGrant := homeGrantOutsideTempRoots(t, "zero-scope-read-")

	scope, err := NewScope(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	readRoot, err := scope.AddRead(readGrant) // request_permissions read grant -> readRoots
	if err != nil {
		t.Fatalf("AddRead: %v", err)
	}

	if slices.Contains(scope.ExtraRoots(), readRoot) {
		t.Fatalf("ExtraRoots carried the read grant %q — then there would be no bug to fix", readRoot)
	}
	if !slices.Contains(scope.ExtraReadRoots(), readRoot) {
		t.Fatalf("ExtraReadRoots omitted the read grant %q: %v — a read-only child cannot audit a granted path",
			readRoot, scope.ExtraReadRoots())
	}
	if slices.Contains(scope.ExtraReadRoots(), scope.WorkspaceRoot()) {
		t.Fatalf("ExtraReadRoots included the workspace root %q — a worktree child would re-open the parent tree",
			scope.WorkspaceRoot())
	}
}

// homeGrantOutsideTempRoots builds the fixture both tests in this file need: a
// directory that AddRead can grant and that is NOT already a write root.
//
// THE ASSUMPTION WAS NEVER CHECKED. Both tests place the grant under $HOME
// precisely because NewScope seeds /tmp and $TMPDIR as write roots, and a grant
// that collapsed into one of those would be writable for a reason that has
// nothing to do with the property under test — the anti-escalation assertion
// would pass, or fail, on the fixture rather than on the code. $HOME is normally
// well clear of them, but it is not guaranteed to be: a sandboxed or CI
// environment that points HOME at a temp directory turns both tests into
// something that proves nothing while still reporting PASS.
//
// Resolved before comparing, because the temp roots are themselves resolved and
// on macOS /var is a symlink to /private/var — comparing the unresolved path
// against a resolved root reports "outside" for a directory that is inside.
func homeGrantOutsideTempRoots(t *testing.T, prefix string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to place a non-temp grant under")
	}
	granted, err := os.MkdirTemp(home, prefix)
	if err != nil {
		t.Fatalf("mkdir under home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(granted) })

	resolved, err := filepath.EvalSymlinks(granted)
	if err != nil {
		t.Fatalf("resolve %q: %v", granted, err)
	}
	for _, root := range defaultTempWriteRoots() {
		if pathWithinRoot(root, resolved) {
			t.Skipf("home directory %q lies under the default temp write root %q, so a grant there is already writable and proves nothing",
				home, root)
		}
	}
	return granted
}
