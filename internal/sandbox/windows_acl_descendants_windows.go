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
// The scan is deliberately bounded and targeted, NOT a blanket recursive walk:
// stamping an inheritable deny (or rewriting every descendant's ACL) across the
// system drive is the exact slow/brittle/ACL-polluting pathology the
// inheritable-deny approach was rejected for. Instead the traversal only ever
// WRITES a deny to a descendant it has confirmed is already writable by those
// broad groups, and it prunes the enormous non-writable system trees
// (C:\Windows, C:\Program Files) so cost stays bounded:
//
//   - It always descends the first windowsDescendantScanBaselineDepth levels,
//     so a shallow, freshly installed writable directory is caught even when its
//     parent is not itself writable.
//   - Below the baseline it descends ONLY into a directory that is itself
//     writable by the broad groups. A writable subtree is the real escape
//     surface and is small in practice, while a non-writable directory cannot
//     have been reached by such a write and so is pruned.
//   - windowsDescendantScanMaxDepth and windowsDescendantScanMaxDirs are hard
//     safety caps against a pathological deep or very broad writable tree. The
//     scan fails closed if either is hit: unlike a locked-down directory (see
//     below), hitting a cap means there IS unexamined territory, so reporting
//     success would certify an incomplete scan as clean.
//   - A directory this admin-elevated process cannot list, or whose DACL it
//     cannot read, is treated as locked down and pruned (not failed closed).
//     This is deliberate, not a residual gap: every one of the four shared
//     roots has real, common subdirectories Administrators are denied by
//     design regardless of privilege — "C:\System Volume Information" is
//     SYSTEM-exclusive on every NTFS volume (see Microsoft KB2867841) and
//     sits directly under the system drive root scanned here. Failing closed
//     on that ubiquitous, expected case would make `zero sandbox setup` fail
//     on every real machine, not just pathological ones. A directory the
//     elevated process cannot even open a handle to, or read the DACL of,
//     cannot have granted BUILTIN\Users or Authenticated Users write either
//     (that would require WRITE_DAC/READ_CONTROL to be broader than what this
//     process — running as Administrator — already has), so pruning it here
//     carries no realistic write-jail risk.
//
// Reparse points (junctions/symlinks) are skipped entirely: their target lives
// outside this subtree, so denying or descending them would touch unrelated
// objects and risk traversal loops.
const (
	windowsDescendantScanBaselineDepth = 2
	windowsDescendantScanMaxDepth      = 24
	windowsDescendantScanMaxDirs       = 8192
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
// (fail closed); a descendant whose parent it merely could not list or whose
// DACL it could not read is treated as locked-down and skipped in the
// enumeration itself (see windowsEnumerateWritableDescendants).
func applyWindowsSharedDescendantDenies(root, denySID string, writeRoots []string) ([]windowsACLSnapshot, error) {
	descendants, err := windowsEnumerateWritableDescendants(root, writeRoots)
	if err != nil {
		return nil, fmt.Errorf("enumerate writable descendants of %s: %w", root, err)
	}
	snapshots := make([]windowsACLSnapshot, 0, len(descendants))
	for _, dir := range descendants {
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
// descended into. See the package-level comment above for the traversal
// bounds and their rationale.
//
// The scan fails closed on exhausting windowsDescendantScanMaxDirs or
// windowsDescendantScanMaxDepth: either means there is unexamined territory
// this call cannot vouch for, so it returns an error rather than a partial
// result the caller could mistake for a complete one. A directory it cannot
// list, or a child whose DACL it cannot read, is treated as locked down and
// pruned instead — see the package-level comment for why that case is safe
// to skip rather than fail closed.
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
			// A directory the elevated setup cannot even list is locked down
			// (SYSTEM-owned, e.g. "System Volume Information" on every shared
			// root's own volume); the broad groups cannot write there either,
			// so skipping it is safe. Traversal is best-effort so a full
			// system-drive walk does not abort setup on the normal
			// un-listable system dirs it must step over.
			continue
		}
		for _, entry := range entries {
			child := filepath.Join(current.path, entry.Name())
			childKey := windowsCapabilityPathKey(child)
			if isExcluded(childKey) {
				continue
			}
			if windowsPathIsReparsePoint(child) {
				continue
			}
			if visited >= windowsDescendantScanMaxDirs {
				return nil, fmt.Errorf("descendant scan exceeded %d entries below %s", windowsDescendantScanMaxDirs, root)
			}
			visited++
			writable, err := windowsDirGrantsBroadenedWrite(child)
			if err != nil {
				// Cannot read the child's DACL: same reasoning as an un-listable
				// directory: locked down, not an escape target. Skip.
				continue
			}
			if writable {
				out = append(out, child)
			}
			if !entry.IsDir() {
				// A file has no descendants to walk; it either got denied above
				// or was not writable, either way there is nothing further to do.
				continue
			}
			childDepth := current.depth + 1
			if childDepth < windowsDescendantScanBaselineDepth || writable {
				if childDepth >= windowsDescendantScanMaxDepth {
					return nil, fmt.Errorf("descendant scan exceeded depth %d at %s", windowsDescendantScanMaxDepth, child)
				}
				queue = append(queue, node{path: child, depth: childDepth})
			}
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
