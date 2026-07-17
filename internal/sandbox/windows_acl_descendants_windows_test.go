//go:build windows

package sandbox

import (
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dirDeniesSID reports whether path's DACL carries a deny ACE naming the given
// string SID. It reads the same way the descendant scan applies denies, so a
// test can confirm the compensating deny actually landed (and, after rollback,
// is gone) using the real Win32 ACL APIs on a test-owned temp tree.
func dirDeniesSID(t *testing.T, path, wantSID string) bool {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo %s: %v", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL %s: %v", path, err)
	}
	if dacl == nil {
		return false
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatalf("GetAce %d of %s: %v", index, path, err)
		}
		if ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.String() == wantSID {
			return true
		}
	}
	return false
}

// grantUsersWrite adds a direct (non-inheriting) allow-write ACE for
// BUILTIN\Users to path's DACL using the real Win32 ACL APIs. The test process
// owns the t.TempDir() tree, so this needs no elevation.
func grantUsersWrite(t *testing.T, path string) {
	t.Helper()
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(Users): %v", err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo %s: %v", path, err)
	}
	oldDACL, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL %s: %v", path, err)
	}
	newDACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.FILE_GENERIC_WRITE,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(usersSID),
		},
	}}, oldDACL)
	if err != nil {
		t.Fatalf("ACLFromEntries %s: %v", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, newDACL, nil); err != nil {
		t.Fatalf("SetNamedSecurityInfo %s: %v", path, err)
	}
}

// TestWindowsDirGrantsBroadenedWriteDetectsUsersWrite pins the DACL probe the
// descendant scan relies on: a directory whose DACL grants BUILTIN\Users write
// is reported writable; one that does not is reported not writable.
func TestWindowsDirGrantsBroadenedWriteDetectsUsersWrite(t *testing.T) {
	root := t.TempDir()
	writable := mkdir(t, filepath.Join(root, "writable"))
	grantUsersWrite(t, writable)
	plain := mkdir(t, filepath.Join(root, "plain"))

	got, err := windowsDirGrantsBroadenedWrite(writable)
	if err != nil {
		t.Fatalf("windowsDirGrantsBroadenedWrite(writable): %v", err)
	}
	if !got {
		t.Fatalf("windowsDirGrantsBroadenedWrite(writable) = false, want true")
	}

	plainWritable, err := windowsDirGrantsBroadenedWrite(plain)
	if err != nil {
		t.Fatalf("windowsDirGrantsBroadenedWrite(plain): %v", err)
	}
	if plainWritable {
		t.Skip("test temp tree grants BUILTIN\\Users write by inheritance; cannot exercise the negative case here")
	}
}

// TestWindowsEnumerateWritableDescendantsFindsExistingWritableChildren is the
// real-Windows regression for the write-jail gap the reviewer flagged: an
// existing writable descendant of a shared root (including one nested under
// another writable directory) must be discovered so it can be denied directly,
// and a configured write root must be excluded so legitimate workspace writes
// are never jailed.
func TestWindowsEnumerateWritableDescendantsFindsExistingWritableChildren(t *testing.T) {
	root := t.TempDir()
	outer := mkdir(t, filepath.Join(root, "outer"))
	grantUsersWrite(t, outer)
	inner := mkdir(t, filepath.Join(outer, "inner"))
	grantUsersWrite(t, inner)
	plain := mkdir(t, filepath.Join(root, "plain"))
	workspace := mkdir(t, filepath.Join(root, "workspace"))
	grantUsersWrite(t, workspace)

	found, err := windowsEnumerateWritableDescendants(root, nil)
	if err != nil {
		t.Fatalf("windowsEnumerateWritableDescendants: %v", err)
	}
	if !windowsPathListContains(found, outer) {
		t.Fatalf("enumeration = %#v, want it to include writable child %q", found, outer)
	}
	if !windowsPathListContains(found, inner) {
		t.Fatalf("enumeration = %#v, want it to include nested writable descendant %q", found, inner)
	}

	plainWritable, err := windowsDirGrantsBroadenedWrite(plain)
	if err != nil {
		t.Fatalf("windowsDirGrantsBroadenedWrite(plain): %v", err)
	}
	if !plainWritable && windowsPathListContains(found, plain) {
		t.Fatalf("enumeration = %#v, want it to exclude non-writable child %q", found, plain)
	}

	// Excluding the workspace write root (and its subtree) must drop it from the
	// result even though it grants Users write.
	excluded, err := windowsEnumerateWritableDescendants(root, []string{workspace})
	if err != nil {
		t.Fatalf("windowsEnumerateWritableDescendants(exclude): %v", err)
	}
	if windowsPathListContains(excluded, workspace) {
		t.Fatalf("enumeration = %#v, want it to exclude the configured write root %q", excluded, workspace)
	}
	if !windowsPathListContains(excluded, outer) {
		t.Fatalf("enumeration = %#v, want it to still include %q when a different path is excluded", excluded, outer)
	}
}

// TestApplyWindowsSharedDescendantDeniesAppliesAndRollsBack proves the
// enforcement half of the fix end to end on real ACLs: a writable descendant of
// a shared root gets a direct deny ACE for the read-only capability SID (the SID
// every broadened token carries), and the returned rollback restores the DACL.
// This runs unprivileged because it operates only on the test-owned temp tree.
func TestApplyWindowsSharedDescendantDeniesAppliesAndRollsBack(t *testing.T) {
	caps, err := LoadOrCreateWindowsCapabilitySIDs(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	root := t.TempDir()
	writable := mkdir(t, filepath.Join(root, "writable"))
	grantUsersWrite(t, writable)

	if dirDeniesSID(t, writable, caps.ReadOnly) {
		t.Fatalf("descendant already denies %q before apply", caps.ReadOnly)
	}
	snapshots, err := applyWindowsSharedDescendantDenies(root, caps.ReadOnly, nil)
	if err != nil {
		t.Fatalf("applyWindowsSharedDescendantDenies: %v", err)
	}
	if len(snapshots) == 0 {
		t.Fatalf("apply returned no snapshots; the writable descendant was never denied")
	}
	if !dirDeniesSID(t, writable, caps.ReadOnly) {
		t.Fatalf("descendant %q does not deny %q after apply", writable, caps.ReadOnly)
	}

	if err := rollbackWindowsACLSnapshots(snapshots); err != nil {
		t.Fatalf("rollbackWindowsACLSnapshots: %v", err)
	}
	if dirDeniesSID(t, writable, caps.ReadOnly) {
		t.Fatalf("descendant %q still denies %q after rollback", writable, caps.ReadOnly)
	}
}
