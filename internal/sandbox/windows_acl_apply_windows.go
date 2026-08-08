//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
)

const windowsFileDeleteChild windows.ACCESS_MASK = 0x00000040

type windowsACLPathGroup struct {
	Path            string
	Entries         []WindowsACLEntry
	Materialize     bool
	MaterializeFile bool
}

// windowsACLChainStep is one directory component beneath the anchor, and
// whether THIS run created it. The flag comes from the kernel rather than from
// "it was missing when we looked", because something else can win the gap
// between the probe and the create, and removing a directory the sandbox did not
// make is how a rollback deletes a user's data.
type windowsACLChainStep struct {
	Name string
	Made bool
}

// windowsACLMaterialization is exactly what materialization created, recorded in
// the shape rollback needs to undo it without resolving a single pathname below
// the anchor.
//
// The anchor is the deepest directory that already existed, and it is the ONLY
// pathname rollback re-resolves. Everything under it is a list of single
// components walked one handle at a time, because a name containing a separator
// is resolved the ordinary way by the kernel and would follow an intermediate
// junction straight out of the approved tree.
type windowsACLMaterialization struct {
	AnchorPath string
	AnchorID   windowsFileIdentity
	// Chain is every component between the anchor and the target, shallow to
	// deep. All of them are needed to descend at rollback time; only the ones
	// with Made set are removed.
	Chain []windowsACLChainStep
	// File is the leaf file component created inside the deepest Chain entry,
	// for the .git/config carveout. Empty when the target is a directory.
	File     string
	FileMade bool
}

func (materialization windowsACLMaterialization) createdAnything() bool {
	if materialization.FileMade {
		return true
	}
	for _, step := range materialization.Chain {
		if step.Made {
			return true
		}
	}
	return false
}

type windowsACLSnapshot struct {
	Path       string
	Descriptor *windows.SECURITY_DESCRIPTOR
	Created    windowsACLMaterialization
}

func applyWindowsACLPlan(plan WindowsACLPlan) (func() error, error) {
	groups := groupWindowsACLPlanByPath(plan)
	snapshots := make([]windowsACLSnapshot, 0, len(groups))
	abort := func(err error) (func() error, error) {
		if rollbackErr := rollbackWindowsACLSnapshots(snapshots); rollbackErr != nil {
			return nil, fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
		}
		return nil, err
	}

	var deferred []windowsACLPathGroup
	for _, group := range groups {
		snapshot, applied, err := applyWindowsACLPathGroup(group)
		if err != nil {
			return abort(err)
		}
		if applied {
			snapshots = append(snapshots, snapshot)
			continue
		}
		deferred = append(deferred, group)
	}

	// A group that applied to nothing was skipped because its target did not
	// exist and the group does not materialize one. A LATER group can still
	// create it, so the skip has to be retried rather than treated as final.
	//
	// The .git rename guard is exactly this shape and was silently absent
	// because of it. .git must NOT be materialized, since an empty one breaks
	// `git init`, so its deny-delete group carries no Materialize. But .git does
	// get created, as the parent chain of the .git\config carveout, and that
	// group sorts AFTER it. So on every workspace that did not already have a
	// .git, the guard was skipped, the directory appeared moments later, and
	// nothing went back for it: the principal could then rename .git aside and
	// shed the config and hooks carveouts, which is the escape the guard exists
	// to stop. Groups that are genuinely absent simply skip again here.
	for _, group := range deferred {
		snapshot, applied, err := applyWindowsACLPathGroup(group)
		if err != nil {
			return abort(err)
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
		group.MaterializeFile = group.MaterializeFile || entry.MaterializeFile
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
	// Reject a malformed group BEFORE touching the filesystem. This validation
	// used to run after materialization, so a bad SID or an unknown action
	// created a directory chain and only then failed, leaving the error path to
	// unwind work that never needed doing. Nothing here depends on isDir: that
	// argument only selects the inheritance flag, while the errors come from the
	// action lookup and the SID parse.
	if _, err := windowsExplicitAccessEntries(group.Entries, false); err != nil {
		return windowsACLSnapshot{}, false, err
	}

	var created windowsACLMaterialization
	var handle windows.Handle
	// Every exit from here goes through one closure, because there are now two
	// things to undo rather than one: the open handle, and whatever
	// materialization created. Both are captured by reference and both start
	// zero, so calling this before either is set is safe and does nothing.
	//
	// The unwind is handle-relative. It must never fall back to a pathname
	// delete: the failure being cleaned up here can BE the path swap, and
	// os.RemoveAll on a swapped ancestor is precisely the recursive elevated
	// delete outside the workspace that this cleanup is supposed to prevent.
	fail := func(err error) (windowsACLSnapshot, bool, error) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		if unwindErr := rollbackWindowsACLMaterialization(created); unwindErr != nil {
			return windowsACLSnapshot{}, false, fmt.Errorf("%w; cleanup failed: %v", err, unwindErr)
		}
		return windowsACLSnapshot{}, false, err
	}

	var isDir bool
	var err error
	handle, isDir, err = openWindowsACLTarget(path)
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
		// created is assigned even on failure: materialization reports what it
		// managed to make before it stopped, and fail() unwinds exactly that.
		created, err = materializeWindowsACLTarget(path, group.MaterializeFile)
		if err != nil {
			return fail(fmt.Errorf("materialize windows ACL target %s: %w", path, err))
		}
		handle, isDir, err = openWindowsACLTarget(path)
		if err != nil {
			// This is the branch that fires when the post-create verify catches a
			// swap, so it is the single most important cleanup in the file.
			return fail(fmt.Errorf("open materialized windows ACL target %s: %w", path, err))
		}
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
	_ = windows.CloseHandle(handle)
	return windowsACLSnapshot{Path: path, Descriptor: descriptor, Created: created}, true, nil
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
	// Ancestors are resolved by CreateFile even with FILE_FLAG_OPEN_REPARSE_POINT,
	// so the check above is not enough on its own.
	if err := verifyWindowsACLTargetNotRedirected(handle, path); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, false, err
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
		entryInheritance := inheritance
		// DenyDelete governs the object it names and nothing beneath it. Inherited
		// onto .git's children it would deny DELETE on every file inside, so git
		// could not remove a lock file, a ref, or anything else it rewrites, and
		// the guard would read as a broken repository rather than as a blocked
		// rename.
		if entry.Action == WindowsACLDenyDelete {
			entryInheritance = windows.NO_INHERITANCE
		}
		out = append(out, windows.EXPLICIT_ACCESS{
			AccessPermissions: permissions,
			AccessMode:        accessMode,
			Inheritance:       entryInheritance,
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
		// DELETE is part of the grant, not an extra. FILE_GENERIC_WRITE covers
		// creating and modifying but not removing or renaming, and a rename needs
		// delete access on the source. Under the old same-user token that gap was
		// invisible, because the caller already held inherited rights on its own
		// tree; a sandbox principal is a separate account with no such
		// inheritance, so without DELETE it can write a file it can never delete.
		// Ordinary editing and most git operations rewrite files by replacing
		// them, so the omission fails normal work rather than an edge case.
		//
		// FILE_DELETE_CHILD is deliberately NOT granted, for the same reason
		// WRITE_DAC and WRITE_OWNER are not. On a parent it authorises deleting a
		// child whatever the child's own DACL says, so granting it on a write root
		// hands back the write-denied carve-outs underneath it: a principal could
		// delete .git/config and recreate it, and the replacement inherits this
		// grant with no deny of its own — restoring exactly the credential.helper
		// and core.hooksPath control the carve-out exists to prevent.
		//
		// It was granted here originally to keep the mask symmetric with
		// WindowsACLDenyWrite, which does treat FILE_DELETE_CHILD as part of
		// write. Symmetry is the wrong goal: denying a capability is not a reason
		// to grant it. DELETE alone covers removing and renaming files the
		// principal owns inside its roots, which is what the grant is for.
		return windows.GRANT_ACCESS, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE, nil
	case WindowsACLAllowRead:
		// Read and traverse without write. A sandbox principal is a separate
		// account with no inherent access to the caller's tree, so a read-only
		// root has to be granted rather than assumed. Deliberately omits
		// FILE_GENERIC_WRITE, DELETE and WRITE_DAC.
		return windows.GRANT_ACCESS, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE, nil
	case windowsACLRevoke:
		// REVOKE_ACCESS drops every ACE naming the trustee regardless of the mask,
		// so the mask is ignored here. Used to retire a principal without having
		// to remember which access each path was granted.
		return windows.REVOKE_ACCESS, 0, nil
	case WindowsACLDenyRead:
		return windows.DENY_ACCESS, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE, nil
	case WindowsACLDenyWrite:
		return windows.DENY_ACCESS, windows.FILE_GENERIC_WRITE | windows.DELETE | windowsFileDeleteChild | windows.WRITE_DAC | windows.WRITE_OWNER, nil
	case WindowsACLDenyDelete:
		// Deny removing or RENAMING the object itself, nothing more. Renaming a
		// directory needs DELETE on that directory, so denying DELETE is what
		// stops .git being moved aside and recreated without its carveouts.
		//
		// WRITE_DAC and WRITE_OWNER come along because a guard the principal can
		// rewrite, or take ownership of and then rewrite, is not a guard.
		//
		// FILE_GENERIC_WRITE is deliberately absent: git writes index, objects and
		// refs constantly, and denying it would break every commit rather than the
		// rename. FILE_DELETE_CHILD is absent for the same reason one level down,
		// since git deletes its own lock files and refs. Neither is needed here:
		// this ACE does not inherit (see windowsExplicitAccessEntries), so it
		// governs the .git directory object alone.
		return windows.DENY_ACCESS, windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER, nil
	default:
		return 0, 0, fmt.Errorf("unsupported windows ACL action %q", action)
	}
}

func rollbackWindowsACLSnapshots(snapshots []windowsACLSnapshot) error {
	var errs []error
	// Reverse order is load-bearing twice over. Groups are sorted ascending by
	// path key and an ancestor key is always a proper prefix of its descendants,
	// so walking backwards unwinds descendant before ancestor: a materialized
	// directory is therefore empty by the time its own removal is attempted.
	// Restoring ACLs in the same order is independently right, because
	// SetSecurityInfo propagates inheritable ACEs down, so the ancestor must go
	// last. TestRollbackUnwindsDescendantsBeforeAncestors pins it.
	for index := len(snapshots) - 1; index >= 0; index-- {
		snapshot := snapshots[index]
		if snapshot.Created.createdAnything() {
			if err := rollbackWindowsACLMaterialization(snapshot.Created); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		dacl, _, err := snapshot.Descriptor.DACL()
		if err != nil {
			errs = append(errs, fmt.Errorf("read rollback windows DACL for %s: %w", snapshot.Path, err))
			continue
		}
		// Re-open no-follow rather than restoring by pathname: the restore must
		// land on the real object, not a reparse point swapped in since apply. The
		// residual window is small because the target is ACL-restricted by now, but
		// a handle keeps the restore honest. On a materialized-target rollback we
		// remove it above, so only the restore-existing path opens here.
		handle, _, err := openWindowsACLTarget(snapshot.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("re-open windows ACL target %s for rollback: %w", snapshot.Path, err))
			continue
		}
		if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			errs = append(errs, fmt.Errorf("rollback windows ACL for %s: %w", snapshot.Path, err))
		}
		_ = windows.CloseHandle(handle)
	}
	return errors.Join(errs...)
}

// materializeWindowsACLTarget creates a missing ACL target with the shape the
// owning tool expects. A directory target is created whole; a file target gets
// its parent chain created and then an empty file, because creating it as a
// directory would break the tool that owns it rather than just mis-ACL it.
// The returned record is meaningful even when the error is non-nil: a chain that
// got three levels deep and then failed still has three levels to unwind.
func materializeWindowsACLTarget(path string, asFile bool) (windowsACLMaterialization, error) {
	directory := path
	leaf := ""
	if asFile {
		directory = filepath.Dir(path)
		leaf = filepath.Base(path)
	}
	created, parent, err := makeWindowsACLDirChainNoFollow(directory)
	if err != nil {
		return created, err
	}
	defer func() { _ = windows.CloseHandle(parent) }()
	if !asFile {
		return created, nil
	}
	// A racing creator winning is still fine: the target exists, which is all
	// materialization needed. createWindowsACLChildFile reports that as
	// created=false, so rollback will not delete a file the sandbox did not make.
	created.File = leaf
	madeFile, err := createWindowsACLChildFile(parent, leaf)
	created.FileMade = madeFile
	return created, err
}

// rollbackWindowsACLMaterialization removes exactly what materialization
// created, deepest first, without resolving any pathname below the anchor.
//
// This is the other half of the pathname problem. The old cleanup called
// os.RemoveAll on the target pathname, which re-resolves every ancestor at the
// moment it runs, so an ancestor swapped to a junction after the object was
// created sent a recursive elevated delete into an unrelated tree. It also only
// ever removed the final component, leaving every intermediate directory the
// chain had created behind.
//
// The anchor is the one pathname that has to be resolved again, and it is
// checked by file identity rather than by name: replacing a directory with
// another REAL directory of the same name needs no reparse point at all and
// would otherwise pass every no-follow check there is.
//
// Residue is preferable to over-deletion throughout. When something cannot be
// removed safely this reports it and leaves it, and never falls back to a
// pathname delete.
func rollbackWindowsACLMaterialization(materialization windowsACLMaterialization) error {
	if !materialization.createdAnything() {
		return nil
	}
	anchor, err := reopenWindowsACLDirectoryAsIdentity(materialization.AnchorPath, materialization.AnchorID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// The anchor is gone, so everything created beneath it is gone too.
			// Nothing to undo, and no way to undo it if there were.
			return nil
		}
		return fmt.Errorf("unwind windows ACL materialization under %s: %w", materialization.AnchorPath, err)
	}

	// One handle per level: handles[i] is the parent of Chain[i], which is what
	// deleting Chain[i] relative to a pinned parent requires.
	handles := []windows.Handle{anchor}
	defer func() {
		for _, handle := range handles {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
		}
	}()

	// Descend only as far as is actually required. Removing a directory needs its
	// PARENT's handle, not its own, so the deepest component is opened only when
	// a file leaf lives inside it. This is not just economy: the deepest
	// component is usually the ACL target itself, so it may already carry the
	// deny-read ACE this rollback is undoing, and opening it would be refused by
	// the very ACL being unwound.
	needed := len(materialization.Chain)
	if !materialization.FileMade && needed > 0 {
		needed--
	}
	depth := 0
	for ; depth < needed; depth++ {
		child, err := openWindowsACLChildDirectory(handles[depth], materialization.Chain[depth].Name)
		if err != nil {
			// Already removed by something else. Stop descending; whatever is
			// below it is gone with it.
			if isWindowsNotExist(err) {
				break
			}
			return fmt.Errorf("unwind windows ACL materialization under %s: %w", materialization.AnchorPath, err)
		}
		handles = append(handles, child)
	}

	var errs []error
	// The file leaf lives inside the deepest chain directory, so it goes first
	// and only if the descent actually reached that far.
	if materialization.FileMade && depth == needed {
		if err := deleteWindowsACLChildFile(handles[len(handles)-1], materialization.File); err != nil {
			errs = append(errs, fmt.Errorf("remove materialized windows ACL file %s: %w", materialization.File, err))
		}
	}
	// Chain[i] is removed through handles[i], so the deepest one that can be
	// removed is bounded by how far the descent actually got.
	deepest := len(handles) - 1
	if last := len(materialization.Chain) - 1; deepest > last {
		deepest = last
	}
	for index := deepest; index >= 0; index-- {
		// Close the child's own handle, if the descent opened one, before asking
		// its parent to remove it. A directory with a live handle open still
		// counts as present, so the parent's delete would come back as non-empty.
		if index+1 < len(handles) && handles[index+1] != 0 {
			_ = windows.CloseHandle(handles[index+1])
			handles[index+1] = 0
		}
		if !materialization.Chain[index].Made {
			continue
		}
		if err := deleteWindowsACLChildDirectory(handles[index], materialization.Chain[index].Name); err != nil {
			errs = append(errs, fmt.Errorf("remove materialized windows ACL directory %s: %w", materialization.Chain[index].Name, err))
		}
	}
	return errors.Join(errs...)
}

// windowsACLMaterializeSwapHook is a test seam and nothing else. It fires inside
// makeWindowsACLDirChainNoFollow at the exact instant the race used to be
// exploitable: the anchor is verified and pinned, and nothing has been created
// yet. A race reproducible only by luck is not a regression test, so the instant
// is made addressable rather than hoped for. Always nil in production.
var windowsACLMaterializeSwapHook func(anchor string)

// makeWindowsACLDirChainNoFollow is an os.MkdirAll that never resolves a
// pathname below its anchor.
//
// It walks UP to the deepest ancestor that already exists and opens it
// no-follow. Because GetFinalPathNameByHandle answers for the whole resolved
// path, that single check clears every ancestor above it too. Then it walks back
// DOWN, creating one component at a time relative to the HANDLE of the level
// above, so the tree it descends is pinned to objects rather than named by
// strings.
//
// That is the difference that matters. This used to verify a component by
// pathname, close the handle, and then hand the same string to os.Mkdir: two
// independent kernel resolutions with a gap between them. A workspace owner who
// swapped the verified ancestor for a junction inside that gap got the component
// created outside the approved tree, as Administrator, and verifying again
// afterwards cannot un-create it. Junctions need no privilege, so this was
// reachable by exactly the unprivileged user the sandbox exists to contain.
//
// It deliberately does NOT re-verify each created component by pathname. A child
// created relative to a pinned parent is in the right place by construction, so
// comparing pathnames afterwards would add nothing and would reject correct
// creates whenever the tree was legitimately renamed mid-setup.
//
// Returns what it created, plus an open handle to the deepest directory which
// the caller must close.
func makeWindowsACLDirChainNoFollow(dir string) (windowsACLMaterialization, windows.Handle, error) {
	cleaned := filepath.Clean(strings.TrimSpace(dir))
	if cleaned == "" || cleaned == "." {
		return windowsACLMaterialization{}, 0, fmt.Errorf("materialize windows ACL target: empty directory path %q", dir)
	}

	// Walk up to the deepest ancestor that exists, collecting the component
	// NAMES that are missing. Names, not paths: everything below the anchor is
	// addressed relative to a handle from here on.
	var missing []string
	current := cleaned
	var anchor windows.Handle
	var anchorID windowsFileIdentity
	for {
		handle, identity, err := openWindowsACLDirectoryNoFollowWithIdentity(current)
		if err == nil {
			anchor, anchorID = handle, identity
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return windowsACLMaterialization{}, 0, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return windowsACLMaterialization{}, 0, fmt.Errorf("materialize windows ACL target %s: no existing ancestor to anchor on", dir)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}

	created := windowsACLMaterialization{AnchorPath: current, AnchorID: anchorID}

	if hook := windowsACLMaterializeSwapHook; hook != nil {
		hook(current)
	}

	// Walk back down, one component per handle. The anchor handle is released as
	// soon as its child is open, so at most two levels are held at once.
	parent := anchor
	for index := len(missing) - 1; index >= 0; index-- {
		name := missing[index]
		child, madeNow, err := createWindowsACLChildDirectory(parent, name)
		if err != nil {
			_ = windows.CloseHandle(parent)
			return created, 0, err
		}
		created.Chain = append(created.Chain, windowsACLChainStep{Name: name, Made: madeNow})
		_ = windows.CloseHandle(parent)
		parent = child
	}
	return created, parent, nil
}
