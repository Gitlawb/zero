//go:build windows

package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
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

// TestRestoreOriginalBinaryFlagsPossibleTamperingWhenRestoreFails is the
// regression test for a review finding on PR #751: when a promotion attempt
// fails AND the restore of the preserved original also cannot get past
// whatever now occupies targetPath, that combination must be reported as a
// security-relevant condition (ErrTargetPossiblyTampered), not folded into
// the same generic error a stalled download would produce — the caller needs
// to be able to tell "try the update again later" apart from "verify what is
// at this path before running it again".
func TestRestoreOriginalBinaryFlagsPossibleTamperingWhenRestoreFails(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "zero.exe.old")
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(oldPath, []byte("original"), 0o755); err != nil {
		t.Fatalf("WriteFile oldPath: %v", err)
	}

	// Simulate an attacker occupying targetPath with a lock MOVEFILE_REPLACE_EXISTING
	// cannot get past: an exclusive, no-share open.
	pathPtr, err := windows.UTF16PtrFromString(targetPath)
	if err != nil {
		t.Fatalf("UTF16PtrFromString: %v", err)
	}
	handle, err := windows.CreateFile(pathPtr, windows.GENERIC_WRITE, 0, nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFile targetPath: %v", err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	restoreErr := restoreOriginalBinary(oldPath, targetPath)
	if restoreErr == nil {
		t.Fatal("restoreOriginalBinary succeeded despite a conflicting exclusive lock on targetPath, want an error")
	}
	if !errors.Is(restoreErr, ErrTargetPossiblyTampered) {
		t.Fatalf("error = %v, want it to wrap ErrTargetPossiblyTampered", restoreErr)
	}
}

func TestCleanupStaleBinaryPreservesUnverifiableStagingFiles(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	unverifiable := filepath.Join(dir, "zero.exe.0123456789abcdef0123456789abcdef.new")
	if err := os.WriteFile(unverifiable, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	CleanupStaleBinary(targetPath)

	if _, err := os.Stat(unverifiable); err != nil {
		t.Fatalf("unverifiable staging file must be preserved: %v", err)
	}
}
