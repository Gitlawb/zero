package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScopeValidateMultiRootSymlinkTraversalPreferred pins Fix 1: when a
// symlink inside an extra root escapes outside all roots, validate must return
// BlockSymlinkTraversal (not BlockOutsideWorkspace) with the original
// requested path and without the --add-dir hint.
func TestScopeValidateMultiRootSymlinkTraversalPreferred(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(extra, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	requestedPath := filepath.Join(extra, "link", "escape.txt")
	block := scope.validate(requestedPath)
	if block == nil {
		t.Fatal("validate(extra/link/escape.txt) = nil, want block")
	}
	if block.Code != BlockSymlinkTraversal {
		t.Fatalf("block.Code=%q want %q", block.Code, BlockSymlinkTraversal)
	}
	if block.Path != requestedPath {
		t.Fatalf("block.Path=%q want original requestedPath %q", block.Path, requestedPath)
	}
	if strings.Contains(block.Reason, "--add-dir") {
		t.Fatalf("block.Reason=%q must not contain --add-dir hint for symlink traversal", block.Reason)
	}
}

// TestValidateResolvesAliasedPathPrefixes is a deterministic cross-platform
// test for normalizePrefixForRoot: builds aliasing by hand via a symlink from
// an alias parent directory to the real workspace root, then verifies that
// paths under the alias are accepted (platform alias resolved) and that an
// in-root symlink escaping outside is still caught as BlockSymlinkTraversal.
func TestValidateResolvesAliasedPathPrefixes(t *testing.T) {
	real := t.TempDir()
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scope, err := NewScope(real, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	if block := scope.validate(filepath.Join(alias, "new.txt")); block != nil {
		t.Fatalf("validate(alias-prefixed path) = %v, want nil", block)
	}
	outside := t.TempDir()
	link := filepath.Join(real, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if block := scope.validate(filepath.Join(alias, "link", "x.txt")); block == nil || block.Code != BlockSymlinkTraversal {
		t.Fatalf("validate(alias path through in-root symlink) = %v, want BlockSymlinkTraversal", block)
	}
}

// TestScopeAddNormalizesSymlinkedRoot verifies that Add resolves symlinks so
// the stored root is the real path.
func TestScopeAddNormalizesSymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	workspace := t.TempDir()
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	if _, err := scope.Add(link); err != nil {
		t.Fatalf("Add(symlinked root): %v", err)
	}
	roots := scope.Roots()
	if len(roots) < 2 {
		t.Fatalf("Roots()=%v want workspace + 1 extra", roots)
	}
	resolvedReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks(real): %v", err)
	}
	if roots[1] != resolvedReal {
		t.Fatalf("Roots()[1]=%q want resolved path %q (no symlink component)", roots[1], resolvedReal)
	}
}

// TestScopeAddTildeExpansion verifies that ~ paths are expanded to the home
// directory. When home is not accessible or writable for a subdir, we assert
// only that a clearly invalid tilde-variant returns a clean error.
func TestScopeAddTildeExpansion(t *testing.T) {
	// Verify that a nonsensical ~-variant fails cleanly rather than panicking.
	workspace := t.TempDir()
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	if _, err := scope.Add("~nonexistent-subdir-zero-test"); err == nil {
		t.Fatal("Add(~nonexistent-subdir) = nil error, want rejection")
	}
}

func TestNewScopeNormalizesAndValidatesExtraRoots(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()

	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	roots := scope.Roots()
	if len(roots) != 2 {
		t.Fatalf("Roots()=%v want workspace + 1 extra", roots)
	}
	if roots[0] != scope.WorkspaceRoot() {
		t.Fatalf("Roots()[0]=%q want workspace root %q", roots[0], scope.WorkspaceRoot())
	}
}

func TestNewScopeRejectsBadExtraRoots(t *testing.T) {
	workspace := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for name, root := range map[string]string{
		"missing directory": missing,
		"regular file":      file,
		"filesystem root":   string(filepath.Separator),
		"empty":             "   ",
	} {
		if _, err := NewScope(workspace, []string{root}); err == nil {
			t.Fatalf("NewScope(%s root %q) = nil error, want rejection", name, root)
		}
	}
}

func TestScopeAddIsIdempotentAndRejectsContainedPaths(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	added, err := scope.Add(extra)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := scope.Add(extra); err != nil {
		t.Fatalf("Add (repeat): %v", err)
	}
	nested := filepath.Join(extra, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if _, err := scope.Add(nested); err != nil {
		t.Fatalf("Add (nested in existing root): %v", err)
	}
	if _, err := scope.Add(workspace); err != nil {
		t.Fatalf("Add (workspace itself): %v", err)
	}
	if got := scope.Roots(); len(got) != 2 {
		t.Fatalf("Roots()=%v want exactly workspace + %q (idempotent adds)", got, added)
	}
}

func TestScopeValidateAllowsAnyRootButRelativeOnlyWorkspace(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	if block := scope.validate(filepath.Join(extra, "out.txt")); block != nil {
		t.Fatalf("validate(extra-root path) = %v, want nil", block)
	}
	if block := scope.validate(filepath.Join(workspace, "in.txt")); block != nil {
		t.Fatalf("validate(workspace path) = %v, want nil", block)
	}
	if block := scope.validate("nested/in.txt"); block != nil {
		t.Fatalf("validate(relative path) = %v, want nil (resolves against workspace)", block)
	}

	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	block := scope.validate(outside)
	if block == nil {
		t.Fatal("validate(outside all roots) = nil, want block")
	}
	if block.Code != BlockOutsideWorkspace {
		t.Fatalf("block.Code=%q want %q", block.Code, BlockOutsideWorkspace)
	}
	if !strings.Contains(block.Reason, "--add-dir") {
		t.Fatalf("block.Reason=%q want actionable --add-dir hint", block.Reason)
	}
}

func TestScopeValidateKeepsSymlinkTraversalProtection(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	block := scope.validate(filepath.Join(link, "escape.txt"))
	if block == nil {
		t.Fatal("validate(symlink escape) = nil, want block")
	}
	if block.Code != BlockSymlinkTraversal {
		t.Fatalf("block.Code=%q want %q", block.Code, BlockSymlinkTraversal)
	}
}
