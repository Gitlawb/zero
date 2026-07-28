//go:build windows

package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestPromoteInstallsTheStagedObjectNotTheStagedPath is the regression test for
// the live handoff half of #742: randomizing the staging name and creating it
// exclusively stops PRE-creation, but not substitution after the verified bytes
// are written. Windows renames through the staging HANDLE, so a substituted entry
// at the staging pathname is simply not what gets promoted.
func TestPromoteInstallsTheStagedObjectNotTheStagedPath(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "new-binary")
	if err := os.WriteFile(sourcePath, []byte("verified-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	staged, err := stageBinary(sourcePath, targetPath)
	if err != nil {
		t.Fatalf("stageBinary: %v", err)
	}
	discarded := false
	defer func() {
		if !discarded {
			staged.discard()
		}
	}()

	// Rehearse the strongest form of the substitution: the staging entry is
	// replaced wholesale between the write and the swap. (A real attacker also has
	// to get past the exclusive share mode this handle holds; the test does not,
	// which only makes the check stricter.)
	substituted := false
	if err := os.Remove(staged.path); err == nil {
		if err := os.WriteFile(staged.path, []byte("attacker-binary"), 0o755); err != nil {
			t.Fatalf("WriteFile substituted staging entry: %v", err)
		}
		substituted = true
	}

	if err := staged.promote(targetPath); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// The staging handle keeps the promoted file open with an exclusive share
	// mode, so release it before reading the installed bytes (installBinary's
	// deferred discard does the same).
	staged.discard()
	discarded = true

	installed, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile installed: %v", err)
	}
	if string(installed) != "verified-binary" {
		t.Fatalf("installed binary = %q, want the verified bytes", installed)
	}
	if !substituted {
		t.Log("the staging entry could not be replaced (exclusive share mode); the handle-bound rename was still exercised")
	}
}

// TestPromoteRejectsALyingRenameByHandle is the regression test for a review
// finding on PR #751: SetFileInformationByHandle reporting success is not, on
// its own, proof the object actually ended up at targetPath. Some Windows
// versions have been observed accepting the rename call against a handle
// whose directory entry was substituted out from under it without the object
// actually moving — this simulates that by stubbing the rename to lie, since
// the real trigger condition is Windows-version-specific and not reliably
// reproducible on demand. Without verifyPromotedTarget, promote would return
// nil while targetPath silently ends up missing, reporting a successful
// update that actually stranded the user without an executable at all.
func TestPromoteRejectsALyingRenameByHandle(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "new-binary")
	if err := os.WriteFile(sourcePath, []byte("verified-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	staged, err := stageBinary(sourcePath, targetPath)
	if err != nil {
		t.Fatalf("stageBinary: %v", err)
	}
	defer staged.discard()

	original := renameFileByHandle
	renameFileByHandle = func(file *os.File, target string) error {
		return nil // lie: report success without touching anything
	}
	defer func() { renameFileByHandle = original }()

	promoteErr := staged.promote(targetPath)
	if promoteErr == nil {
		t.Fatal("promote reported success for a rename that never actually happened, want an error")
	}
	if !strings.Contains(promoteErr.Error(), "unreachable") {
		t.Fatalf("error = %q, want it to explain the target is unreachable", promoteErr.Error())
	}
	installed, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(installed) != "old-binary" {
		t.Fatalf("target = %q, want the original binary restored", installed)
	}
	if _, err := os.Stat(targetPath + ".old"); !os.IsNotExist(err) {
		t.Fatalf(".old leftover survived a successful restore: %v", err)
	}
}

// A promotion failure followed by a blocked restore is security-relevant all
// the way through installBinary; its contextual wrappers must not erase the
// sentinel that callers of Apply use to distinguish possible path tampering.
func TestInstallBinaryPreservesPossibleTamperingError(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "new-binary")
	if err := os.WriteFile(sourcePath, []byte("verified-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	original := renameFileByHandle
	var conflicting windows.Handle
	renameFileByHandle = func(_ *os.File, target string) error {
		pathPtr, err := windows.UTF16PtrFromString(target)
		if err != nil {
			return err
		}
		conflicting, err = windows.CreateFile(pathPtr, windows.GENERIC_WRITE, 0, nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			return fmt.Errorf("create conflicting target: %w", err)
		}
		return errors.New("injected promotion failure")
	}
	t.Cleanup(func() {
		renameFileByHandle = original
		if conflicting != 0 {
			_ = windows.CloseHandle(conflicting)
		}
	})

	err := installBinary(sourcePath, targetPath)
	if !errors.Is(err, ErrTargetPossiblyTampered) {
		t.Fatalf("installBinary error = %v, want it to wrap ErrTargetPossiblyTampered", err)
	}
}

func TestVerifyPromotedTargetRejectsDifferentRegularFile(t *testing.T) {
	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "staged.exe")
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(stagedPath, []byte("verified"), 0o755); err != nil {
		t.Fatalf("WriteFile staged: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("attacker"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	staged, err := os.Open(stagedPath)
	if err != nil {
		t.Fatalf("Open staged: %v", err)
	}
	defer func() { _ = staged.Close() }()

	if err := verifyPromotedTarget(staged, targetPath); err == nil {
		t.Fatal("verifyPromotedTarget accepted a different regular file at targetPath")
	}
}

func TestVerifyPromotedTargetRejectsAHardLinkedName(t *testing.T) {
	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "staged.exe")
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(stagedPath, []byte("verified"), 0o755); err != nil {
		t.Fatalf("WriteFile staged: %v", err)
	}
	if err := os.Link(stagedPath, targetPath); err != nil {
		t.Fatalf("Link target: %v", err)
	}
	staged, err := os.Open(stagedPath)
	if err != nil {
		t.Fatalf("Open staged: %v", err)
	}
	defer func() { _ = staged.Close() }()

	if err := verifyPromotedTarget(staged, targetPath); err == nil {
		t.Fatal("verifyPromotedTarget accepted a second hard-linked name without a completed rename")
	}
}

func TestInstallBinaryThroughReparsePointAncestor(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("Mkdir real install directory: %v", err)
	}
	linkedDir := filepath.Join(root, "linked")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	targetPath := filepath.Join(linkedDir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "new-binary")
	if err := os.WriteFile(sourcePath, []byte("verified-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	if err := installBinary(sourcePath, targetPath); err != nil {
		t.Fatalf("installBinary through reparse-point ancestor: %v", err)
	}
	installed, err := os.ReadFile(filepath.Join(realDir, "zero.exe"))
	if err != nil {
		t.Fatalf("ReadFile installed: %v", err)
	}
	if string(installed) != "verified-binary" {
		t.Fatalf("installed binary = %q, want the verified bytes", installed)
	}
}

// TestInstallBinaryInstallsVerifiedBytes is the success control for the ordinary
// path: the staged bytes land at the target, the running binary is preserved as
// "<target>.old", and no staging artifact survives.
func TestInstallBinaryInstallsVerifiedBytes(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "new-binary")
	if err := os.WriteFile(sourcePath, []byte("verified-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	if err := installBinary(sourcePath, targetPath); err != nil {
		t.Fatalf("installBinary: %v", err)
	}
	installed, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile installed: %v", err)
	}
	if string(installed) != "verified-binary" {
		t.Fatalf("installed binary = %q, want the verified bytes", installed)
	}
	if old, err := os.ReadFile(targetPath + ".old"); err != nil {
		t.Fatalf("the replaced binary must be preserved for later cleanup: %v", err)
	} else if string(old) != "old-binary" {
		t.Fatalf("preserved binary = %q, want the previous one", old)
	}
	assertNoStagingLeftovers(t, dir)
}

// TestInstallBinaryCleansUpWhenStagingFails covers the cleanup ordering: a
// failure after the staging file exists must not leave it behind, because each
// attempt now uses a fresh random name that the next attempt never reuses.
func TestInstallBinaryCleansUpWhenStagingFails(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	if err := installBinary(filepath.Join(t.TempDir(), "missing-source"), targetPath); err == nil {
		t.Fatal("installBinary with an unreadable source succeeded, want error")
	}
	assertNoStagingLeftovers(t, dir)
}
