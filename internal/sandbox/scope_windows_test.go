//go:build windows

package sandbox

import (
	"path/filepath"
	"testing"
)

// On Windows a workspace created under an 8.3 short path is resolved by
// EvalSymlinks to its long form, so NormalizePrefixForRoot must resolve a raw
// request's prefix to match — the same alias handling it does for macOS
// /var -> /private/var. If the volume root were mishandled (drive path
// mangled to a drive-relative form), the engine gate would fail OPEN or DENY
// legitimate extra-root writes. This pins both directions on Windows. Caught
// on PR #162's windows smoke run.
func TestScopeValidateHandlesVolumePathsOnWindows(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	// A path inside a granted root must be allowed even when the workspace/
	// extra dirs were created under 8.3 short names and the scope stored their
	// long forms.
	if violation := scope.validate(filepath.Join(extra, "ok.txt")); violation != nil {
		t.Fatalf("validate(extra-root path) = %v, want nil", violation)
	}
	if violation := scope.validate(filepath.Join(workspace, "in.txt")); violation != nil {
		t.Fatalf("validate(workspace path) = %v, want nil", violation)
	}

	// An absolute path outside all roots must still be denied (no fail-open
	// from drive-path mangling).
	outside := filepath.Join(t.TempDir(), "escape.txt")
	violation := scope.validate(outside)
	if violation == nil {
		t.Fatal("validate(outside all roots) = nil, want violation (fail-open regression)")
	}
	if violation.Code != ViolationOutsideWorkspace {
		t.Fatalf("violation.Code=%q want %q", violation.Code, ViolationOutsideWorkspace)
	}
}
