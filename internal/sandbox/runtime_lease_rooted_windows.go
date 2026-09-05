//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// acquireRuntimeLeaseRooted creates and locks the lease file for root without
// ever resolving an owned component by name.
//
// THE LEASE IS ACQUIRED BEFORE ANYTHING ELSE, SO IT CANNOT BE THE WEAK LINK.
//
// Provisioning already descends from the fixed cache or TEMP base through
// retained no-follow handles, because a predictable owned component is exactly
// what an ordinary same-account process can replace with a junction. Lease
// acquisition ran first and did neither: it checked the components for aliases,
// then called os.MkdirAll on the parent by pathname and opened
// "<root>.lease" by pathname, both of which follow. A junction dropped on
// "zero", "runtime" or "v1" between the check and either call put elevated
// setup's first writes inside somebody else's tree, and restoring the component
// afterwards left the later handle-relative provisioning working on the
// legitimate tree so no post-check ever saw it.
//
// The lease file is a SIBLING of the runtime root rather than one of its owned
// components, so refuseAliasedRuntimeComponents never inspected it at all. Here
// it is created relative to the retained handle for the directory that contains
// it, which is the deepest owned component, so its name is resolved exactly once
// and relative to a verified object.
//
// Only the components above the leaf are created. The leaf itself belongs to
// provisioning, which records it for rollback; creating it here would mean two
// owners for one directory.
func acquireRuntimeLeaseRooted(root string) (*sandboxRuntimeLease, []windowsCreatedRuntimeDir, error) {
	base, components, owned := windowsSandboxRuntimeOwnedTail(root)
	if !owned {
		// Fail rather than fall back to the pathname walk: the walk is the defect.
		return nil, nil, fmt.Errorf("acquire sandbox runtime lease for %s: %w", root, errRuntimeTailNotOwned)
	}
	if len(components) == 0 {
		return nil, nil, fmt.Errorf("acquire sandbox runtime lease for %s: %w", root, errRuntimeTailNotOwned)
	}
	// The base is the operator's, and may legitimately be a redirected cache or
	// TEMP location, so it is created and opened by name exactly as provisioning
	// does. Everything below it is Zero's and is addressed by handle.
	if runtimeLeasePreCreateBarrier != nil {
		runtimeLeasePreCreateBarrier()
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create sandbox runtime base: %w", err)
	}

	// The lease sits beside the leaf, so the deepest directory needed here is the
	// leaf's parent.
	parents := components[:len(components)-1]
	tail := make([]string, 0, len(parents))
	current := base
	for _, component := range parents {
		current = filepath.Join(current, component)
		tail = append(tail, current)
	}

	created, parent, err := createRuntimeTailRetainingHandle(base, tail)
	if err != nil {
		if parent != 0 {
			_ = windows.CloseHandle(parent)
		}
		return nil, created, err
	}
	defer func() { _ = windows.CloseHandle(parent) }()

	handle, madeLease, err := acquireSharedRuntimeLeaseAt(parent, filepath.Base(sandboxRuntimeLeasePath(root)))
	if err != nil {
		return nil, created, fmt.Errorf("acquire sandbox runtime lease: %w", err)
	}
	return &sandboxRuntimeLease{handle: handle, root: root, createdFile: madeLease}, created, nil
}

// acquireSharedRuntimeLeaseAt is acquireSharedRuntimeLease with the file named
// relative to a directory handle instead of by full path.
func acquireSharedRuntimeLeaseAt(parent windows.Handle, name string) (runtimeLeaseHandle, bool, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return runtimeLeaseHandle{}, false, fmt.Errorf("encode sandbox runtime lease name %s: %w", name, err)
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))

	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	// FILE_OPEN_IF because the lease is shared: whoever gets there first creates
	// it. FILE_NON_DIRECTORY_FILE so a directory under that name is refused.
	//
	// FILE_OPEN_REPARSE_POINT IS NOT A REFUSAL. It says do not follow, so the call
	// returns a handle to the LINK, and FILE_NON_DIRECTORY_FILE excludes
	// directories rather than non-directory reparse objects. A file symbolic link
	// planted at <digest>.lease was therefore opened and locked as if it were the
	// lease. No-follow and classification are two requirements; the flag is only
	// the first, and the handle is asked for the second below.
	err = windows.NtCreateFile(
		&handle,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.SYNCHRONIZE,
		&attributes,
		&iosb,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN_IF,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	if err != nil {
		return runtimeLeaseHandle{}, false, err
	}
	// FILE_CREATED here, rather than a Stat before the call, because only the
	// create itself can distinguish the file it made from one that arrived a
	// moment earlier.
	created := iosb.Information == windowsFileCreatedDisposition
	if err := refuseReparseRuntimeLeaseHandle(handle, name); err != nil {
		_ = windows.CloseHandle(handle)
		return runtimeLeaseHandle{}, false, err
	}
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return runtimeLeaseHandle{}, false, fmt.Errorf("wrap the sandbox runtime lease handle for %s", name)
	}
	lease := runtimeLeaseHandle{file: file}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), 0, 0, 1, 0, &lease.overlapped); err != nil {
		_ = file.Close()
		return runtimeLeaseHandle{}, false, err
	}
	return lease, created, nil
}

// refuseReparseRuntimeLeaseHandle proves the opened lease is an ordinary file.
//
// Asked of the HANDLE, not the name, so there is no second resolution for a
// substitution to land in. This is the check the directory descent already makes
// in openWindowsChildNoFollow; the lease carried the no-follow flag and not the
// classification that has to go with it.
func refuseReparseRuntimeLeaseHandle(handle windows.Handle, name string) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("inspect the sandbox runtime lease %s: %w", name, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refusing to use the sandbox runtime lease at %s: a reparse point here means the shared and exclusive holders would lock different objects", name)
	}
	return nil
}

// openRuntimeLeaseParentRooted opens, and never creates, the directory holding
// the lease for root.
//
// Cleanup's side of the contract. It descends the same owned components from the
// same base with the same no-follow opens acquisition uses, so both sides resolve
// the lease name relative to a verified directory rather than by pathname.
//
// It creates nothing. A runtime tree that is not there has no lease to take, and
// cleanup rebuilding the tree in order to lock it would be inventing the thing it
// is about to remove.
func openRuntimeLeaseParentRooted(root string) (windows.Handle, error) {
	base, components, owned := windowsSandboxRuntimeOwnedTail(root)
	if !owned || len(components) == 0 {
		return 0, fmt.Errorf("open the sandbox runtime lease parent for %s: %w", root, errRuntimeTailNotOwned)
	}
	parent, err := openWindowsDirectoryByName(base)
	if err != nil {
		return 0, fmt.Errorf("open sandbox runtime base %s: %w", base, err)
	}
	// The lease is a SIBLING of the leaf, so the deepest directory needed here is
	// the leaf's parent, exactly as in acquisition.
	for _, name := range components[:len(components)-1] {
		child, openErr := openWindowsChildNoFollow(parent, name,
			windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, windows.FILE_DIRECTORY_FILE)
		if openErr != nil {
			_ = windows.CloseHandle(parent)
			return 0, openErr
		}
		_ = windows.CloseHandle(parent)
		parent = child
	}
	return parent, nil
}

// tryAcquireExclusiveRuntimeLeaseRooted is cleanup's acquisition, resolved the
// way acquisition resolves it.
func tryAcquireExclusiveRuntimeLeaseRooted(root string) (runtimeLeaseHandle, bool, error) {
	parent, err := openRuntimeLeaseParentRooted(root)
	if err != nil {
		return runtimeLeaseHandle{}, false, err
	}
	defer func() { _ = windows.CloseHandle(parent) }()

	name := filepath.Base(sandboxRuntimeLeasePath(root))
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return runtimeLeaseHandle{}, false, fmt.Errorf("encode sandbox runtime lease name %s: %w", name, err)
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))

	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	// FILE_OPEN_IF matches what cleanup did by pathname with O_CREATE: a runtime
	// root whose lease file is gone is held by nobody, and creating the empty lease
	// is how that is expressed.
	err = windows.NtCreateFile(
		&handle,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.SYNCHRONIZE,
		&attributes,
		&iosb,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN_IF,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	if err != nil {
		return runtimeLeaseHandle{}, false, err
	}
	if err := refuseReparseRuntimeLeaseHandle(handle, name); err != nil {
		_ = windows.CloseHandle(handle)
		return runtimeLeaseHandle{}, false, err
	}
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return runtimeLeaseHandle{}, false, fmt.Errorf("wrap the sandbox runtime lease handle for %s", name)
	}
	lease := runtimeLeaseHandle{file: file}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &lease.overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return runtimeLeaseHandle{}, true, nil
		}
		return runtimeLeaseHandle{}, false, err
	}
	return lease, false, nil
}

// windowsFileCreatedDisposition is the IO_STATUS_BLOCK Information value that
// says NtCreateFile made the file rather than opening one that was there.
// x/sys/windows does not export it.
const windowsFileCreatedDisposition = 2
