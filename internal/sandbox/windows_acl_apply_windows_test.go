//go:build windows

package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A workspace-owned target (no elevation needed: the current user holds WRITE_DAC
// on its own temp dir) must apply and roll back through the handle-based path.
// This exercises the whole open -> GetSecurityInfo -> SetSecurityInfo -> close
// sequence and the re-open rollback that replaced the pathname-based calls in
// #728, so a regression that reintroduced GetNamedSecurityInfo/SetNamedSecurityInfo
// (or broke the handle plumbing) would fail here.
func TestApplyWindowsACLPathGroupHandleBasedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	group := windowsACLPathGroup{
		Path: dir,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLAllowWrite,
			Path:       dir,
			Capability: "S-1-1-0", // Everyone: a well-known, StringToSid-parseable group SID
		}},
	}

	snapshot, applied, err := applyWindowsACLPathGroup(group)
	if err != nil {
		t.Fatalf("applyWindowsACLPathGroup: %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true for an existing directory target")
	}
	if snapshot.Path != dir || snapshot.Materialized {
		t.Fatalf("snapshot = %#v, want Path=%q Materialized=false", snapshot, dir)
	}
	if snapshot.Descriptor == nil {
		t.Fatal("snapshot has no captured descriptor to roll back to")
	}
	if err := rollbackWindowsACLSnapshots([]windowsACLSnapshot{snapshot}); err != nil {
		t.Fatalf("rollbackWindowsACLSnapshots: %v", err)
	}
}

// TestApplyWindowsACLPathGroupRevokeCapabilityRemovesStaleDeny is the
// real-Windows regression for jatmn's P2 finding: promoting a path to an
// allowed write root must also remove a stale deny ACE an earlier setup
// round left there for the stable capability SID, not merely omit it from
// this plan. Without the fix, applyWindowsACLPlan's SetEntriesInAcl-based
// merge only touches trustees actually named in the new entry list, so an
// old DenyWrite ACE for a SID the new plan does not mention would survive
// and keep winning over the new Allow under deny-before-allow evaluation.
func TestApplyWindowsACLPathGroupRevokeCapabilityRemovesStaleDeny(t *testing.T) {
	// The stale/allow SIDs must be synthetic identities the test process itself
	// is not a member of (exactly like the real stable capability SIDs
	// LoadOrCreateWindowsCapabilitySIDs mints): a WindowsACLDenyWrite mask
	// includes WRITE_DAC/WRITE_OWNER/DELETE, so denying a well-known group the
	// test process actually belongs to (e.g. Everyone, BUILTIN\Users) would
	// lock the test out of managing — and t.TempDir() out of cleaning up —
	// its own fixture.
	caps, err := LoadOrCreateWindowsCapabilitySIDs(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	otherCaps, err := LoadOrCreateWindowsCapabilitySIDs(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs (other): %v", err)
	}
	staleSID := caps.ReadOnly
	allowSID := otherCaps.ReadOnly

	dir := t.TempDir()
	// Simulate the stale deny an earlier setup round applied while this path
	// was still covered by the shared-root/descendant DenyWrite mitigation.
	if _, _, err := applyWindowsACLPathGroup(windowsACLPathGroup{
		Path: dir,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLDenyWrite,
			Path:       dir,
			Capability: staleSID,
			NoInherit:  true,
		}},
	}); err != nil {
		t.Fatalf("apply stale deny: %v", err)
	}
	if !dirDeniesSID(t, dir, staleSID) {
		t.Fatalf("test fixture bug: %q does not carry the stale deny it is supposed to", dir)
	}

	// Now promote dir to a write root: the plan carries an Allow for a
	// different SID plus the reconciling revoke for the stale one.
	if _, _, err := applyWindowsACLPathGroup(windowsACLPathGroup{
		Path: dir,
		Entries: []WindowsACLEntry{
			{Action: WindowsACLAllowWrite, Path: dir, Capability: allowSID},
			{Action: WindowsACLRevokeCapability, Path: dir, Capability: staleSID, NoInherit: true},
		},
	}); err != nil {
		t.Fatalf("apply promotion to write root: %v", err)
	}
	if dirDeniesSID(t, dir, staleSID) {
		t.Fatalf("%q still carries the stale deny for %q after promotion to a write root", dir, staleSID)
	}
}

// TestApplyWindowsACLRevokePreservesDenyRead pins that migration revoke for a
// promoted write root removes only the experimental DenyWrite ACE for the
// stable SID and leaves a co-resident DenyRead for the same SID intact — so a
// concurrent profile's read boundary is not deleted (jatmn P1).
func TestApplyWindowsACLRevokePreservesDenyRead(t *testing.T) {
	// Synthetic capability SID (not a group this process is in) so DenyWrite's
	// WRITE_DAC/DELETE bits do not lock the test out of its own temp dir.
	caps, err := LoadOrCreateWindowsCapabilitySIDs(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	sid := caps.ReadOnly
	dir := t.TempDir()

	// Profile A shape plus experimental broadening: both DenyRead and full
	// DenyWrite for the same stable SID on one path (two ACEs in one apply so
	// a second DENY_ACCESS merge cannot replace the first).
	if _, _, err := applyWindowsACLPathGroup(windowsACLPathGroup{
		Path: dir,
		Entries: []WindowsACLEntry{
			{
				Action:     WindowsACLDenyRead,
				Path:       dir,
				Capability: sid,
				NoInherit:  true,
			},
			{
				Action:     WindowsACLDenyWrite,
				Path:       dir,
				Capability: sid,
				NoInherit:  true,
			},
		},
	}); err != nil {
		t.Fatalf("apply DenyRead+DenyWrite: %v", err)
	}
	writeDenied, err := windowsPathDeniesCapabilitySID(dir, sid)
	if err != nil {
		t.Fatalf("windowsPathDeniesCapabilitySID before: %v", err)
	}
	if !writeDenied {
		t.Fatal("fixture: expected full write deny present before revoke")
	}
	if !dirDeniesReadSID(t, dir, sid) {
		t.Fatal("fixture: expected read deny present before revoke")
	}

	// Profile B promotes dir to a write root: revoke stale write deny only.
	if _, _, err := applyWindowsACLPathGroup(windowsACLPathGroup{
		Path: dir,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLRevokeCapability,
			Path:       dir,
			Capability: sid,
			NoInherit:  true,
		}},
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	writeDenied, err = windowsPathDeniesCapabilitySID(dir, sid)
	if err != nil {
		t.Fatalf("windowsPathDeniesCapabilitySID after: %v", err)
	}
	if writeDenied {
		t.Fatal("write deny for SID still present after migration revoke")
	}
	if !dirDeniesReadSID(t, dir, sid) {
		t.Fatal("DenyRead for same SID was removed by migration revoke; read boundary must be preserved")
	}
}

// TestApplyWindowsACLPlanRevokeDescendantsClearsChildDeny is the regression for
// the RevokeDescendants walk: promoting a root to a write root must clear a
// stale direct deny an earlier run left on an existing child, not only the root
// path itself. Also pins reparse skip and the depth cap as best-effort bounds.
func TestApplyWindowsACLPlanRevokeDescendantsClearsChildDeny(t *testing.T) {
	caps, err := LoadOrCreateWindowsCapabilitySIDs(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	staleSID := caps.ReadOnly
	allowSID, err := LoadOrCreateWindowsCapabilitySIDs(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs (allow): %v", err)
	}

	root := t.TempDir()
	child := mkdir(t, filepath.Join(root, "child"))
	deep := mkdir(t, filepath.Join(child, "deep"))
	// Stale full DenyWrite on the direct child (the primary cleanup target).
	if _, _, err := applyWindowsACLPathGroup(windowsACLPathGroup{
		Path: child,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLDenyWrite,
			Path:       child,
			Capability: staleSID,
			NoInherit:  true,
		}},
	}); err != nil {
		t.Fatalf("apply stale deny on child: %v", err)
	}
	// Partial write deny on deep: the old complete-coverage pre-check would
	// skip this; unconditional revoke must still clear it.
	denyCapabilityMask(t, deep, staleSID, windows.FILE_WRITE_ATTRIBUTES)
	if !dirDeniesSID(t, child, staleSID) {
		t.Fatal("fixture: child missing full stale deny")
	}
	if denyACECountForSID(t, deep, staleSID) == 0 {
		t.Fatal("fixture: deep missing partial stale deny")
	}

	// Junction under root: walker must skip it (best-effort) without failing.
	juncTarget := mkdir(t, filepath.Join(t.TempDir(), "junc-target"))
	junc := filepath.Join(root, "junc")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junc, juncTarget).CombinedOutput(); err != nil {
		t.Logf("skipping junction sub-check (mklink /J unavailable): %v %s", err, strings.TrimSpace(string(out)))
	} else {
		t.Cleanup(func() { _ = os.Remove(junc) })
		if !windowsPathIsReparsePoint(junc) {
			t.Fatalf("fixture: %q is not a reparse point", junc)
		}
	}

	cleanup, err := applyWindowsACLPlan(WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLAllowWrite, Path: root, Capability: allowSID.ReadOnly},
		{
			Action:            WindowsACLRevokeCapability,
			Path:              root,
			Capability:        staleSID,
			NoInherit:         true,
			RevokeDescendants: true,
		},
	}})
	if err != nil {
		t.Fatalf("applyWindowsACLPlan: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })

	if dirDeniesSID(t, child, staleSID) {
		t.Fatalf("%q still carries full stale deny after RevokeDescendants", child)
	}
	if denyACECountForSID(t, deep, staleSID) != 0 {
		t.Fatalf("%q still carries partial stale deny after RevokeDescendants", deep)
	}

	// Depth cap: with max depth 1 the walker revokes root's children but does
	// not enqueue them, so a nested deny under a new root is left in place.
	cappedRoot := t.TempDir()
	cappedChild := mkdir(t, filepath.Join(cappedRoot, "level1"))
	cappedDeep := mkdir(t, filepath.Join(cappedChild, "level2"))
	if _, _, err := applyWindowsACLPathGroup(windowsACLPathGroup{
		Path: cappedDeep,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLDenyWrite,
			Path:       cappedDeep,
			Capability: staleSID,
			NoInherit:  true,
		}},
	}); err != nil {
		t.Fatalf("apply stale deny on capped deep: %v", err)
	}
	oldDepth := windowsDescendantScanMaxDepth
	windowsDescendantScanMaxDepth = 1
	t.Cleanup(func() { windowsDescendantScanMaxDepth = oldDepth })
	if _, err := applyWindowsACLPlan(WindowsACLPlan{Entries: []WindowsACLEntry{{
		Action:            WindowsACLRevokeCapability,
		Path:              cappedRoot,
		Capability:        staleSID,
		NoInherit:         true,
		RevokeDescendants: true,
	}}}); err != nil {
		t.Fatalf("applyWindowsACLPlan (depth cap): %v", err)
	}
	if !dirDeniesSID(t, cappedDeep, staleSID) {
		t.Fatal("depth cap should leave level2 deny in place when max depth is 1")
	}
}

// dirDeniesReadSID reports whether path's DACL has a DENY ACE for wantSID whose
// mask covers FILE_GENERIC_READ (DenyRead shape) without the full write-probe
// mask of experimental DenyWrite.
func dirDeniesReadSID(t *testing.T, path, wantSID string) bool {
	t.Helper()
	want, err := windows.StringToSid(wantSID)
	if err != nil {
		t.Fatalf("StringToSid %q: %v", wantSID, err)
	}
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
	_, readMask, err := windowsACLAccess(WindowsACLDenyRead)
	if err != nil {
		t.Fatalf("windowsACLAccess DenyRead: %v", err)
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatalf("GetAce %d of %s: %v", index, path, err)
		}
		if ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE && ace.Header.AceType != windowsAccessDeniedObjectAceType {
			continue
		}
		sid, ok := windowsAceSID(ace)
		if !ok || !sid.Equals(want) {
			continue
		}
		if ace.Mask&readMask == readMask && !windowsIsExperimentalWriteDenyMask(ace.Mask) {
			return true
		}
	}
	return false
}

// A materialized target that does not exist yet is created, ACL'd through the
// handle, and removed on rollback.
func TestApplyWindowsACLPathGroupMaterializes(t *testing.T) {
	target := filepath.Join(t.TempDir(), "created")
	group := windowsACLPathGroup{
		Path:        target,
		Materialize: true,
		Entries: []WindowsACLEntry{{
			Action:      WindowsACLDenyRead,
			Path:        target,
			Capability:  "S-1-1-0",
			Materialize: true,
		}},
	}

	snapshot, applied, err := applyWindowsACLPathGroup(group)
	if err != nil {
		t.Fatalf("applyWindowsACLPathGroup: %v", err)
	}
	if !applied || !snapshot.Materialized {
		t.Fatalf("applied=%v materialized=%v, want both true", applied, snapshot.Materialized)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("materialized target not created: %v", err)
	}
	if err := rollbackWindowsACLSnapshots([]windowsACLSnapshot{snapshot}); err != nil {
		t.Fatalf("rollbackWindowsACLSnapshots: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("materialized target still present after rollback: stat err = %v", err)
	}
}

// The core #728 guard: a target that resolves to a reparse point (symlink /
// junction) is refused rather than followed, so a swapped-in link during elevated
// setup cannot redirect the ACL change onto a system object.
func TestOpenWindowsACLTargetRejectsReparsePoint(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	// Prefer a junction: unlike a symlink it needs no admin/Developer Mode, so
	// this guard actually runs in CI. A junction is a directory reparse point,
	// exactly the swap vector openWindowsACLTarget must refuse to follow. Fall
	// back to a symlink and skip only if neither reparse form can be created.
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, realDir).CombinedOutput(); err != nil {
		if serr := os.Symlink(realDir, link); serr != nil {
			t.Skipf("cannot create a reparse point (junction: %v %q; symlink: %v)", err, strings.TrimSpace(string(out)), serr)
		}
	}
	handle, _, err := openWindowsACLTarget(link)
	if err == nil {
		_ = windows.CloseHandle(handle)
		t.Fatal("openWindowsACLTarget followed a reparse point, want rejection")
	}
	if !strings.Contains(err.Error(), "reparse-point") {
		t.Fatalf("openWindowsACLTarget(symlink) err = %v, want a reparse-point rejection", err)
	}
}

// A missing target surfaces as os.ErrNotExist so the caller's materialize path
// still fires (a real open error, e.g. reparse rejection, must NOT look missing).
func TestOpenWindowsACLTargetMissingIsNotExist(t *testing.T) {
	_, _, err := openWindowsACLTarget(filepath.Join(t.TempDir(), "does-not-exist"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("openWindowsACLTarget(missing) err = %v, want os.ErrNotExist", err)
	}
}

// isDir is read from the same handle used for the ACL ops, not a separate Stat.
func TestOpenWindowsACLTargetReportsIsDir(t *testing.T) {
	dir := t.TempDir()
	handle, isDir, err := openWindowsACLTarget(dir)
	if err != nil {
		t.Fatalf("openWindowsACLTarget(dir): %v", err)
	}
	_ = windows.CloseHandle(handle)
	if !isDir {
		t.Fatal("isDir = false for a directory target, want true")
	}

	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	handle, isDir, err = openWindowsACLTarget(file)
	if err != nil {
		t.Fatalf("openWindowsACLTarget(file): %v", err)
	}
	_ = windows.CloseHandle(handle)
	if isDir {
		t.Fatal("isDir = true for a regular file, want false")
	}
}
