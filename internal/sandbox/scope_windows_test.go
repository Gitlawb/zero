//go:build windows

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// Volume-qualified paths must pass through NormalizePrefixForRoot verbatim:
// its POSIX component walk cannot represent a volume root like `C:\` and
// would mangle the path into a drive-relative form (`C:Users\...`) that the
// single-root checks treat as relative — flipping the policy gate fail-open.
// This pins the Windows smoke regression caught on PR #162.
func TestNormalizePrefixForRootReturnsVolumePathsVerbatim(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sub", "file.txt")
	if got := NormalizePrefixForRoot(target, root); got != target {
		t.Fatalf("NormalizePrefixForRoot(%q) = %q, want verbatim", target, got)
	}
	outside := filepath.Join(os.TempDir(), "elsewhere", "x.txt")
	if got := NormalizePrefixForRoot(outside, root); got != outside {
		t.Fatalf("NormalizePrefixForRoot(%q) = %q, want verbatim", outside, got)
	}
}

// The engine-level guarantee the mangling broke: an absolute path outside all
// scope roots must be denied on Windows exactly as on POSIX.
func TestScopeValidateDeniesOutsidePathsOnWindows(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	if violation := scope.validate(filepath.Join(extra, "ok.txt")); violation != nil {
		t.Fatalf("validate(extra-root path) = %v, want nil", violation)
	}
	outside := filepath.Join(t.TempDir(), "escape.txt")
	violation := scope.validate(outside)
	if violation == nil {
		t.Fatal("validate(outside all roots) = nil, want violation (fail-open regression)")
	}
	if violation.Code != ViolationOutsideWorkspace {
		t.Fatalf("violation.Code=%q want %q", violation.Code, ViolationOutsideWorkspace)
	}
}
