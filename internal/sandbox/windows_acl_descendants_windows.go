//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The shared-root compensating deny (windows_acl.go) puts a direct,
// non-inheriting DenyWrite on each of C:\, %ProgramData%, %SystemRoot%\Temp,
// and C:\Users\Public. That blocks new writes directly under those objects, but
// a Windows access check for an EXISTING child never evaluates a non-inherited
// ACE on the child's parent, so a pre-existing descendant that independently
// grants BUILTIN\Users or Authenticated Users write stays writable once the
// elevated fully-restricted (DenyRead) token is broadened with those two groups,
// a write outside every configured write root. This file enumerates those
// existing writable descendants and denies each one directly.
//
// Coverage rules (fail closed):
//
//   - Every directory under a shared root is considered for descent within the
//     depth/entry caps, whether or not the parent itself is Users-writable. A
//     depth-N writable child under non-writable ancestors is a real escape and
//     must be found (see CodeRabbit/jatmn review).
//   - Hitting windowsDescendantScanMaxDepth or windowsDescendantScanMaxDirs
//     means unexamined territory remains: the scan returns an error so setup
//     and pre-broaden revalidation cannot certify a partial walk as clean.
//   - Reparse points (junctions, symlinks, volume mount points) are fail-closed:
//     sandboxed access can follow them, but denying/descending them risks
//     touching unrelated trees or other volumes. Incomplete coverage of a
//     reparse target aborts broadening rather than pretending the tree is safe.
//   - A directory this process cannot list, or a child whose DACL it cannot
//     read, is fail-closed UNLESS the basename is a known SYSTEM-exclusive
//     Windows directory (e.g. "System Volume Information") AND it sits at the
//     one place that basename is ever legitimately the real thing: directly
//     under an actual drive letter root (windowsPathIsDriveRootPath). Without
//     that allowlist, elevated setup would fail on every real machine; without
//     the root-level scoping, a same-named directory anywhere else in the
//     tree (nested under ProgramData or Public, whether by installer accident
//     or deliberately) would be silently skipped instead of failing closed —
//     see jatmn's review.
//   - Stock system trees under the drive root (Windows, Program Files, ...)
//     are NOT pruned by basename: a directory's own DACL being non-writable
//     says nothing about whether an installer-created descendant several
//     levels down independently grants Users/AuthUsers write, so certifying
//     a subtree clean from its root DACL alone would miss exactly that
//     escape (see jatmn's review). Every directory is descended subject only
//     to the depth/entry caps above; exhausting those caps on a genuinely
//     huge stock tree is a fail-closed error, not a silent partial pass.
//
// Staleness: setup alone is not enough. Non-inheriting denies only cover the
// filesystem state at apply time. The elevated command runner revalidates and
// reapplies this scan immediately before broadening the restricted token
// (windowsEnsureSharedDescendantCoverage); if coverage cannot be re-established,
// the token stays on the narrow SID set.
//
// Basename policies live in windows_acl_descendants.go so non-Windows tests can
// pin them without Win32. Bounds are vars so Windows tests can lower them.
var (
	windowsDescendantScanMaxDepth = 24
	windowsDescendantScanMaxDirs  = 8192
)

// windowsBroadenedWriteProbeMask is the set of access-mask bits that let a
// principal create, delete, or modify content, attributes, or extended
// attributes in (or the security of) a directory, i.e. the bits that make a
// directory a usable write-jail escape. FILE_WRITE_DATA is FILE_ADD_FILE and
// FILE_APPEND_DATA is FILE_ADD_SUBDIRECTORY for a directory object.
const windowsBroadenedWriteProbeMask windows.ACCESS_MASK = windows.FILE_WRITE_DATA |
	windows.FILE_APPEND_DATA |
	windows.FILE_WRITE_ATTRIBUTES |
	windows.FILE_WRITE_EA |
	windowsFileDeleteChild |
	windows.DELETE |
	windows.WRITE_DAC |
	windows.WRITE_OWNER |
	windows.GENERIC_WRITE |
	windows.GENERIC_ALL

// applyWindowsSharedDescendantDenies enumerates the existing writable
// descendants of a shared root and applies a direct, non-inheriting DenyWrite
// (naming denySID, the same stable read-only capability SID the root deny uses)
// to each. It returns every snapshot it applied (including on error) so the
// caller can roll the whole apply back. A descendant it identified as writable
// but could not deny is a hole it cannot close, so that failure is returned
// (fail closed). An incomplete enumeration (caps, reparse, unreadable child)
// is also returned as an error. Descendants that already carry an equivalent
// deny for denySID are left untouched so setup reruns and command-time
// revalidation do not accumulate duplicate permanent ACEs.
func applyWindowsSharedDescendantDenies(root, denySID string, writeRoots []string) ([]windowsACLSnapshot, error) {
	descendants, err := windowsEnumerateWritableDescendants(root, writeRoots)
	if err != nil {
		return nil, fmt.Errorf("enumerate writable descendants of %s: %w", root, err)
	}
	snapshots := make([]windowsACLSnapshot, 0, len(descendants))
	for _, dir := range descendants {
		denied, err := windowsPathDeniesCapabilitySID(dir, denySID)
		if err != nil {
			return snapshots, fmt.Errorf("inspect existing deny on %s: %w", dir, err)
		}
		if denied {
			continue
		}
		snapshot, applied, err := applyWindowsACLPathGroup(windowsACLPathGroup{
			Path: dir,
			Entries: []WindowsACLEntry{{
				Action:     WindowsACLDenyWrite,
				Path:       dir,
				Capability: denySID,
				NoInherit:  true,
			}},
		})
		if err != nil {
			return snapshots, fmt.Errorf("deny writable descendant %s: %w", dir, err)
		}
		if applied {
			snapshots = append(snapshots, snapshot)
		}
	}
	return snapshots, nil
}

// windowsEnumerateWritableDescendants returns the existing files and
// directories below root that grant BUILTIN\Users or Authenticated Users
// write, excluding any configured write root (and anything under it) so
// legitimate workspace writes are never jailed. Files are checked and denied
// just like directories — a writable file directly under a shared root is as
// much an escape surface as a writable directory — but only directories are
// descended into.
//
// Fail closed: exhausting the depth or entry caps, encountering a reparse
// point, or failing to list/inspect a non-allowlisted entry returns an error
// rather than a partial success the caller could mistake for complete coverage.
func windowsEnumerateWritableDescendants(root string, writeRoots []string) ([]string, error) {
	if windowsCapabilityPathKey(root) == "" {
		return nil, nil
	}
	excluded := make([]string, 0, len(writeRoots))
	for _, writeRoot := range writeRoots {
		if key := windowsCapabilityPathKey(writeRoot); key != "" {
			excluded = append(excluded, key)
		}
	}
	isExcluded := func(key string) bool {
		for _, prefix := range excluded {
			if key == prefix || strings.HasPrefix(key, prefix+`\`) {
				return true
			}
		}
		return false
	}

	type node struct {
		path  string
		depth int
	}
	var out []string
	visited := 0
	queue := []node{{path: root, depth: 0}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(current.path)
		if err != nil {
			// The system-locked basename allowlist only applies to the real
			// thing: a canonical root-level system directory directly under an
			// actual drive letter root. A same-named directory anywhere else in
			// the tree (e.g. nested under ProgramData or Public) is not the
			// stock SYSTEM-exclusive object and must fail closed instead of
			// being silently skipped — see jatmn's review.
			if windowsPathIsDriveRootPath(filepath.Dir(current.path)) && windowsDescendantScanNameIsSystemLocked(filepath.Base(current.path)) {
				continue
			}
			return nil, fmt.Errorf("list descendants of %s: %w", current.path, err)
		}
		for _, entry := range entries {
			child := filepath.Join(current.path, entry.Name())
			childKey := windowsCapabilityPathKey(child)
			if isExcluded(childKey) {
				continue
			}
			if windowsPathIsReparsePoint(child) {
				return nil, fmt.Errorf("descendant scan hit reparse point %s under %s; cannot establish coverage for its target", child, root)
			}
			if visited >= windowsDescendantScanMaxDirs {
				return nil, fmt.Errorf("descendant scan exceeded %d entries below %s", windowsDescendantScanMaxDirs, root)
			}
			visited++
			writable, err := windowsDirGrantsBroadenedWrite(child)
			if err != nil {
				// Same canonical-root-level scoping as the ReadDir case above:
				// current.path (child's parent) must itself be a drive root for
				// this to be the real, SYSTEM-exclusive directory.
				if windowsPathIsDriveRootPath(current.path) && windowsDescendantScanNameIsSystemLocked(entry.Name()) {
					continue
				}
				return nil, fmt.Errorf("inspect DACL of %s: %w", child, err)
			}
			if writable {
				out = append(out, child)
			}
			if !entry.IsDir() {
				continue
			}
			childDepth := current.depth + 1
			if childDepth >= windowsDescendantScanMaxDepth {
				// A directory at the depth cap may still have unexamined
				// children. Fail closed rather than pretend the subtree is clean.
				// Leaf files at this depth were already inspected above.
				// Only fail when we would have needed to descend further: always
				// report the cap so callers cannot certify "complete".
				return nil, fmt.Errorf("descendant scan exceeded depth %d at %s", windowsDescendantScanMaxDepth, child)
			}
			// Always descend (subject to caps), including through non-writable
			// ancestors and stock system trees (Windows, Program Files, ...), so
			// a deep writable child is not missed. A non-writable directory's OWN
			// DACL says nothing about a descendant several levels down: an
			// installer-created child with a loosened, non-inherited grant (e.g.
			// C:\Users\shared) is exactly the escape this scan exists to find, and
			// certifying a subtree clean from its root DACL alone would miss it
			// (see jatmn's review). There is deliberately no basename-based
			// shortcut here anymore — hitting windowsDescendantScanMaxDepth or
			// windowsDescendantScanMaxDirs on a genuinely huge stock tree fails
			// the scan closed (see the caller), which keeps the narrow SID set
			// rather than certifying an unexamined subtree as safe.
			queue = append(queue, node{path: child, depth: childDepth})
		}
	}
	return out, nil
}

// windowsAccessAllowedObjectAceType and windowsAccessDeniedObjectAceType are
// the AceType values for ACCESS_ALLOWED_OBJECT_ACE / ACCESS_DENIED_OBJECT_ACE
// (https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-access_allowed_object_ace).
// x/sys/windows only models the plain ACCESS_ALLOWED_ACE layout (Header, Mask,
// SidStart) and exposes just ACCESS_ALLOWED_ACE_TYPE/ACCESS_DENIED_ACE_TYPE, so
// these two are declared locally.
const (
	windowsAccessAllowedObjectAceType = 0x05
	windowsAccessDeniedObjectAceType  = 0x06
)

// windowsAceSID locates the trustee SID within ace, an *ACCESS_ALLOWED_ACE
// pointer that GetAce hands back regardless of the ACE's true type — for
// object ACEs that pointer is only valid for reading Header/Mask, not
// SidStart. An object ACE (ACCESS_ALLOWED_OBJECT_ACE / ACCESS_DENIED_OBJECT_ACE)
// inserts a Flags DWORD and up to two conditionally-present 16-byte GUIDs
// (ObjectType, InheritedObjectType) between Mask and the real SID; naively
// reading &ace.SidStart for one of these — as if it had the plain ACE layout —
// reinterprets Flags/GUID bytes as SID bytes and silently computes the wrong
// trustee, both risking a false match and missing a real Users/Authenticated
// Users grant hidden inside an object ACE. ok is false for any other ACE type
// (audit, alarm, mandatory label, compound, ...), which does not represent a
// trustee write grant in the sense this scan cares about and is skipped
// exactly as it always has been.
func windowsAceSID(ace *windows.ACCESS_ALLOWED_ACE) (sid *windows.SID, ok bool) {
	switch ace.Header.AceType {
	case windows.ACCESS_ALLOWED_ACE_TYPE, windows.ACCESS_DENIED_ACE_TYPE:
		return (*windows.SID)(unsafe.Pointer(&ace.SidStart)), true
	case windowsAccessAllowedObjectAceType, windowsAccessDeniedObjectAceType:
		// For an object ACE, the memory the Go struct calls SidStart is
		// actually the ACE's Flags DWORD; the real SID sits further out,
		// pushed by whichever of the two optional GUIDs Flags says are present.
		// offset is plain arithmetic on a byte count, never itself derived from
		// a pointer conversion, so accumulating it across statements is safe;
		// only the final pointer+offset conversion below needs to happen in a
		// single expression (go vet's unsafeptr rule).
		flags := ace.SidStart
		offset := unsafe.Sizeof(ace.SidStart)
		if flags&windows.ACE_OBJECT_TYPE_PRESENT != 0 {
			offset += 16
		}
		if flags&windows.ACE_INHERITED_OBJECT_TYPE_PRESENT != 0 {
			offset += 16
		}
		return (*windows.SID)(unsafe.Pointer(uintptr(unsafe.Pointer(&ace.SidStart)) + offset)), true
	default:
		return nil, false
	}
}

// windowsDirGrantsBroadenedWrite reports whether path's effective DACL lets
// BUILTIN\Users or Authenticated Users write. It walks the DACL (which, as
// returned by GetNamedSecurityInfo, already contains inherited ACEs) in order,
// honoring a deny ACE that precedes an allow for the same bits, the canonical
// evaluation. A NULL DACL grants everyone full access and is treated as
// writable.
//
// Note: this is a deliberate DACL walk rather than AccessCheck. It must detect
// grants that would become usable once the restricted token is broadened with
// those groups, independent of the setup process's own token. INHERIT_ONLY ACEs
// are skipped because they do not apply to the object itself.
func windowsDirGrantsBroadenedWrite(path string) (bool, error) {
	// GetNamedSecurityInfo returns a self-relative descriptor copied onto the Go
	// heap (it LocalFrees the Win32 allocation itself), so it must NOT be
	// LocalFree'd here: doing so frees Go-managed memory and corrupts the heap.
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil {
		return true, nil
	}
	var deniedWrite windows.ACCESS_MASK
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return false, fmt.Errorf("read ACE %d of %s: %w", index, path, err)
		}
		// An INHERIT_ONLY ACE does not apply to this object itself — it only
		// seeds ACLs of newly created children. Counting one here could let
		// an inherit-only deny suppress a later applicable allow in
		// deniedWrite, misclassifying a writable directory as safe.
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		sid, ok := windowsAceSID(ace)
		if !ok {
			continue
		}
		if !sid.IsWellKnown(windows.WinBuiltinUsersSid) && !sid.IsWellKnown(windows.WinAuthenticatedUserSid) {
			continue
		}
		writeBits := ace.Mask & windowsBroadenedWriteProbeMask
		if writeBits == 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE, windowsAccessDeniedObjectAceType:
			deniedWrite |= writeBits
		case windows.ACCESS_ALLOWED_ACE_TYPE, windowsAccessAllowedObjectAceType:
			if writeBits&^deniedWrite != 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

// windowsPathIsReparsePoint reports whether path carries the reparse-point
// attribute (a junction, symlink, or mount point). Any error resolving the
// attributes is reported as "not a reparse point" so the caller falls through
// to its own DACL read, which surfaces a real access problem there instead.
func windowsPathIsReparsePoint(path string) bool {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return false
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// windowsEnsureSharedDescendantCoverage re-enumerates the shared deny roots and
// ensures every currently Users/AuthUsers-writable descendant carries a direct
// DenyWrite for the stable read-only capability SID. It is called immediately
// before a command broadens the restricted token so a child created after
// `zero sandbox setup` cannot remain an uncovered write-jail escape.
//
// Prefer reapplying denies (same permanent posture as elevated setup). When
// reapply fails — typically because the command process lacks WRITE_DAC on a
// system path — fall back to a read-only hole check: if any writable
// descendant still lacks the synthetic deny, coverage is incomplete and the
// caller must not broaden. Fail closed on enumeration errors either way.
func windowsEnsureSharedDescendantCoverage(config WindowsSandboxCommandConfig) error {
	plan, err := BuildWindowsACLPlan(config)
	if err != nil {
		return err
	}
	writeRoots := windowsPlanAllowWriteRoots(plan)
	for _, group := range groupWindowsACLPlanByPath(plan) {
		denySID, ok := windowsGroupScanDescendantsSID(group)
		if !ok {
			continue
		}
		if _, err := os.Stat(group.Path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat shared deny root %s: %w", group.Path, err)
		}
		if _, err := applyWindowsSharedDescendantDenies(group.Path, denySID, writeRoots); err == nil {
			continue
		} else if holes, holeErr := windowsUncoveredWritableDescendants(group.Path, denySID, writeRoots); holeErr != nil {
			return holeErr
		} else if len(holes) > 0 {
			return fmt.Errorf("shared descendant write coverage incomplete under %s (e.g. %s): %w", group.Path, holes[0], err)
		}
		// Reapply failed but every currently writable descendant already carries
		// the synthetic deny, so the write jail still holds for this root.
	}
	return nil
}

// windowsUncoveredWritableDescendants returns Users/AuthUsers-writable
// descendants of root that do not yet carry a DenyWrite ACE for denySID.
func windowsUncoveredWritableDescendants(root, denySID string, writeRoots []string) ([]string, error) {
	descendants, err := windowsEnumerateWritableDescendants(root, writeRoots)
	if err != nil {
		return nil, fmt.Errorf("enumerate writable descendants of %s: %w", root, err)
	}
	var holes []string
	for _, dir := range descendants {
		denied, err := windowsPathDeniesCapabilitySID(dir, denySID)
		if err != nil {
			return nil, fmt.Errorf("inspect existing deny on %s: %w", dir, err)
		}
		if !denied {
			holes = append(holes, dir)
		}
	}
	return holes, nil
}

// windowsPathDeniesCapabilitySID reports whether path's DACL already contains
// a deny ACE naming the given capability SID string (the synthetic identity
// used for shared-root / descendant DenyWrite entries) that covers write
// access. A deny ACE for the right SID is only "already denies write" if its
// mask actually includes write-relevant bits: the same stable capability SID
// is also used for DenyRead entries (planWindowsDenyReadPaths), so a path
// that only carries a pre-existing DenyRead ACE for wantSID must NOT be
// mistaken for one that already blocks writes — see jatmn's review, which
// found this would mask a real writable descendant under a DenyRead path and
// skip closing it.
func windowsPathDeniesCapabilitySID(path, wantSID string) (bool, error) {
	want, err := windows.StringToSid(wantSID)
	if err != nil {
		return false, fmt.Errorf("parse capability SID %q: %w", wantSID, err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil {
		return false, nil
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return false, fmt.Errorf("read ACE %d of %s: %w", index, path, err)
		}
		if ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE && ace.Header.AceType != windowsAccessDeniedObjectAceType {
			continue
		}
		// An INHERIT_ONLY ACE does not apply to this object itself (see the
		// same skip in windowsDirGrantsBroadenedWrite). Counting one here
		// would report an inherited-but-inapplicable deny as "already
		// denied," causing applyWindowsSharedDescendantDenies to skip
		// applying the real, effective deny and leave the descendant open.
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		sid, ok := windowsAceSID(ace)
		if !ok {
			continue
		}
		if !sid.Equals(want) {
			continue
		}
		// Require the deny to actually cover write-relevant bits: a deny ACE
		// for wantSID that only denies, e.g., read/execute (the DenyRead
		// shape) does not close the write-jail escape this function exists to
		// detect, and must not be reported as an existing write deny.
		if ace.Mask&windowsBroadenedWriteProbeMask != 0 {
			return true, nil
		}
	}
	return false, nil
}
