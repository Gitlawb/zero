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
//     safety caps against a pathological deep or very broad writable tree; if
//     either is hit the scan stops (leaving deeper writable descendants
//     unscanned, a bounded residual gap, still strictly smaller than the
//     root-object-only enforcement it replaces).
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
// principal create, delete, or modify content in (or the security of) a
// directory, i.e. the bits that make a directory a usable write-jail escape.
// FILE_WRITE_DATA is FILE_ADD_FILE and FILE_APPEND_DATA is FILE_ADD_SUBDIRECTORY
// for a directory object.
const windowsBroadenedWriteProbeMask windows.ACCESS_MASK = windows.FILE_WRITE_DATA |
	windows.FILE_APPEND_DATA |
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

// windowsEnumerateWritableDescendants returns the existing directories below
// root that grant BUILTIN\Users or Authenticated Users write, excluding any
// configured write root (and anything under it) so legitimate workspace writes
// are never jailed. See the package-level comment above for the traversal
// bounds and their rationale.
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
			// (SYSTEM-owned); the broad groups cannot write there, so skipping
			// it is safe. Traversal is best-effort so a full system-drive walk
			// does not abort setup on the normal un-listable system dirs it must
			// step over.
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			child := filepath.Join(current.path, entry.Name())
			childKey := windowsCapabilityPathKey(child)
			if isExcluded(childKey) {
				continue
			}
			if windowsPathIsReparsePoint(child) {
				continue
			}
			if visited >= windowsDescendantScanMaxDirs {
				return out, nil
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
			childDepth := current.depth + 1
			if childDepth >= windowsDescendantScanMaxDepth {
				continue
			}
			if childDepth < windowsDescendantScanBaselineDepth || writable {
				queue = append(queue, node{path: child, depth: childDepth})
			}
		}
	}
	return out, nil
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
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsWellKnown(windows.WinBuiltinUsersSid) && !sid.IsWellKnown(windows.WinAuthenticatedUserSid) {
			continue
		}
		writeBits := ace.Mask & windowsBroadenedWriteProbeMask
		if writeBits == 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			deniedWrite |= writeBits
		case windows.ACCESS_ALLOWED_ACE_TYPE:
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
