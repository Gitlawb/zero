//go:build !windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// acquireRuntimeLeaseRootedUnix creates and locks the lease for root without
// resolving an owned component by name.
//
// The lease is the FIRST thing setup writes, so it is the write a redirection has
// to be caught before, not after. It is a sibling of the runtime root rather than
// one of its owned components, which is why the alias guard never looked at it at
// all: it walks the components, and the lease is not one of them.
//
// Only the components above the leaf are created here. The leaf belongs to
// provisioning, which records it for rollback, and creating it here would mean
// two owners for one directory.
func acquireRuntimeLeaseRootedUnix(root string) (*sandboxRuntimeLease, []windowsCreatedRuntimeDir, error) {
	base, components, owned := windowsSandboxRuntimeOwnedTail(root)
	if !owned || len(components) == 0 {
		// Fail rather than fall back to the pathname walk: the walk is the defect.
		return nil, nil, fmt.Errorf("acquire sandbox runtime lease for %s: %w", root, errRuntimeTailNotOwned)
	}
	if runtimeLeasePreCreateBarrier != nil {
		runtimeLeasePreCreateBarrier()
	}
	// The base is the operator's, and may legitimately be a redirected or linked
	// temp location, so it is created and opened by name exactly as before.
	// Everything below it is Zero's and is addressed by descriptor.
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create sandbox runtime base: %w", err)
	}

	// The lease sits beside the leaf, so the deepest directory needed here is the
	// leaf's parent.
	tail := make([]string, 0, len(components)-1)
	current := base
	for _, component := range components[:len(components)-1] {
		current = filepath.Join(current, component)
		tail = append(tail, current)
	}

	created, parent, err := createRuntimeTailRetainingFD(base, tail)
	if err != nil {
		return nil, created, err
	}
	defer func() { _ = unix.Close(parent) }()

	handle, err := acquireSharedRuntimeLeaseAtFD(parent, filepath.Base(sandboxRuntimeLeasePath(root)))
	if err != nil {
		return nil, created, fmt.Errorf("acquire sandbox runtime lease: %w", err)
	}
	return &sandboxRuntimeLease{handle: handle}, created, nil
}

// acquireSharedRuntimeLeaseAtFD opens the lease relative to a verified parent and
// takes the shared lock on it.
func acquireSharedRuntimeLeaseAtFD(parent int, name string) (runtimeLeaseHandle, error) {
	file, err := openRuntimeLeaseAtFD(parent, name)
	if err != nil {
		return runtimeLeaseHandle{}, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH); err != nil {
		_ = file.Close()
		return runtimeLeaseHandle{}, err
	}
	return runtimeLeaseHandle{file: file}, nil
}

// openRuntimeLeaseAtFD opens or creates the lease under parent and proves it is
// an ordinary file.
//
// O_NOFOLLOW so a symlink planted at the lease name is an ELOOP rather than an
// open of its target, and a regular-file check besides, because O_NOFOLLOW says
// nothing about a fifo or a device a local user can also create there. Both
// holders have to end up on the same object or the lock protects nothing.
func openRuntimeLeaseAtFD(parent int, name string) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("refusing to use the sandbox runtime lease at %s: it is a link, so the holders would lock different objects: %w", name, err)
		}
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("inspect the sandbox runtime lease %s: %w", name, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("refusing to use the sandbox runtime lease at %s: it is not an ordinary file (mode %#o)", name, stat.Mode&unix.S_IFMT)
	}
	if uid := unix.Getuid(); uid >= 0 && stat.Uid != uint32(uid) {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("refusing to use the sandbox runtime lease at %s: it is owned by uid %d, not %d", name, stat.Uid, uid)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("wrap the sandbox runtime lease handle for %s", name)
	}
	return file, nil
}

// tryAcquireExclusiveRuntimeLeaseRootedUnix is cleanup's acquisition, resolved
// the way acquisition resolves it.
//
// It opens and never creates the tree: a runtime root that is not there has no
// lease to take, and cleanup rebuilding it in order to lock it would be inventing
// the thing it is about to remove.
func tryAcquireExclusiveRuntimeLeaseRootedUnix(root string) (runtimeLeaseHandle, bool, error) {
	base, components, owned := windowsSandboxRuntimeOwnedTail(root)
	if !owned || len(components) == 0 {
		return runtimeLeaseHandle{}, false, fmt.Errorf("open the sandbox runtime lease parent for %s: %w", root, errRuntimeTailNotOwned)
	}
	parent, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return runtimeLeaseHandle{}, false, fmt.Errorf("open sandbox runtime base %s: %w", base, err)
	}
	path := base
	for _, name := range components[:len(components)-1] {
		path = filepath.Join(path, name)
		child, openErr := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			_ = unix.Close(parent)
			return runtimeLeaseHandle{}, false, fmt.Errorf("open sandbox runtime component %s: %w", path, openErr)
		}
		if err := refuseForeignRuntimeDirectory(child, path); err != nil {
			_ = unix.Close(child)
			_ = unix.Close(parent)
			return runtimeLeaseHandle{}, false, err
		}
		_ = unix.Close(parent)
		parent = child
	}
	defer func() { _ = unix.Close(parent) }()

	file, err := openRuntimeLeaseAtFD(parent, filepath.Base(sandboxRuntimeLeasePath(root)))
	if err != nil {
		return runtimeLeaseHandle{}, false, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return runtimeLeaseHandle{}, true, nil
		}
		return runtimeLeaseHandle{}, false, err
	}
	return runtimeLeaseHandle{file: file}, false, nil
}
