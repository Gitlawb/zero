//go:build windows

package update

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestMarkOldBinaryPreservedRefusesPreCreatedLink covers jatmn's #751 finding
// that the marker was a predictable link-following truncate write: the path is
// fixed, so a lower-privileged writer in the install directory can pre-create it
// as a hard link (or reparse point) and have the elevated updater truncate and
// write through it into a file of their choosing.
func TestMarkOldBinaryPreservedRefusesPreCreatedLink(t *testing.T) {
	for _, kind := range []string{"hardlink", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			oldPath := filepath.Join(dir, "zero.exe.old")
			if err := os.WriteFile(oldPath, []byte("known-good"), 0o755); err != nil {
				t.Fatalf("WriteFile old binary: %v", err)
			}
			victim := filepath.Join(t.TempDir(), "victim.txt")
			const victimContent = "attacker-chosen target that must not be written"
			if err := os.WriteFile(victim, []byte(victimContent), 0o600); err != nil {
				t.Fatalf("WriteFile victim: %v", err)
			}

			markerPath := oldPath + oldBinaryPreservedSuffix
			var linkErr error
			switch kind {
			case "hardlink":
				linkErr = os.Link(victim, markerPath)
			case "symlink":
				linkErr = os.Symlink(victim, markerPath)
			}
			if linkErr != nil {
				t.Skipf("%s unsupported here: %v", kind, linkErr)
			}

			// Whatever this returns, the one thing it must not do is write through
			// the planted object. Reporting the marker as present is the safe
			// answer: it makes the next run PRESERVE the recovery copy.
			_ = markOldBinaryPreserved(oldPath)

			got, err := os.ReadFile(victim)
			if err != nil {
				t.Fatalf("ReadFile victim: %v", err)
			}
			if string(got) != victimContent {
				t.Fatalf("marker write followed the planted %s and wrote into %q: %q", kind, victim, got)
			}
			// The recovery copy is still preserved, which is the marker's purpose.
			targetPath := filepath.Join(dir, "zero.exe")
			if err := os.WriteFile(targetPath, []byte("unverified"), 0o755); err != nil {
				t.Fatalf("WriteFile target: %v", err)
			}
			CleanupStaleBinary(targetPath)
			if _, err := os.Stat(oldPath); err != nil {
				t.Fatalf("recovery copy was removed despite a marker being present: %v", err)
			}
		})
	}
}

// TestRestoreOriginalBinaryKeepsRecoveryCopyWhenMarkingFails covers the half of
// CodeRabbit's marker finding that surfacing the failure alone does not: when no
// marker can be established, nothing on disk tells the next run to keep the
// copy, so it is moved out from under routine cleanup instead of being left at
// the one name CleanupStaleBinary deletes.
func TestRestoreOriginalBinaryKeepsRecoveryCopyWhenMarkingFails(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	oldPath := targetPath + ".old"
	if err := os.WriteFile(targetPath, []byte("unverified"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.WriteFile(oldPath, []byte("known-good"), 0o755); err != nil {
		t.Fatalf("WriteFile old binary: %v", err)
	}
	// Hold the target with no sharing so the restore rename fails.
	blocker, err := openWithoutSharing(targetPath)
	if err != nil {
		t.Skipf("cannot hold the target exclusively on this filesystem: %v", err)
	}
	defer func() { _ = blocker.Close() }()
	// Force the marker to be unestablishable. Doing it through the seam rather
	// than by breaking the filesystem keeps oldPath itself intact, which is the
	// state this behavior is about.
	originalMark := markOldBinaryPreserved
	markOldBinaryPreserved = func(string) error { return errors.New("injected marker failure") }
	t.Cleanup(func() { markOldBinaryPreserved = originalMark })
	stubRandomStagingSuffix(t, "deadbeef")

	err = restoreOriginalBinary(oldPath, targetPath)
	if !errors.Is(err, ErrTargetPossiblyTampered) {
		t.Fatalf("restore error = %v, want ErrTargetPossiblyTampered", err)
	}
	kept := oldPath + ".deadbeef.recovery"
	if !strings.Contains(err.Error(), kept) {
		t.Fatalf("error = %v, want it to name the path the copy was moved to", err)
	}
	got, readErr := os.ReadFile(kept)
	if readErr != nil {
		t.Fatalf("recovery copy was not kept: %v", readErr)
	}
	if string(got) != "known-good" {
		t.Fatalf("kept copy = %q, want the last verified binary", got)
	}
	// And routine cleanup cannot reach it: it only ever removes "<target>.old".
	CleanupStaleBinary(targetPath)
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("cleanup removed the kept recovery copy: %v", err)
	}
}

// TestOldBinaryPreservedTreatsAnUnreadableMarkerAsPresent pins the conservative
// side of the marker check: only a definite "not there" allows the copy to be
// deleted, because deleting it is irreversible and keeping it costs a file.
func TestOldBinaryPreservedTreatsAnUnreadableMarkerAsPresent(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "zero.exe.old")
	if oldBinaryPreserved(oldPath) {
		t.Fatal("a genuinely absent marker must report not-preserved")
	}
	// A directory at the marker name is an entry Lstat can see; anything other
	// than a clean not-exist keeps the copy.
	if err := os.Mkdir(oldPath+oldBinaryPreservedSuffix, 0o700); err != nil {
		t.Fatalf("Mkdir marker: %v", err)
	}
	if !oldBinaryPreserved(oldPath) {
		t.Fatal("an entry at the marker name must count as preserved")
	}
}

// TestRestoreOriginalBinarySurfacesMarkerWriteFailure covers the #751 P3: the
// error promises the original is preserved at <target>.old, but if the marker
// cannot be written the next run's cleanup deletes exactly that file. The
// operator has to be told to act now rather than at their convenience.
func TestRestoreOriginalBinarySurfacesMarkerWriteFailure(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("unverified"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	// An oldPath under a directory that does not exist: the restore rename fails
	// (nothing to move) and so does the marker creation beside it.
	oldPath := filepath.Join(dir, "missing-dir", "zero.exe.old")

	err := restoreOriginalBinary(oldPath, targetPath)
	if !errors.Is(err, ErrTargetPossiblyTampered) {
		t.Fatalf("restore error = %v, want ErrTargetPossiblyTampered", err)
	}
	if !strings.Contains(err.Error(), "recovery marker could not be written") {
		t.Fatalf("error = %v, want it to disclose the failed marker", err)
	}
	if !strings.Contains(err.Error(), oldPath) {
		t.Fatalf("error = %v, want the path the operator must copy", err)
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
