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
//     These bounds must be large enough that a stock C:\ (with its Windows,
//     Program Files, and WinSxS trees) actually completes — see the comment on
//     the vars below for the reasoning and the honest limits of that estimate.
//   - Reparse points (junctions, symlinks, volume mount points) are NOT a
//     special case: CreateFile/GetNamedSecurityInfo/ReadDir called on a path
//     without FILE_FLAG_OPEN_REPARSE_POINT already transparently resolve
//     through a directory junction or symlink to its target (standard NTFS
//     reparse behavior), so treating a reparse point exactly like a normal
//     directory here already inspects and descends into whatever it actually
//     points at. A stock drive has real compatibility junctions (e.g.
//     C:\Documents and Settings -> C:\Users) that must not hard-fail the scan
//     (see jatmn's review); this walker no longer special-cases them at all.
//     What bounds a pathological loop (a junction pointing at an ancestor) is
//     the same depth/entry cap as everything else — worst case that fails
//     closed, it does not run forever.
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
	windowsDescendantScanMaxDepth = 48
	// windowsDescendantScanMaxDirs bounds the total files+directories the scan
	// will inspect below a single shared root. A stock Windows install can
	// easily have tens to hundreds of thousands of objects under C:\Windows
	// alone (WinSxS in particular), so the previous 8,192 cap made every
	// elevated DenyRead setup fail on a normal system drive (jatmn's review).
	// This is raised to a size intended to comfortably cover a typical stock
	// C:\Windows + Program Files + Program Files (x86) tree while still
	// bounding worst-case work to a finite number rather than removing the
	// cap outright. It is a reasoned estimate, not a measurement: this fix was
	// written and cross-compiled without access to a real Windows machine, so
	// the actual object count on any given box (and the wall-clock cost of
	// walking it, since windowsEnsureSharedDescendantCoverage repeats this
	// scan before every DenyRead command) could not be verified directly.
	// Failing closed here only costs functionality (the narrow SID set), never
	// safety, so an unusually large tree is a safe, if inconvenient, failure
	// mode rather than a security regression.
	windowsDescendantScanMaxDirs = 500000
)

// windowsBroadenedWriteProbeMask is the set of access-mask bits that let a
// principal create, delete, or modify content, attributes, or extended
// attributes in (or the security of) a directory, i.e. the bits that make a
// directory a usable write-jail escape. FILE_WRITE_DATA is FILE_ADD_FILE and
// FILE_APPEND_DATA is FILE_ADD_SUBDIRECTORY for a directory object.
const windowsBroadenedWriteProbeMask windows.ACCESS_MASK = windows.FILE_GENERIC_WRITE |
	windowsFileDeleteChild |
	windows.DELETE |
	windows.WRITE_DAC |
	windows.WRITE_OWNER

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
			isReparse := (entry.Type()&os.ModeSymlink != 0) || (entry.Type()&os.ModeIrregular != 0)
			if isReparse {
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

// windowsEnsureSharedDescendantCoverage re-enumerates the shared deny roots and
// ensures every currently Users/AuthUsers-writable descendant carries a direct
// DenyWrite for the stable read-only capability SID. It is called immediately
// before a command broadens the restricted token so a child created after
// `zero sandbox setup` cannot remain an uncovered write-jail escape.
//
// It also revalidates the direct deny on each shared ROOT itself (C:\,
// ProgramData, Windows\Temp, Public), not just its descendants: this was
// previously left unchecked, so an installer or service that removed the root
// deny after setup would leave the descendant walk reporting no holes while
// the root itself had silently reopened, and the runner would still broaden
// the token (see jatmn's review).
//
// Prefer reapplying denies (same permanent posture as elevated setup) for both
// the root and its descendants. When reapply fails — typically because the
// command process lacks WRITE_DAC on a system path — fall back to a
// read-only check: if the root's own deny is missing, or any writable
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
		rootDenied, checkErr := windowsPathDeniesCapabilitySID(group.Path, denySID)
		if checkErr != nil {
			return checkErr
		}
		if !rootDenied {
			if _, _, err := applyWindowsACLPathGroup(group); err != nil {
				denied, denyErr := windowsPathDeniesCapabilitySID(group.Path, denySID)
				if denyErr != nil || !denied {
					return fmt.Errorf("shared root write deny missing on %s: %w", group.Path, err)
				}
				// Reapply failed but the root's own deny is still effectively in
				// place, so the write jail continues to hold for this root.
			}
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
