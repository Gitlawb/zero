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

func TestCleanupStaleBinaryPreservesOldWhenTargetIsAbsent(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	oldPath := targetPath + ".old"
	if err := os.WriteFile(oldPath, []byte("known-good"), 0o755); err != nil {
		t.Fatalf("WriteFile old binary: %v", err)
	}

	CleanupStaleBinary(targetPath)

	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("target must not be created: %v", err)
	}
	got, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("ReadFile preserved old binary: %v", err)
	}
	if string(got) != "known-good" {
		t.Fatalf("preserved old binary = %q, want known-good", got)
	}
}

// TestCleanupStaleBinaryPreservesMarkedOldWhenTargetExists covers jatmn's #751
// P3 follow-up: after ErrTargetPossiblyTampered, targetPath holds exactly the
// bytes the updater could NOT verify while .old holds the ones it could. The
// next Apply saw a present target and deleted .old as an ordinary leftover,
// erasing the recovery copy the failure had just told the operator to use.
func TestCleanupStaleBinaryPreservesMarkedOldWhenTargetExists(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	oldPath := targetPath + ".old"
	if err := os.WriteFile(targetPath, []byte("unverified"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.WriteFile(oldPath, []byte("known-good"), 0o755); err != nil {
		t.Fatalf("WriteFile old binary: %v", err)
	}
	markOldBinaryPreserved(oldPath)

	CleanupStaleBinary(targetPath)

	got, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("marked recovery copy was removed: %v", err)
	}
	if string(got) != "known-good" {
		t.Fatalf("preserved old binary = %q, want known-good", got)
	}
	if _, err := os.Stat(oldPath + oldBinaryPreservedSuffix); err != nil {
		t.Fatalf("marker must survive alongside the copy it protects: %v", err)
	}

	// Once the marker is cleared — which a successful promotion does — the copy
	// is an ordinary leftover again.
	clearOldBinaryPreserved(oldPath)
	CleanupStaleBinary(targetPath)
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("unmarked old binary was not removed: %v", err)
	}
}

// TestRestoreOriginalBinaryMarksPreservedCopy pins the other half: the path that
// reports "original preserved at <.old>" is the path that makes that true across
// runs.
func TestRestoreOriginalBinaryMarksPreservedCopy(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	oldPath := targetPath + ".old"
	if err := os.WriteFile(oldPath, []byte("known-good"), 0o755); err != nil {
		t.Fatalf("WriteFile old binary: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("unverified"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	// Hold the target with no sharing so the restore rename cannot replace it,
	// which is the condition ErrTargetPossiblyTampered describes.
	blocker, err := openWithoutSharing(targetPath)
	if err != nil {
		t.Skipf("cannot hold the target exclusively on this filesystem: %v", err)
	}
	defer func() { _ = blocker.Close() }()

	err = restoreOriginalBinary(oldPath, targetPath)
	if !errors.Is(err, ErrTargetPossiblyTampered) {
		t.Fatalf("restore error = %v, want ErrTargetPossiblyTampered", err)
	}
	if !oldBinaryPreserved(oldPath) {
		t.Fatal("a failed restore must mark the preserved copy so later cleanup keeps it")
	}
	CleanupStaleBinary(targetPath)
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("recovery copy was removed after a failed restore: %v", err)
	}
}

// openWithoutSharing opens an existing file denying every share mode, so a
// rename onto it fails the way a principal squatting the executable path does.
func openWithoutSharing(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func TestCleanupStaleBinaryRemovesOldWhenTargetExists(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	oldPath := targetPath + ".old"
	if err := os.WriteFile(targetPath, []byte("current"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.WriteFile(oldPath, []byte("stale"), 0o755); err != nil {
		t.Fatalf("WriteFile old binary: %v", err)
	}

	CleanupStaleBinary(targetPath)

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("stale old binary was not removed: %v", err)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(got) != "current" {
		t.Fatalf("target = %q, want current", got)
	}
}
