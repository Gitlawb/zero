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

// Shared-root DenyWrite and Users/Authenticated Users SID broadening are no
// longer shipped: BuildWindowsACLPlan does not stamp machine-wide denies on
// C:\, %ProgramData%, %SystemRoot%\Temp, or C:\Users\Public, and the restricted
// token is never broadened with those groups. This file keeps the descendant
// walker and related helpers for (1) migration cleanup of stale denies left by
// earlier PR builds and (2) Windows regression tests of that walker.
//
// Historical coverage rules the walker still implements (fail closed when used):
//
//   - Every directory under a root is considered for descent within the
//     depth/entry caps, whether or not the parent itself is Users-writable.
//   - Hitting windowsDescendantScanMaxDepth or windowsDescendantScanMaxDirs
//     means unexamined territory remains: the scan returns an error rather than
//     certifying a partial walk as clean.
//   - Reparse points (junctions, symlinks, volume mount points) use a
//     no-follow, identity-aware policy (see jatmn's review). The walker:
//     1. Detects reparse points via FILE_ATTRIBUTE_REPARSE_POINT (and the
//     ModeSymlink/ModeIrregular bits ReadDir already reports) and never
//     inspects their DACL, never applies a deny, and never descends.
//     2. Records the (volume serial, file index) identity of every real
//     directory it does enter, so an alternate path to the same object is
//     skipped rather than re-enumerated.
//   - A directory this process cannot list, or a child whose DACL it cannot
//     read, is fail-closed UNLESS the basename is a known SYSTEM-exclusive
//     Windows directory (e.g. "System Volume Information") AND it sits
//     directly under an actual drive letter root (windowsPathIsDriveRootPath).
//
// Basename policies live in windows_acl_descendants.go so non-Windows tests can
// pin them without Win32. Bounds are vars so Windows tests can lower them.
var (
	windowsDescendantScanMaxDepth = 48
	// windowsDescendantScanMaxDirs bounds the total files+directories the scan
	// will inspect below a single root. Kept large enough for stock system
	// trees while still bounding worst-case work; the walker is retained for
	// migration cleanup and tests, not live elevated DenyRead setup.
	windowsDescendantScanMaxDirs = 500000
)

// windowsBroadenedWriteProbeMask is the set of access-mask bits that let a
// principal create, delete, or modify content, attributes, or extended
// attributes in (or the security of) a directory, i.e. the bits that make a
// directory a usable write-jail escape. FILE_WRITE_DATA is FILE_ADD_FILE and
// FILE_APPEND_DATA is FILE_ADD_SUBDIRECTORY for a directory object.
const windowsBroadenedWriteProbeMask windows.ACCESS_MASK = (windows.FILE_GENERIC_WRITE |
	windowsFileDeleteChild |
	windows.DELETE |
	windows.WRITE_DAC |
	windows.WRITE_OWNER) &^ windows.SYNCHRONIZE

// applyWindowsSharedDescendantDenies enumerates the existing writable
// descendants of a shared root and applies a direct, non-inheriting DenyWrite
// (naming denySID, the same stable read-only capability SID the root deny uses)
// to each. It returns every snapshot it applied (including on error) so the
// caller can roll the whole apply back. A descendant it identified as writable
// but could not deny is a hole it cannot close, so that failure is returned
// (fail closed). An incomplete enumeration (caps, unreadable non-reparse child)
// is also returned as an error. Reparse points are skipped by the enumerator
// (no-follow). Descendants that already carry a complete write deny for
// denySID are left untouched so setup reruns and command-time revalidation do
// not accumulate duplicate permanent ACEs.
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
// Fail closed: exhausting the depth or entry caps, or failing to list/inspect
// a non-allowlisted, non-reparse entry returns an error rather than a partial
// success the caller could mistake for complete coverage. Reparse points are
// skipped (no-follow), not treated as incomplete coverage: their targets are
// reached through the real path when it lies under the same root.
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
	// seenIDs records real directory object identities already entered so an
	// alternate path to the same object is not re-enumerated (identity-aware
	// half of the reparse policy).
	seenIDs := make(map[windowsFileObjectID]struct{})
	queue := []node{{path: root, depth: 0}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		// Defensive: never list through a reparse path that somehow reached
		// the queue (root is expected to be a real directory).
		if windowsPathIsReparsePoint(current.path) {
			continue
		}
		if id, ok := windowsFileObjectIdentity(current.path); ok {
			if _, seen := seenIDs[id]; seen {
				continue
			}
			seenIDs[id] = struct{}{}
		}
		entries, err := os.ReadDir(current.path)
		if err != nil {
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
			// No-follow: stock compatibility junctions (and any other reparse
			// point) are never DACL-inspected, denied, or descended. Mode bits
			// catch what ReadDir already classified; GetFileAttributes covers
			// any reparse form those bits miss.
			isReparse := (entry.Type()&os.ModeSymlink != 0) || (entry.Type()&os.ModeIrregular != 0) || windowsPathIsReparsePoint(child)
			if isReparse {
				if visited >= windowsDescendantScanMaxDirs {
					return nil, fmt.Errorf("descendant scan exceeded %d entries below %s", windowsDescendantScanMaxDirs, root)
				}
				visited++
				continue
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

// windowsFileObjectID is the NTFS object identity used to detect that two
// paths name the same directory (volume serial + 64-bit file index).
type windowsFileObjectID struct {
	volume uint32
	index  uint64
}

// windowsFileObjectIdentity returns the on-disk identity of path when it can
// be opened as a real (non-reparse) directory. ok is false on any open/inspect
// failure so the walker falls through to path-based enumeration rather than
// treating an unreadable directory as already-seen.
func windowsFileObjectIdentity(path string) (windowsFileObjectID, bool) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windowsFileObjectID{}, false
	}
	handle, err := windows.CreateFile(
		ptr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return windowsFileObjectID{}, false
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return windowsFileObjectID{}, false
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return windowsFileObjectID{}, false
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return windowsFileObjectID{}, false
	}
	return windowsFileObjectID{
		volume: info.VolumeSerialNumber,
		index:  (uint64(info.FileIndexHigh) << 32) | uint64(info.FileIndexLow),
	}, true
}

// windowsAccessAllowedObjectAceType and windowsAccessDeniedObjectAceType are
// the AceType values for ACCESS_ALLOWED_OBJECT_ACE / ACCESS_DENIED_OBJECT_ACE
// (https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-access_allowed_object_ace).
// windowsAccessAllowedCallbackAceType and windowsAccessAllowedCallbackObjectAceType
// are ACCESS_ALLOWED_CALLBACK_ACE_TYPE and ACCESS_ALLOWED_CALLBACK_OBJECT_ACE_TYPE
// (MS-DTYP 2.4.4.6 / conditional-ACE object variant): a callback ACE carries a
// conditional expression (e.g. "resource attribute matches") that gates
// whether the grant applies, appended AFTER the SID, so it does not move the
// SID's own offset relative to its non-callback sibling. x/sys/windows only
// models the plain ACCESS_ALLOWED_ACE layout (Header, Mask, SidStart) and
// exposes just ACCESS_ALLOWED_ACE_TYPE/ACCESS_DENIED_ACE_TYPE, so all four are
// declared locally.
const (
	windowsAccessAllowedObjectAceType         = 0x05
	windowsAccessDeniedObjectAceType          = 0x06
	windowsAccessAllowedCallbackAceType       = 0x09
	windowsAccessAllowedCallbackObjectAceType = 0x0B
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
// Users grant hidden inside an object ACE.
//
// ACCESS_ALLOWED_CALLBACK_ACE_TYPE and ACCESS_ALLOWED_CALLBACK_OBJECT_ACE_TYPE
// are recognized the same way as their non-callback counterparts: per MS-DTYP,
// a callback ACE's conditional expression ("ApplicationData") is appended
// AFTER the SID, not inserted before it, so the SID offset is identical. Only
// the ALLOW callback variants are recognized here, deliberately — see
// windowsDirGrantsBroadenedWrite for why a callback DENY is never trusted to
// suppress a grant. ok is false for any other ACE type (audit, alarm,
// mandatory label, compound, callback deny, ...), which either does not
// represent a trustee write grant in the sense this scan cares about, or (for
// callback deny) is not safe to rely on, and is skipped exactly as it always
// has been.
func windowsAceSID(ace *windows.ACCESS_ALLOWED_ACE) (sid *windows.SID, ok bool) {
	switch ace.Header.AceType {
	case windows.ACCESS_ALLOWED_ACE_TYPE, windows.ACCESS_DENIED_ACE_TYPE, windowsAccessAllowedCallbackAceType:
		return (*windows.SID)(unsafe.Pointer(&ace.SidStart)), true
	case windowsAccessAllowedObjectAceType, windowsAccessDeniedObjectAceType, windowsAccessAllowedCallbackObjectAceType:
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
//
// A callback allow ACE (ACCESS_ALLOWED_CALLBACK_ACE / _OBJECT_ACE) is treated
// exactly like an unconditional allow: this static walk cannot evaluate the
// ACE's conditional expression against the sandbox token, so the only safe
// assumption is the worst case, that the condition holds and the grant
// applies (see jatmn's review). The symmetric callback DENY types are
// deliberately NOT recognized by windowsAceSID at all, so they never reach
// this switch: trusting an unproven condition to suppress deniedWrite would
// risk the opposite mistake, misclassifying a writable directory as safe.
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
		case windows.ACCESS_ALLOWED_ACE_TYPE, windowsAccessAllowedObjectAceType,
			windowsAccessAllowedCallbackAceType, windowsAccessAllowedCallbackObjectAceType:
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
// deny ACE(s) naming the given capability SID string (the synthetic identity
// used for shared-root / descendant DenyWrite entries) that together cover
// every write-relevant bit in windowsBroadenedWriteProbeMask.
//
// A partial deny is not coverage: denying only FILE_WRITE_ATTRIBUTES (or only
// read/execute via a DenyRead ACE that reuses the same stable SID) leaves
// FILE_WRITE_DATA / FILE_APPEND_DATA open for a Users/AuthUsers grant, so the
// apply and verification paths must still merge the full canonical DenyWrite
// rather than skipping the path — see jatmn's review. Accumulated deny ACEs
// for wantSID are OR'd before the completeness check so a multi-ACE full
// cover still counts.
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
	var deniedMask windows.ACCESS_MASK
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
		if !ok || !sid.Equals(want) {
			continue
		}
		deniedMask |= ace.Mask
	}
	return (deniedMask & windowsBroadenedWriteProbeMask) == windowsBroadenedWriteProbeMask, nil
}
