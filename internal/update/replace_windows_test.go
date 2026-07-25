//go:build windows

package update

import (
	"os"
	"path/filepath"
	"testing"
)

// The replacement path itself (rename the running binary aside, then rename the
// staged object into place through its handle) is covered by
// TestInstallBinaryInstallsVerifiedBytes and
// TestPromoteInstallsTheStagedObjectNotTheStagedPath in
// stage_promote_windows_test.go, which exercise it through the staging handle the
// production code uses rather than a loose pathname.

func TestRenameWithRetrySucceedsImmediately(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}

	if err := renameWithRetry(src, dst); err != nil {
		t.Fatalf("renameWithRetry: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected dst to exist after rename: %v", err)
	}
}

// A permanently-failing rename (source never appears) must exhaust its
// retries and surface the underlying error, rather than retrying forever.
func TestRenameWithRetryFailsAfterExhaustingAttempts(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	dst := filepath.Join(dir, "dst")

	if err := renameWithRetry(missing, dst); err == nil {
		t.Fatal("expected renameWithRetry to fail for a source that never appears")
	}
}
