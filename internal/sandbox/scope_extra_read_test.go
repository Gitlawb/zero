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
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to place a non-temp grant under")
	}
	granted, err := os.MkdirTemp(home, "zero-scope-ro-")
	if err != nil {
		t.Fatalf("mkdir under home: %v", err)
	}
	defer os.RemoveAll(granted)

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
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to place a non-temp grant under")
	}
	readGrant, err := os.MkdirTemp(home, "zero-scope-read-")
	if err != nil {
		t.Fatalf("mkdir under home: %v", err)
	}
	defer os.RemoveAll(readGrant)

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
