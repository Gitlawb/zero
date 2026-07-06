//go:build windows

package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceBinaryReplacesRunningBinary(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	newPath := filepath.Join(dir, "zero.exe.new")

	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("new-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile new: %v", err)
	}

	if err := replaceBinary(targetPath, newPath); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(data) != "new-binary" {
		t.Fatalf("target content = %q, want %q", data, "new-binary")
	}
	if _, err := os.Stat(targetPath + ".old"); err != nil {
		t.Fatalf("expected the original binary to be preserved at %s.old: %v", targetPath, err)
	}
}

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
