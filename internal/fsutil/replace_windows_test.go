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

// TestRecoverPartialReplaceRescuesTheReplacement is the regression test for
// jatmn's #757 second P1 finding. With no backup file, ReplaceFileW's 1176/1177
// failures leave the DESTINATION DELETED and the replacement surviving only under
// its temporary name. Returning those errors unrepaired let the caller's deferred
// temp-file cleanup remove that sole surviving copy, so one failed
// `specialist create --force` destroyed the old manifest AND the new one.
func TestRecoverPartialReplaceRescuesTheReplacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		code syscall.Errno
	}{
		{name: "ERROR_UNABLE_TO_MOVE_REPLACEMENT", code: errorUnableToMoveReplacement},
		{name: "ERROR_UNABLE_TO_MOVE_REPLACEMENT_2", code: errorUnableToMoveReplacement2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, ".manifest.tmp")
			dst := filepath.Join(dir, "manifest.md")
			if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
				t.Fatalf("WriteFile src: %v", err)
			}
			// dst is deliberately absent: the documented post-failure state is that
			// Windows has already removed (1176) or renamed away (1177) the
			// destination while the replacement stayed put.

			err := recoverPartialReplace(tc.code, src, dst)
			if err == nil {
				t.Fatal("recoverPartialReplace must still report the failure; the destination's security descriptor was lost with the destination")
			}
			if !errors.Is(err, tc.code) {
				t.Fatalf("error = %v, want it to wrap %v", err, tc.code)
			}
			data, readErr := os.ReadFile(dst)
			if readErr != nil {
				t.Fatalf("the replacement was not recovered to the destination: %v", readErr)
			}
			if string(data) != "new" {
				t.Fatalf("destination content = %q, want the replacement bytes", data)
			}
			// The replacement must no longer sit at its temporary name, so the
			// caller's cleanup cannot delete the only copy of the new content.
			if _, err := os.Lstat(src); !os.IsNotExist(err) {
				t.Fatalf("the replacement should have been moved off its temporary name: %v", err)
			}
		})
	}
}

// TestRecoverPartialReplaceLeavesIntactStatesAlone covers the other half of the
// contract: for failures Microsoft documents as leaving "the replaced and
// replacement files retain their original file names", the destination still
// holds its original content, so the error must pass through untouched and the
// temporary file must stay put for the caller's cleanup to remove.
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
			if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
				t.Fatalf("WriteFile src: %v", err)
			}
			if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
				t.Fatalf("WriteFile dst: %v", err)
			}

			if err := recoverPartialReplace(tc.code, src, dst); !errors.Is(err, tc.code) {
				t.Fatalf("error = %v, want the original %v unchanged", err, tc.code)
			}
			if data, err := os.ReadFile(dst); err != nil || string(data) != "old" {
				t.Fatalf("destination = %q err=%v, want its original content untouched", data, err)
			}
			if _, err := os.Lstat(src); err != nil {
				t.Fatalf("the temporary file must stay put for the caller to clean up: %v", err)
			}
		})
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
