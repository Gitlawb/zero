//go:build windows

package sandbox

import (
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

	handle, err := acquireSharedRuntimeLeaseAt(parent, filepath.Base(sandboxRuntimeLeasePath(root)))
	if err != nil {
		return nil, created, fmt.Errorf("acquire sandbox runtime lease: %w", err)
	}
	return &sandboxRuntimeLease{handle: handle}, created, nil
}

// acquireSharedRuntimeLeaseAt is acquireSharedRuntimeLease with the file named
// relative to a directory handle instead of by full path.
func acquireSharedRuntimeLeaseAt(parent windows.Handle, name string) (runtimeLeaseHandle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return runtimeLeaseHandle{}, fmt.Errorf("encode sandbox runtime lease name %s: %w", name, err)
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
	// it. FILE_NON_DIRECTORY_FILE and FILE_OPEN_REPARSE_POINT so a directory or a
	// link planted under that name is refused rather than opened through.
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
		return runtimeLeaseHandle{}, err
	}
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return runtimeLeaseHandle{}, fmt.Errorf("wrap the sandbox runtime lease handle for %s", name)
	}
	lease := runtimeLeaseHandle{file: file}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), 0, 0, 1, 0, &lease.overlapped); err != nil {
		_ = file.Close()
		return runtimeLeaseHandle{}, err
	}
	return lease, nil
}
