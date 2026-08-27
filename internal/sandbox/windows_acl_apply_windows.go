//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
)

const windowsFileDeleteChild windows.ACCESS_MASK = 0x00000040

type windowsACLPathGroup struct {
	Path        string
	Entries     []WindowsACLEntry
	Materialize bool
}

type windowsACLSnapshot struct {
	Path         string
	Descriptor   *windows.SECURITY_DESCRIPTOR
	Materialized bool
	// Identity is the object the forward apply actually modified, captured from
	// the open handle. Compensation reopens BY NAME, and a name is not an
	// object: see rollbackWindowsACLSnapshots.
	Identity windowsObjectIdentity
}

// windowsObjectIdentity identifies a filesystem object independently of the
// name it currently answers to.
type windowsObjectIdentity struct {
	volume uint32
	high   uint32
	low    uint32
	valid  bool
}

func windowsIdentityFromHandle(handle windows.Handle) windowsObjectIdentity {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return windowsObjectIdentity{}
	}
	return windowsObjectIdentity{
		volume: info.VolumeSerialNumber,
		high:   info.FileIndexHigh,
		low:    info.FileIndexLow,
		valid:  true,
	}
}

// matches is deliberately false when either side is unknown. A compensation
// that cannot prove it is acting on the object it changed must not act.
func (id windowsObjectIdentity) matches(other windowsObjectIdentity) bool {
	return id.valid && other.valid &&
		id.volume == other.volume && id.high == other.high && id.low == other.low
}

// windowsACLStampRequest asks the apply to write the runtime setup stamp THROUGH
// THE SAME HANDLE it just applied the capability ACE through.
//
// The stamp exists to prove that the directory a command later uses is the
// object setup granted the ACE to. Writing it afterwards by pathname cannot
// prove that, however carefully the second open is done: the ACE goes on
// through a rooted handle, that handle closes, network setup runs, and only then
// does the marker write re-open the name. A local process that can reach the
// user-owned runtime tree can remove the predictable root in that window and
// put an ordinary directory in its place. The re-open correctly rejects a
// junction, but an ordinary replacement is not a reparse point and is not the
// ACL-bearing object either, so it collects a valid-looking stamp. Marker
// validation then passes over a directory with no capability ACE, and the next
// WRITE_RESTRICTED command fails its cache and TMP writes with setup insisting
// it is current.
//
// Closing that means never naming the target again after the ACE lands. The
// hash is known before the apply, so the stamp can simply ride along.
type windowsACLStampRequest struct {
	Root     string
	PlanHash string
}

// windowsACLStampSwapHook fires in the exact window this design closes: after
// the capability ACE is on the object and before the stamp is written. Nil in
// production; a test uses it to replace the runtime root with an ordinary
// directory, which is what a local process would do.
var windowsACLStampSwapHook func(path string)

// windowsACLStampWriteHook replaces the ride-along stamp write. Nil in
// production; a test uses it to reach the post-commit failure path, which no
// ordinary input produces once the bound handle is already open.
var windowsACLStampWriteHook func(path string) error

// writeRidingStamp writes the stamp through the handle the capability ACE was
// applied on, or through the test hook when one is installed.
func writeRidingStamp(handle windows.Handle, path string, planHash string) error {
	if windowsACLStampWriteHook != nil {
		return windowsACLStampWriteHook(path)
	}
	return writeWindowsRuntimeStampToDirectoryHandle(handle, planHash)
}

// restoreWindowsACLThroughHandle puts a captured DACL back on the object the
// handle names, by handle rather than by pathname for the same reason the stamp
// rides along: after the apply, the name is no longer proof of the object.
func restoreWindowsACLThroughHandle(handle windows.Handle, descriptor *windows.SECURITY_DESCRIPTOR) error {
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read the captured windows DACL: %w", err)
	}
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func applyWindowsACLPlan(plan WindowsACLPlan) (func() error, error) {
	return applyWindowsACLPlanWithStamp(plan, nil)
}

func applyWindowsACLPlanWithStamp(plan WindowsACLPlan, stamp *windowsACLStampRequest) (func() error, error) {
	groups := groupWindowsACLPlanByPath(plan)
	snapshots := make([]windowsACLSnapshot, 0, len(groups))
	for _, group := range groups {
		snapshot, applied, err := applyWindowsACLPathGroupWithStamp(group, stamp)
		if err != nil {
			rollbackErr := rollbackWindowsACLSnapshots(snapshots)
			if rollbackErr != nil {
				return nil, fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
			}
			return nil, err
		}
		if applied {
			snapshots = append(snapshots, snapshot)
		}
	}
	return func() error {
		return rollbackWindowsACLSnapshots(snapshots)
	}, nil
}

func groupWindowsACLPlanByPath(plan WindowsACLPlan) []windowsACLPathGroup {
	byPath := map[string]*windowsACLPathGroup{}
	for _, entry := range dedupeWindowsACLEntries(plan.Entries) {
		key := windowsCapabilityPathKey(entry.Path)
		if key == "" {
			continue
		}
		group := byPath[key]
		if group == nil {
			group = &windowsACLPathGroup{Path: entry.Path}
			byPath[key] = group
		}
		group.Entries = append(group.Entries, entry)
		group.Materialize = group.Materialize || entry.Materialize
	}
	out := make([]windowsACLPathGroup, 0, len(byPath))
	for _, group := range byPath {
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		return windowsCapabilityPathKey(out[i].Path) < windowsCapabilityPathKey(out[j].Path)
	})
	return out
}

func applyWindowsACLPathGroup(group windowsACLPathGroup) (windowsACLSnapshot, bool, error) {
	return applyWindowsACLPathGroupWithStamp(group, nil)
}

func applyWindowsACLPathGroupWithStamp(group windowsACLPathGroup, stamp *windowsACLStampRequest) (windowsACLSnapshot, bool, error) {
	path := strings.TrimSpace(group.Path)
	if path == "" || len(group.Entries) == 0 {
		return windowsACLSnapshot{}, false, nil
	}
	// Open ONE no-follow handle to the target and drive every ACL operation
	// (read + write) through it, so the read and the write hit the same kernel
	// object. The previous pathname-based Stat/GetNamedSecurityInfo/
	// SetNamedSecurityInfo each re-resolved the path independently, so during
	// elevated setup a lower-privileged local user could swap the target for a
	// symlink/junction between operations and redirect the ACL change onto a
	// system object it never validated (issue #728, a TOCTOU privilege boundary).
	materialized := false
	handle, isDir, err := openWindowsACLTarget(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return windowsACLSnapshot{}, false, err
		}
		if !group.Materialize {
			if windowsACLGroupRequiresExistingTarget(group) {
				return windowsACLSnapshot{}, false, fmt.Errorf("windows ACL target does not exist: %s", path)
			}
			return windowsACLSnapshot{}, false, nil
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return windowsACLSnapshot{}, false, fmt.Errorf("materialize windows ACL target %s: %w", path, err)
		}
		materialized = true
		handle, isDir, err = openWindowsACLTarget(path)
		if err != nil {
			_ = os.RemoveAll(path)
			return windowsACLSnapshot{}, false, fmt.Errorf("open materialized windows ACL target %s: %w", path, err)
		}
	}
	// From here the handle is open; every early return must close it first (and
	// remove a freshly materialized target) so a failure leaks neither.
	fail := func(err error) (windowsACLSnapshot, bool, error) {
		_ = windows.CloseHandle(handle)
		if materialized {
			_ = os.RemoveAll(path)
		}
		return windowsACLSnapshot{}, false, err
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fail(fmt.Errorf("read windows ACL for %s: %w", path, err))
	}
	oldDACL, _, err := descriptor.DACL()
	if err != nil {
		return fail(fmt.Errorf("read windows DACL for %s: %w", path, err))
	}
	accessEntries, err := windowsExplicitAccessEntries(group.Entries, isDir)
	if err != nil {
		return fail(err)
	}
	nextDACL, err := windows.ACLFromEntries(accessEntries, oldDACL)
	if err != nil {
		return fail(fmt.Errorf("build windows ACL for %s: %w", path, err))
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, nextDACL, nil); err != nil {
		return fail(fmt.Errorf("apply windows ACL for %s: %w", path, err))
	}
	// The apply is committed; the retained descriptor is the rollback baseline.
	// The handle has served its purpose (read+write bound to one object) and is
	// closed now — rollback re-opens no-follow rather than holding a handle for
	// the whole sandbox lifetime, since one caller discards the rollback closure.
	// BEFORE THE HANDLE CLOSES, and only for the target the stamp names. This is
	// the whole point: the ACE and the stamp land on one kernel object with no
	// pathname resolution in between.
	if stamp != nil && windowsSameRuntimeRootPath(stamp.Root, path) {
		if windowsACLStampSwapHook != nil {
			windowsACLStampSwapHook(path)
		}
		if err := writeRidingStamp(handle, path, stamp.PlanHash); err != nil {
			// THE ACE AND ITS STAMP ARE ONE TRANSACTION.
			//
			// SetSecurityInfo above has already committed, and this function
			// returns no rollback closure on its error paths, so the caller has
			// nothing to compensate with. Without this restore a failed setup
			// reports failure while leaving the capability grant on a pre-existing
			// runtime root: the tree stays writable by the restricted token and
			// nothing on disk records that it should not be.
			if restoreErr := restoreWindowsACLThroughHandle(handle, descriptor); restoreErr != nil {
				return fail(fmt.Errorf("stamp windows ACL target %s: %w (the committed ACL could not be restored either: %v)", path, err, restoreErr))
			}
			return fail(fmt.Errorf("stamp windows ACL target %s: %w", path, err))
		}
	}
	// Captured while the handle is still open, because this is the last moment
	// the object and the name are known to be the same thing.
	identity := windowsIdentityFromHandle(handle)
	_ = windows.CloseHandle(handle)
	return windowsACLSnapshot{Path: path, Descriptor: descriptor, Materialized: materialized, Identity: identity}, true, nil
}

// openWindowsACLTarget opens path for reading and rewriting its DACL without
// following a final-component reparse point (FILE_FLAG_OPEN_REPARSE_POINT), and
// with FILE_FLAG_BACKUP_SEMANTICS so a directory can be opened. It returns the
// handle and whether the target is a directory. A reparse-point target is
// rejected outright: a sandbox setup target that resolves to a symlink/junction
// during elevated setup is the signature of a path-swap attack, and following it
// is exactly the redirection this guard exists to prevent. A missing target is
// surfaced as os.ErrNotExist so the caller's materialize path still fires.
func openWindowsACLTarget(path string) (windows.Handle, bool, error) {
	// A RUNTIME ROOT IS OPENED BY HANDLE, NOT BY NAME.
	//
	// FILE_FLAG_OPEN_REPARSE_POINT below protects only the FINAL component; every
	// ancestor in the pathname is resolved normally. The runtime tail is the one
	// part of the tree Zero creates and therefore the one part an unprivileged
	// local user can predict and pre-empt, and junctions need no privilege, so a
	// swap at an owned ancestor between the last check and this open redirects the
	// elevated capability ACL into a directory of their choosing.
	//
	// Everything else here is the user's own tree, where an ancestor reparse point
	// is ordinary configuration and following it is correct.
	if _, _, owned := windowsSandboxRuntimeOwnedTail(path); owned {
		handle, err := openWindowsRuntimeTailDirectory(path, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_TRAVERSE)
		if err != nil {
			return 0, false, err
		}
		return handle, true, nil
	}
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, false, fmt.Errorf("encode windows ACL target %s: %w", path, err)
	}
	handle, err := windows.CreateFile(
		utf16Path,
		windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		// syscall.Errno.Is maps ERROR_FILE_NOT_FOUND/ERROR_PATH_NOT_FOUND to
		// os.ErrNotExist, so the %w keeps the caller's errors.Is check working.
		return 0, false, fmt.Errorf("open windows ACL target %s: %w", path, err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, false, fmt.Errorf("inspect windows ACL target %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return 0, false, fmt.Errorf("refusing to apply ACL to reparse-point target %s: possible path swap during elevated setup", path)
	}
	isDir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	return handle, isDir, nil
}

func windowsACLGroupRequiresExistingTarget(group windowsACLPathGroup) bool {
	for _, entry := range group.Entries {
		if entry.Action == WindowsACLAllowWrite {
			return true
		}
	}
	return false
}

func windowsExplicitAccessEntries(entries []WindowsACLEntry, isDir bool) ([]windows.EXPLICIT_ACCESS, error) {
	out := make([]windows.EXPLICIT_ACCESS, 0, len(entries))
	inheritance := uint32(0)
	if isDir {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	for _, entry := range entries {
		sid, err := windows.StringToSid(entry.Capability)
		if err != nil {
			return nil, fmt.Errorf("parse windows capability SID %q: %w", entry.Capability, err)
		}
		accessMode, permissions, err := windowsACLAccess(entry.Action)
		if err != nil {
			return nil, err
		}
		out = append(out, windows.EXPLICIT_ACCESS{
			AccessPermissions: permissions,
			AccessMode:        accessMode,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	return out, nil
}

func windowsACLAccess(action WindowsACLAction) (windows.ACCESS_MODE, windows.ACCESS_MASK, error) {
	switch action {
	case WindowsACLAllowWrite:
		return windows.GRANT_ACCESS, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE, nil
	case WindowsACLDenyRead:
		return windows.DENY_ACCESS, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE, nil
	case WindowsACLDenyWrite:
		return windows.DENY_ACCESS, windows.FILE_GENERIC_WRITE | windows.DELETE | windowsFileDeleteChild | windows.WRITE_DAC | windows.WRITE_OWNER, nil
	default:
		return 0, 0, fmt.Errorf("unsupported windows ACL action %q", action)
	}
}

func rollbackWindowsACLSnapshots(snapshots []windowsACLSnapshot) error {
	var errs []error
	for index := len(snapshots) - 1; index >= 0; index-- {
		snapshot := snapshots[index]
		// A NAME IS NOT AN OBJECT ONCE THE APPLY HANDLE HAS CLOSED.
		//
		// The forward apply and its stamp go through one handle, so they are
		// provably about one object. Compensation runs later, after a network or
		// marker failure, and resolves these names again. Opening no-follow stops
		// a reparse point but accepts an ORDINARY directory moved into the name
		// since: the original is renamed aside, a substitute is created, and
		// rollback then restores the pre-apply DACL onto the substitute, strips a
		// stamp there, and reports success, while the moved original keeps this
		// run's capability ACE and a valid stamp. Setup would claim a completed
		// rollback with the modified object still reachable elsewhere, having
		// also mutated something it never touched going forward.
		//
		// So every compensation proves it holds the object it changed, and
		// otherwise leaves the substitute alone and says plainly what was left
		// behind.
		if snapshot.Materialized {
			// NOT identity-checked, and the reason is a real limit rather than an
			// oversight. A materialized target is one this run created, and its
			// plan routinely denies Everyone read (that is what a protected
			// metadata carve-out IS), so the attributes identity needs cannot be
			// read back even by the owner: the check turned rollback of every
			// materialized directory into "Access is denied". Establishing
			// identity here would mean holding the apply handle open until the
			// last failure point, which is a larger change than this one.
			if err := os.RemoveAll(snapshot.Path); err != nil {
				errs = append(errs, fmt.Errorf("remove materialized windows ACL target %s: %w", snapshot.Path, err))
			}
			continue
		}
		// ONE OPEN, AND THE IDENTITY COMES FROM THE HANDLE THAT GETS MUTATED.
		//
		// Checking identity through a separate open and then resolving the name
		// again for the restore proves nothing about the second handle: the two
		// opens are a check-then-use, and the fact established (this NAME resolved
		// to the object we changed) is not the fact the write depends on (this
		// HANDLE is that object). Opening once and asking the handle who it is
		// removes the window rather than narrowing it.
		handle, _, err := openWindowsACLTarget(snapshot.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("re-open windows ACL target %s for rollback: %w", snapshot.Path, err))
			continue
		}
		if !snapshot.Identity.matches(windowsIdentityFromHandle(handle)) {
			_ = windows.CloseHandle(handle)
			errs = append(errs, fmt.Errorf(
				"windows ACL target %s is no longer the object this setup modified; "+
					"leaving the replacement untouched, and the original still carries this run's grant",
				snapshot.Path))
			continue
		}
		dacl, _, err := snapshot.Descriptor.DACL()
		if err != nil {
			_ = windows.CloseHandle(handle)
			errs = append(errs, fmt.Errorf("read rollback windows DACL for %s: %w", snapshot.Path, err))
			continue
		}
		if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			errs = append(errs, fmt.Errorf("rollback windows ACL for %s: %w", snapshot.Path, err))
		}
		_ = windows.CloseHandle(handle)
	}
	return errors.Join(errs...)
}
