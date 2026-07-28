//go:build windows

package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestReplaceWithRetryKeepsTheDestinationIdentity proves the publish goes through
// ReplaceFileW and not os.Rename: ReplaceFileW gives the replacement the replaced
// file's identity, so the destination's creation time survives. A rename would
// carry the temporary file's creation time over instead, and os.Rename is also
// documented non-atomic on Windows.
func TestReplaceWithRetryKeepsTheDestinationIdentity(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "manifest.md")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile dst: %v", err)
	}
	created := creationTime(t, dst)

	src := filepath.Join(dir, ".manifest.tmp")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	// Push the replacement's own creation time clearly past the destination's so a
	// rename would be visible in the comparison below.
	future := windows.NsecToFiletime(created.Nanoseconds() + int64(10*1e9))
	setCreationTime(t, src, future)

	if err := ReplaceWithRetry(src, dst, nil); err != nil {
		t.Fatalf("ReplaceWithRetry: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("destination content = %q, want the replacement bytes", data)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("the replacement file should be consumed by the replace: %v", err)
	}
	assertNoReplaceBackups(t, dir)
	if got := creationTime(t, dst); got != created {
		t.Fatalf("creation time = %v, want the replaced file's %v (a rename would not preserve it)", got, created)
	}
}

// TestReplaceWithRetryPreservesDestinationDACL is the regression test for the
// second half of the finding: the replacement is a freshly created temporary file
// carrying the directory's inherited DACL, so publishing it with a rename would
// REPLACE the restrictive descriptor an explicitly locked-down file had.
func TestReplaceWithRetryPreservesDestinationDACL(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "manifest.md")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile dst: %v", err)
	}
	// A protected DACL granting only the owner: distinct from whatever the temp
	// file inherits from the directory.
	restricted, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;OW)")
	if err != nil {
		t.Skipf("cannot build a test security descriptor: %v", err)
	}
	dacl, _, err := restricted.DACL()
	if err != nil {
		t.Skipf("cannot read the test DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		dst,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Skipf("cannot apply a restrictive DACL on this filesystem: %v", err)
	}
	want := describeDACL(t, dst)

	src := filepath.Join(dir, ".manifest.tmp")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	if inherited := describeDACL(t, src); inherited == want {
		t.Skip("the temporary file already carries the same DACL; this filesystem cannot show the difference")
	}

	if err := ReplaceWithRetry(src, dst, nil); err != nil {
		t.Fatalf("ReplaceWithRetry: %v", err)
	}
	if got := describeDACL(t, dst); got != want {
		t.Fatalf("DACL after replace = %q, want the destination's own %q", got, want)
	}
	assertNoReplaceBackups(t, dir)
}

// TestReplaceWithRetryPublishesWhenDestinationIsMissing covers the no-destination
// case: ReplaceFileW requires an existing file to replace, so there is a rename
// fallback (and nothing to preserve).
func TestReplaceWithRetryPublishesWhenDestinationIsMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".manifest.tmp")
	dst := filepath.Join(dir, "manifest.md")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	if err := ReplaceWithRetry(src, dst, nil); err != nil {
		t.Fatalf("ReplaceWithRetry: %v", err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "new" {
		t.Fatalf("destination = %q err=%v, want the replacement bytes", data, err)
	}
	assertNoReplaceBackups(t, dir)
}

// TestReplaceWithRetryRetriesTransientLockViolation keeps the retry behavior that
// RenameWithRetry provides for antivirus/indexer holds.
func TestReplaceWithRetryRetriesTransientLockViolation(t *testing.T) {
	attempts := 0
	err := ReplaceWithRetry("src", "dst", func(src, dst string) error {
		attempts++
		if attempts < 3 {
			return &os.PathError{Op: "replace", Path: dst, Err: syscall.Errno(32)} // ERROR_SHARING_VIOLATION
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReplaceWithRetry: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want the transient violations retried", attempts)
	}
}

// TestReplaceFileFlagsDoNotIgnoreMergeErrors is the regression test for jatmn's
// #757 P1 finding on flags: the call used to pass REPLACEFILE_IGNORE_MERGE_ERRORS
// (0x2) while the comment claimed ACL failures were fail-closed. Microsoft
// documents 0x2 and REPLACEFILE_IGNORE_ACL_ERRORS (0x4) identically — with either
// one set, a call lacking WRITE_DAC "succeeds but the ACLs are not preserved" —
// so passing 0x2 let a --force overwrite silently publish the temporary file's
// inherited directory DACL over a restricted specialist and expose its system
// prompt.
//
// This asserts the flag word directly rather than a live denied merge because a
// denied merge is not constructible for a file this process owns: Windows grants
// an object's owner READ_CONTROL and WRITE_DAC implicitly, so no DACL a test can
// apply to its own temp file can withhold WRITE_DAC from it. Pinning the flags is
// what actually prevents the regression — re-adding either bit fails here.
func TestReplaceFileFlagsDoNotIgnoreMergeErrors(t *testing.T) {
	const (
		ignoreMergeErrors = 0x00000002
		ignoreACLErrors   = 0x00000004
	)
	if replaceFileFlags&ignoreMergeErrors != 0 {
		t.Error("REPLACEFILE_IGNORE_MERGE_ERRORS must not be set: it makes ReplaceFileW succeed WITHOUT preserving ACLs when it cannot obtain WRITE_DAC")
	}
	if replaceFileFlags&ignoreACLErrors != 0 {
		t.Error("REPLACEFILE_IGNORE_ACL_ERRORS must not be set: it makes ReplaceFileW succeed WITHOUT preserving ACLs when it cannot obtain WRITE_DAC")
	}
}

func TestReplaceExistingRejectsUnsupportedAtomicReplacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		code syscall.Errno
	}{
		{name: "ERROR_INVALID_FUNCTION", code: errorInvalidFunction},
		{name: "ERROR_NOT_SUPPORTED", code: errorNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, ".manifest.tmp")
			dst := filepath.Join(dir, "manifest.md")
			if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
				t.Fatalf("WriteFile src: %v", err)
			}
			if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
				t.Fatalf("WriteFile dst: %v", err)
			}

			err := replaceExistingWith(src, dst, func(replaced, replacement, backup string) error {
				if replaced != dst || replacement != src {
					t.Fatalf("ReplaceFileW paths = (%q, %q), want (%q, %q)", replaced, replacement, dst, src)
				}
				if filepath.Dir(backup) != dir {
					t.Fatalf("backup directory = %q, want sibling directory %q", filepath.Dir(backup), dir)
				}
				return tc.code
			}, nil)
			if !errors.Is(err, tc.code) {
				t.Fatalf("error = %v, want unsupported error %v", err, tc.code)
			}
			if !strings.Contains(err.Error(), "atomic replacement is not supported") {
				t.Fatalf("error = %v, want an atomic-replacement explanation", err)
			}
			assertFileContent(t, dst, "old")
			assertFileContent(t, src, "new")
			assertNoReplaceBackups(t, dir)
		})
	}
}

func TestReplaceExistingCleansManagedBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".manifest.tmp")
	dst := filepath.Join(dir, "manifest.md")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile dst: %v", err)
	}

	var backupPath string
	err := replaceExistingWith(src, dst, func(replaced, replacement, backup string) error {
		backupPath = backup
		if matched, matchErr := filepath.Match(replaceBackupPattern, filepath.Base(backup)); matchErr != nil || !matched {
			t.Fatalf("backup path = %q, want pattern %q (match error: %v)", backup, replaceBackupPattern, matchErr)
		}
		if _, statErr := os.Lstat(backup); !os.IsNotExist(statErr) {
			t.Fatalf("backup path must be vacant before ReplaceFileW: %v", statErr)
		}
		if renameErr := os.Rename(replaced, backup); renameErr != nil {
			t.Fatalf("stage backup: %v", renameErr)
		}
		// Windows refuses to remove a read-only file. The cleanup path must clear
		// that attribute without changing the backup's DACL, then remove it.
		if chmodErr := os.Chmod(backup, 0o400); chmodErr != nil {
			t.Fatalf("make backup read-only: %v", chmodErr)
		}
		return os.Rename(replacement, replaced)
	}, nil)
	if err != nil {
		t.Fatalf("replaceExistingWith: %v", err)
	}
	assertFileContent(t, dst, "new")
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("replacement source should be consumed: %v", err)
	}
	if _, err := os.Lstat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("managed backup should be removed: %v", err)
	}
	assertNoReplaceBackups(t, dir)
}

func TestReplaceWithRetryDoesNotRetryCommittedReplacementWhenBackupCleanupFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".manifest.tmp")
	dst := filepath.Join(dir, "manifest.md")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	attempts := 0
	cleanupErr := &os.PathError{Op: "remove", Path: "managed backup", Err: syscall.Errno(32)}
	var backupPath string
	err := ReplaceWithRetry(src, dst, func(src, dst string) error {
		attempts++
		return replaceExistingWithCleanup(src, dst, func(replaced, replacement, backup string) error {
			backupPath = backup
			if err := os.Rename(replaced, backup); err != nil {
				return err
			}
			return os.Rename(replacement, replaced)
		}, nil, func(got string) error {
			if got != backupPath {
				t.Fatalf("cleanup backup path = %q, want %q", got, backupPath)
			}
			return cleanupErr
		})
	})
	var outcome *CommittedReplacementCleanupError
	if !errors.As(err, &outcome) {
		t.Fatalf("error = %v, want committed cleanup outcome", err)
	}
	if outcome.BackupPath != backupPath || !errors.Is(outcome, syscall.Errno(32)) {
		t.Fatalf("outcome = %#v, want backup %q and sharing violation", outcome, backupPath)
	}
	if attempts != 1 {
		t.Fatalf("replacement attempts = %d, want 1", attempts)
	}
	assertFileContent(t, dst, "new")
	assertFileContent(t, backupPath, "old")
}

func TestReplaceExistingKeeps1176FilesAtOriginalNames(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".manifest.tmp")
	dst := filepath.Join(dir, "manifest.md")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile dst: %v", err)
	}

	err := replaceExistingWith(src, dst, func(_, _, _ string) error {
		// With a backup name, Microsoft documents that 1176 leaves the replaced
		// and replacement files under their original names.
		return errorUnableToMoveReplacement
	}, nil)
	if !errors.Is(err, errorUnableToMoveReplacement) {
		t.Fatalf("error = %v, want %v", err, errorUnableToMoveReplacement)
	}
	assertFileContent(t, dst, "old")
	assertFileContent(t, src, "new")
	assertNoReplaceBackups(t, dir)
}

func TestReplaceExistingRollsBack1177AndRetriesTransientRestore(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".manifest.tmp")
	dst := filepath.Join(dir, "manifest.md")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile dst: %v", err)
	}

	var backupPath string
	restoreAttempts := 0
	err := replaceExistingWith(src, dst, func(replaced, _, backup string) error {
		backupPath = backup
		if renameErr := os.Rename(replaced, backup); renameErr != nil {
			t.Fatalf("stage documented 1177 backup: %v", renameErr)
		}
		return errorUnableToMoveReplacement2
	}, func(backup, destination string) error {
		restoreAttempts++
		if restoreAttempts < 3 {
			return &os.PathError{Op: "rename", Path: backup, Err: syscall.Errno(32)}
		}
		return os.Rename(backup, destination)
	})
	if !errors.Is(err, errorUnableToMoveReplacement2) {
		t.Fatalf("error = %v, want %v", err, errorUnableToMoveReplacement2)
	}
	if restoreAttempts != 3 {
		t.Fatalf("restore attempts = %d, want transient lock failures retried", restoreAttempts)
	}
	assertFileContent(t, dst, "old")
	assertFileContent(t, src, "new")
	if _, err := os.Lstat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup should be consumed by rollback: %v", err)
	}
	assertNoReplaceBackups(t, dir)
}

func TestReplaceExistingPreserves1177BackupWhenRollbackFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".manifest.tmp")
	dst := filepath.Join(dir, "manifest.md")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile dst: %v", err)
	}

	var backupPath string
	replaceAttempts := 0
	err := ReplaceWithRetry(src, dst, func(src, dst string) error {
		replaceAttempts++
		return replaceExistingWith(src, dst, func(replaced, _, backup string) error {
			backupPath = backup
			if renameErr := os.Rename(replaced, backup); renameErr != nil {
				t.Fatalf("stage documented 1177 backup: %v", renameErr)
			}
			return errorUnableToMoveReplacement2
		}, func(_, _ string) error {
			return &os.PathError{Op: "rename", Path: backupPath, Err: syscall.Errno(32)}
		})
	})
	if !errors.Is(err, errorUnableToMoveReplacement2) {
		t.Fatalf("error = %v, want %v", err, errorUnableToMoveReplacement2)
	}
	if replaceAttempts != 1 {
		t.Fatalf("replace attempts = %d, want no retry after a partial failure", replaceAttempts)
	}
	if os.IsPermission(err) || isWindowsSharingOrLockViolation(err) {
		t.Fatalf("partial-failure error exposes the rollback lock error to the outer retry loop: %v", err)
	}
	if !strings.Contains(err.Error(), backupPath) {
		t.Fatalf("error = %v, want retained backup path %q", err, backupPath)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("destination should remain absent after failed rollback: %v", err)
	}
	assertFileContent(t, backupPath, "old")
	assertFileContent(t, src, "new")
}

func TestReplaceExistingRejectsSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")
	dst := filepath.Join(dir, "manifest.md")
	src := filepath.Join(dir, ".manifest.tmp")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}
	if err := os.Symlink(target, dst); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	called := false
	err := replaceExistingWith(src, dst, func(_, _, _ string) error {
		called = true
		return nil
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to replace symlink") {
		t.Fatalf("symlink replacement error = %v", err)
	}
	if called {
		t.Fatal("ReplaceFileW callback was called for a symlink destination")
	}
	assertFileContent(t, target, "old")
	assertFileContent(t, src, "new")
	if info, err := os.Lstat(dst); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination symlink was changed: info=%v err=%v", info, err)
	}
	assertNoReplaceBackups(t, dir)
}

func TestRecoverPartialReplaceLeavesIntactStatesAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		code syscall.Errno
	}{
		{name: "ERROR_UNABLE_TO_REMOVE_REPLACED", code: errorUnableToRemoveReplaced},
		{name: "ERROR_ACCESS_DENIED", code: syscall.Errno(5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, ".manifest.tmp")
			dst := filepath.Join(dir, "manifest.md")
			backup := filepath.Join(dir, ".zero-replace-test.backup")
			if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
				t.Fatalf("WriteFile src: %v", err)
			}
			if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
				t.Fatalf("WriteFile dst: %v", err)
			}

			if err := recoverPartialReplace(tc.code, src, dst, backup, nil); !errors.Is(err, tc.code) {
				t.Fatalf("error = %v, want the original %v unchanged", err, tc.code)
			}
			assertFileContent(t, dst, "old")
			assertFileContent(t, src, "new")
			if _, err := os.Lstat(backup); !os.IsNotExist(err) {
				t.Fatalf("unexpected backup residue: %v", err)
			}
		})
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("content of %s = %q, want %q", path, data, want)
	}
}

func assertNoReplaceBackups(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, replaceBackupPattern))
	if err != nil {
		t.Fatalf("Glob replacement backups: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("replacement backups remain: %v", matches)
	}
}

func creationTime(t *testing.T, path string) syscall.Filetime {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat %s: %v", path, err)
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		t.Skipf("no Windows file attributes for %s", path)
	}
	return data.CreationTime
}

func setCreationTime(t *testing.T, path string, created windows.Filetime) {
	t.Helper()
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("UTF16PtrFromString: %v", err)
	}
	handle, err := windows.CreateFile(pathPtr, windows.FILE_WRITE_ATTRIBUTES, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFile %s: %v", path, err)
	}
	defer func() {
		_ = windows.CloseHandle(handle)
	}()
	if err := windows.SetFileTime(handle, &created, nil, nil); err != nil {
		t.Fatalf("SetFileTime %s: %v", path, err)
	}
}

func describeDACL(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Skipf("cannot read the security descriptor of %s: %v", path, err)
	}
	text := sd.String()
	if index := strings.Index(text, "D:"); index >= 0 {
		return text[index:]
	}
	return text
}
