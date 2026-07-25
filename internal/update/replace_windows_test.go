//go:build windows

package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestIsGeneratedStagingFileName(t *testing.T) {
	hex32 := "0123456789abcdef0123456789abcdef"
	cases := map[string]bool{
		"zero.exe." + hex32 + ".new":     true,
		"zero.exe." + hex32[:31] + ".new": false, // one hex char short
		"zero.exe." + hex32 + "A.new":    false,  // uppercase hex, not what hex.EncodeToString produces
		"zero.exe.release-notes.new":     false,  // loose look-alike a user could plausibly have
		"zero.exe.backup":                false,  // no .new suffix
		"other.exe." + hex32 + ".new":    false,  // wrong binary name
	}
	for name, want := range cases {
		if got := isGeneratedStagingFileName("zero.exe", name); got != want {
			t.Errorf("isGeneratedStagingFileName(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestRemoveStaleStagingLeftoversIgnoresLookalikeNames covers the cleanup
// finding from the same PR #751 review: only the exact generated shape
// ("<binary>.<32 lowercase hex chars>.new") is swept, so a legitimate file
// that merely starts and ends the same way — e.g. release notes a user saved
// next to the binary — survives regardless of age.
func TestRemoveStaleStagingLeftoversIgnoresLookalikeNames(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	generated := filepath.Join(dir, "zero.exe.0123456789abcdef0123456789abcdef.new")
	lookalike := filepath.Join(dir, "zero.exe.release-notes.new")
	for _, path := range []string{generated, lookalike} {
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
	}
	stale := time.Now().Add(-2 * stagingLeftoverMinAge)
	for _, path := range []string{generated, lookalike} {
		if err := os.Chtimes(path, stale, stale); err != nil {
			t.Fatalf("Chtimes %s: %v", path, err)
		}
	}

	removeStaleStagingLeftovers(targetPath, time.Now())

	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("generated staging leftover survived: %v", err)
	}
	if _, err := os.Stat(lookalike); err != nil {
		t.Fatalf("look-alike file must be left alone: %v", err)
	}
}
