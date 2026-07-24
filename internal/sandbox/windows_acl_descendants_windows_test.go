//go:build windows

package sandbox

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// touchFile creates an empty file at path, failing the test on error.
func touchFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

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

// denyUsersWrite adds a direct (non-inheriting) deny-write ACE for
// BUILTIN\Users to path's DACL, overriding any inherited write grant from the
// enclosing t.TempDir() tree. Used to construct a directory that is
// genuinely non-writable at its own DACL, independent of whatever the test
// temp tree happens to inherit.
//
// Denies the concrete (non-generic) bits of windowsBroadenedWriteProbeMask —
// the same bits windowsDirGrantsBroadenedWrite itself checks, minus
// GENERIC_WRITE/GENERIC_ALL. Two things must NOT be in this mask:
//   - SYNCHRONIZE (part of windows.FILE_GENERIC_WRITE): the test process is
//     normally a member of BUILTIN\Users, and denying SYNCHRONIZE also blocks
//     its own later synchronous opens of path (e.g. os.ReadDir), not just
//     "write" — verified directly against this code path.
//   - Raw GENERIC_WRITE/GENERIC_ALL bits: stored unmapped in an ACE (as
//     opposed to being resolved to their constituent FILE_* bits first),
//     these were empirically observed to make Windows deny EVERY access,
//     including a plain FILE_LIST_DIRECTORY open, not just generic write.
func denyUsersWrite(t *testing.T, path string) {
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
	denyMask := windowsBroadenedWriteProbeMask &^ (windows.GENERIC_WRITE | windows.GENERIC_ALL | windows.SYNCHRONIZE)
	newDACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: denyMask,
		AccessMode:        windows.DENY_ACCESS,
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

// TestWindowsEnumerateWritableDescendantsDoesNotPruneSystemLookalikeTrees is
// the real-Windows regression for jatmn's review finding: a directory whose
// basename matched the old prune list (e.g. "Program Files") and was
// non-writable at its OWN DACL must still be descended into, because a
// non-writable root DACL says nothing about a writable descendant several
// levels down (e.g. an installer-created "Program Files\SomeApp" with a
// loosened grant). Before the fix, this subtree was silently certified clean
// from the root DACL alone and never scanned further.
func TestWindowsEnumerateWritableDescendantsDoesNotPruneSystemLookalikeTrees(t *testing.T) {
	root := t.TempDir()
	programFiles := mkdir(t, filepath.Join(root, "Program Files"))
	// Create and grant the nested descendant BEFORE denying write on
	// programFiles itself: the deny targets BUILTIN\Users, which the test
	// process is normally a member of, so denying it on programFiles first
	// would block the test process from creating anything under it.
	someApp := mkdir(t, filepath.Join(programFiles, "SomeApp"))
	grantUsersWrite(t, someApp)
	denyUsersWrite(t, programFiles)

	rootWritable, err := windowsDirGrantsBroadenedWrite(programFiles)
	if err != nil {
		t.Fatalf("windowsDirGrantsBroadenedWrite(programFiles): %v", err)
	}
	if rootWritable {
		t.Fatal("test fixture bug: programFiles must be non-writable at its own DACL to exercise the old prune condition")
	}

	found, err := windowsEnumerateWritableDescendants(root, nil)
	if err != nil {
		t.Fatalf("windowsEnumerateWritableDescendants: %v", err)
	}
	if !windowsPathListContains(found, someApp) {
		t.Fatalf("enumeration = %#v, want it to include writable descendant %q under non-writable %q (must not be pruned by basename)", found, someApp, programFiles)
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
	// Depth-3 writable child under non-writable ancestors: the scan must keep
	// descending through non-writable parents or this escape stays open.
	level1 := mkdir(t, filepath.Join(root, "locked1"))
	level2 := mkdir(t, filepath.Join(level1, "locked2"))
	deepWritable := mkdir(t, filepath.Join(level2, "deep-writable"))
	grantUsersWrite(t, deepWritable)
	plain := mkdir(t, filepath.Join(root, "plain"))
	workspace := mkdir(t, filepath.Join(root, "workspace"))
	grantUsersWrite(t, workspace)
	writableFile := touchFile(t, filepath.Join(root, "writable.txt"))
	grantUsersWrite(t, writableFile)

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
	if !windowsPathListContains(found, deepWritable) {
		t.Fatalf("enumeration = %#v, want it to include depth-3 writable child %q under non-writable ancestors", found, deepWritable)
	}
	if !windowsPathListContains(found, writableFile) {
		t.Fatalf("enumeration = %#v, want it to include writable file %q (a file is as much an escape surface as a directory)", found, writableFile)
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

// selfUserSID returns the current process token's own user SID, used by the
// canonical-root-scoping test below to deny itself directory-listing access
// (an owner always retains READ_CONTROL/WRITE_DAC implicitly, so this cannot
// lock the test out of restoring its own change).
func selfUserSID(t *testing.T) *windows.SID {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser: %v", err)
	}
	return user.User.Sid
}

// setSelfListDirectoryAccess grants or denies the current user FILE_LIST_DIRECTORY
// on path, restoring/breaking the ability to os.ReadDir it without touching
// READ_CONTROL/WRITE_DAC (which owners always retain), so the test can always
// undo its own change.
//
// deny=false restores access via SET_ACCESS with a zero mask rather than
// REVOKE_ACCESS: empirically, SetEntriesInAclW's REVOKE_ACCESS mode does not
// remove a pre-existing DENY ACE for the trustee (verified directly against
// this code path — see the same finding in BuildWindowsACLPlan's
// WindowsACLRevokeCapability, windows_acl_apply_windows.go), so relying on it
// here would leave the test process permanently denied FILE_LIST_DIRECTORY on
// its own temp fixture.
func setSelfListDirectoryAccess(t *testing.T, path string, deny bool) {
	t.Helper()
	mode := windows.ACCESS_MODE(windows.SET_ACCESS)
	permissions := windows.ACCESS_MASK(0)
	if deny {
		mode = windows.DENY_ACCESS
		permissions = windows.FILE_LIST_DIRECTORY
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo %s: %v", path, err)
	}
	oldDACL, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL %s: %v", path, err)
	}
	var targetOldDACL *windows.ACL = oldDACL
	if deny {
		targetOldDACL = nil
	}
	newDACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: permissions,
		AccessMode:        mode,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(selfUserSID(t)),
		},
	}}, targetOldDACL)
	if err != nil {
		t.Fatalf("ACLFromEntries %s: %v", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, newDACL, nil); err != nil {
		t.Fatalf("SetNamedSecurityInfo %s: %v", path, err)
	}
}

// TestWindowsEnumerateWritableDescendantsFailsClosedOnNonRootLookalikeSystemDir
// is the real-Windows regression for jatmn's review finding: a directory that
// shares a basename with a known SYSTEM-exclusive Windows directory (here
// "Recovery") but sits somewhere other than a genuine drive letter root must
// fail the scan closed when it cannot be listed, not be silently skipped as
// if it were the real, stock volume-root object.
func TestWindowsEnumerateWritableDescendantsFailsClosedOnNonRootLookalikeSystemDir(t *testing.T) {
	root := t.TempDir()
	outer := mkdir(t, filepath.Join(root, "outer"))
	lookalike := mkdir(t, filepath.Join(outer, "Recovery"))

	setSelfListDirectoryAccess(t, lookalike, true)
	t.Cleanup(func() { setSelfListDirectoryAccess(t, lookalike, false) })

	if _, err := os.ReadDir(lookalike); err == nil {
		t.Skip("could not deny self directory-listing access on this host; skipping fail-closed assertion")
	}

	_, err := windowsEnumerateWritableDescendants(root, nil)
	if err == nil {
		t.Fatal("windowsEnumerateWritableDescendants: expected a fail-closed error for an unlistable non-root-level lookalike system directory, got nil")
	}
}

// TestWindowsEnumerateWritableDescendantsFailsClosedOnEntryCap pins that
// exhausting the descendant entry budget is an error, not a silent partial
// success that would still let setup broaden the restricted token.
func TestWindowsEnumerateWritableDescendantsFailsClosedOnEntryCap(t *testing.T) {
	prev := windowsDescendantScanMaxDirs
	windowsDescendantScanMaxDirs = 3
	t.Cleanup(func() { windowsDescendantScanMaxDirs = prev })

	root := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d"} {
		mkdir(t, filepath.Join(root, name))
	}
	_, err := windowsEnumerateWritableDescendants(root, nil)
	if err == nil {
		t.Fatal("windowsEnumerateWritableDescendants: expected entry-cap error, got nil")
	}
}

// TestWindowsPathDeniesCapabilitySIDRoundTrip ensures the pre-broaden hole
// check can see a deny ACE that applyWindowsSharedDescendantDenies just wrote.
func TestWindowsPathDeniesCapabilitySIDRoundTrip(t *testing.T) {
	caps, err := LoadOrCreateWindowsCapabilitySIDs(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	root := t.TempDir()
	writable := mkdir(t, filepath.Join(root, "writable"))
	grantUsersWrite(t, writable)

	before, err := windowsPathDeniesCapabilitySID(writable, caps.ReadOnly)
	if err != nil {
		t.Fatalf("windowsPathDeniesCapabilitySID before: %v", err)
	}
	if before {
		t.Fatal("path already denies capability SID before apply")
	}
	if _, err := applyWindowsSharedDescendantDenies(root, caps.ReadOnly, nil); err != nil {
		t.Fatalf("applyWindowsSharedDescendantDenies: %v", err)
	}
	after, err := windowsPathDeniesCapabilitySID(writable, caps.ReadOnly)
	if err != nil {
		t.Fatalf("windowsPathDeniesCapabilitySID after: %v", err)
	}
	if !after {
		t.Fatal("path does not deny capability SID after apply")
	}
	holes, err := windowsUncoveredWritableDescendants(root, caps.ReadOnly, nil)
	if err != nil {
		t.Fatalf("windowsUncoveredWritableDescendants: %v", err)
	}
	if len(holes) != 0 {
		t.Fatalf("holes = %#v, want none after apply", holes)
	}
}

// TestWindowsPathDeniesCapabilitySIDIgnoresReadOnlyDeny is the real-Windows
// regression for jatmn's review finding: a pre-existing deny ACE for the
// stable capability SID that only denies read/execute (the exact shape
// planWindowsDenyReadPaths applies for a DenyRead path) must NOT be read as
// "write already denied." Before the fix, any deny ACE naming the SID short-
// circuited the check regardless of its mask, so a writable descendant that
// happened to sit under a DenyRead path would be skipped by
// applyWindowsSharedDescendantDenies and never get the write deny it needs.
func TestWindowsPathDeniesCapabilitySIDIgnoresReadOnlyDeny(t *testing.T) {
	caps, err := LoadOrCreateWindowsCapabilitySIDs(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	root := t.TempDir()
	writable := mkdir(t, filepath.Join(root, "writable"))
	grantUsersWrite(t, writable)

	// Apply the same DenyRead entry BuildWindowsACLPlan would generate for a
	// DenyRead path sharing this stable capability SID.
	if _, _, err := applyWindowsACLPathGroup(windowsACLPathGroup{
		Path: writable,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLDenyRead,
			Path:       writable,
			Capability: caps.ReadOnly,
		}},
	}); err != nil {
		t.Fatalf("apply DenyRead entry: %v", err)
	}

	deniesWrite, err := windowsPathDeniesCapabilitySID(writable, caps.ReadOnly)
	if err != nil {
		t.Fatalf("windowsPathDeniesCapabilitySID: %v", err)
	}
	if deniesWrite {
		t.Fatal("windowsPathDeniesCapabilitySID = true for a read-only deny ACE, want false: a DenyRead ACE does not block writes")
	}

	// The descendant-denial pass must therefore still add the real write deny
	// rather than skipping this path as already covered.
	if _, err := applyWindowsSharedDescendantDenies(root, caps.ReadOnly, nil); err != nil {
		t.Fatalf("applyWindowsSharedDescendantDenies: %v", err)
	}
	deniesWrite, err = windowsPathDeniesCapabilitySID(writable, caps.ReadOnly)
	if err != nil {
		t.Fatalf("windowsPathDeniesCapabilitySID after apply: %v", err)
	}
	if !deniesWrite {
		t.Fatal("windowsPathDeniesCapabilitySID = false after applyWindowsSharedDescendantDenies, want true: the write deny should now be present")
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
	// Checking only that the deny disappeared would also pass a rollback that
	// clobbered the original Users write ACE along with it; reassert the
	// pre-existing writable state is actually restored, not just any DACL.
	restoredWritable, err := windowsDirGrantsBroadenedWrite(writable)
	if err != nil {
		t.Fatalf("windowsDirGrantsBroadenedWrite after rollback: %v", err)
	}
	if !restoredWritable {
		t.Fatalf("rollback did not restore the original writable DACL on %q", writable)
	}
}

// TestWindowsAceSIDLocatesSIDInObjectACE pins the offset arithmetic
// windowsAceSID relies on for ACCESS_ALLOWED_OBJECT_ACE / ACCESS_DENIED_OBJECT_ACE:
// the real SID sits past a Flags DWORD and 0, 1, or 2 conditionally-present
// 16-byte GUIDs (ObjectType, InheritedObjectType), never at the plain-ACE
// SidStart offset GetAce's *ACCESS_ALLOWED_ACE typing would naively suggest.
// This builds the raw ACE bytes directly, per Microsoft's documented layout,
// because x/sys/windows has no AddAccessAllowedObjectAce binding to create a
// real one through the OS.
func TestWindowsAceSIDLocatesSIDInObjectACE(t *testing.T) {
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid: %v", err)
	}
	sidBytes := unsafe.Slice((*byte)(unsafe.Pointer(usersSID)), usersSID.Len())

	cases := []struct {
		name    string
		aceType byte
		flags   uint32
		guids   int // number of 16-byte GUIDs the flags say precede the SID
	}{
		{"no optional GUIDs", windowsAccessAllowedObjectAceType, 0, 0},
		{"object type GUID only", windowsAccessAllowedObjectAceType, windows.ACE_OBJECT_TYPE_PRESENT, 1},
		{"inherited type GUID only", windowsAccessDeniedObjectAceType, windows.ACE_INHERITED_OBJECT_TYPE_PRESENT, 1},
		{"both GUIDs", windowsAccessDeniedObjectAceType, windows.ACE_OBJECT_TYPE_PRESENT | windows.ACE_INHERITED_OBJECT_TYPE_PRESENT, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Layout: ACE_HEADER(4) + Mask(4) + Flags(4) + guids*GUID(16) + SID.
			buf := make([]byte, 4+4+4+16*tc.guids+len(sidBytes))
			buf[0] = tc.aceType                                // Header.AceType
			binary.LittleEndian.PutUint32(buf[8:12], tc.flags) // Flags, at the offset SidStart occupies in the plain-ACE layout
			copy(buf[12+16*tc.guids:], sidBytes)

			ace := (*windows.ACCESS_ALLOWED_ACE)(unsafe.Pointer(&buf[0]))
			sid, ok := windowsAceSID(ace)
			if !ok {
				t.Fatal("windowsAceSID returned ok=false for a recognized object ACE type")
			}
			if !sid.Equals(usersSID) {
				t.Fatalf("windowsAceSID = %s, want %s", sid.String(), usersSID.String())
			}
		})
	}
}

// TestWindowsAceSIDSkipsUnhandledAceTypes confirms an ACE type this scan does
// not model (audit, mandatory label, ...) is skipped rather than misread as a
// plain or object ACE — the same conservative behavior the code had before
// object-ACE support was added.
func TestWindowsAceSIDSkipsUnhandledAceTypes(t *testing.T) {
	const systemMandatoryLabelAceType = 0x11
	buf := make([]byte, 32)
	buf[0] = systemMandatoryLabelAceType
	ace := (*windows.ACCESS_ALLOWED_ACE)(unsafe.Pointer(&buf[0]))
	if _, ok := windowsAceSID(ace); ok {
		t.Fatal("windowsAceSID should return ok=false for an unhandled ACE type")
	}
}

func TestWindowsPathDeniesCapabilitySIDRequiresEssentialWriteMask(t *testing.T) {
	dir := t.TempDir()
	sid := "S-1-1-0"

	group := windowsACLPathGroup{
		Path: dir,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLDenyWrite,
			Path:       dir,
			Capability: sid,
		}},
	}
	if _, _, err := applyWindowsACLPathGroup(group); err != nil {
		t.Fatal(err)
	}

	denied, err := windowsPathDeniesCapabilitySID(dir, sid)
	if err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("expected full DenyWrite to satisfy windowsPathDeniesCapabilitySID")
	}
}

func TestWindowsEnsureSharedDescendantCoverageDeduplicatesRootDeny(t *testing.T) {
	dir := t.TempDir()
	sid := "S-1-1-0"

	group := windowsACLPathGroup{
		Path: dir,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLDenyWrite,
			Path:       dir,
			Capability: sid,
		}},
	}
	if _, _, err := applyWindowsACLPathGroup(group); err != nil {
		t.Fatal(err)
	}

	denied, err := windowsPathDeniesCapabilitySID(dir, sid)
	if err != nil || !denied {
		t.Fatalf("expected root to be denied before re-check, denied=%v, err=%v", denied, err)
	}
}
