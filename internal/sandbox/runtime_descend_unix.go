//go:build !windows

package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// createRuntimeTailRetainingFD creates the owned tail of the runtime root
// beneath base, one component at a time, from retained descriptors.
//
// THE NAME IS NOT AUTHORIZATION, ON THIS SIDE EITHER.
//
// The fallback root moved from an atomically minted os.MkdirTemp parent to a
// predictable /tmp/zero-u<uid>/runtime/v1/<digest>. The stable name is required,
// because setup and every later process have to agree on one root without
// talking to each other, but it also means another local account can name the
// first owned component before this one does. refuseAliasedRuntimeComponents
// answers about a component that is ABSENT by saying there is nothing to alias,
// and the code then called os.MkdirAll on the parent and opened <root>.lease by
// full pathname. Both follow a link planted after that answer, so the guard was
// authorizing writes it could not see the destination of.
//
// So the base is opened by name exactly once, and every component below it is
// created or opened relative to a retained descriptor with O_NOFOLLOW. A link at
// an owned component is then an ELOOP or ENOTDIR from the kernel rather than a
// redirection nobody noticed, and no owned name is ever resolved twice.
//
// The base itself is opened WITHOUT O_NOFOLLOW. It belongs to the operator and is
// legitimately a link on macOS, where /tmp resolves through /private. Only the
// tail Zero creates is held to the stricter rule.
func createRuntimeTailRetainingFD(base string, tail []string) ([]windowsCreatedRuntimeDir, int, error) {
	if runtimeBaseOpenedByName != nil {
		runtimeBaseOpenedByName(base)
	}
	parent, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, -1, fmt.Errorf("open sandbox runtime base %s: %w", base, err)
	}

	if runtimeDescentBarrier != nil {
		runtimeDescentBarrier()
	}

	var created []windowsCreatedRuntimeDir
	for _, path := range tail {
		name := filepath.Base(path)
		child, madeIt, openErr := openOrCreateRuntimeChildNoFollow(parent, name)
		if openErr != nil {
			_ = unix.Close(parent)
			return created, -1, fmt.Errorf("open sandbox runtime component %s: %w", path, openErr)
		}
		if err := refuseForeignRuntimeDirectory(child, path); err != nil {
			_ = unix.Close(child)
			_ = unix.Close(parent)
			return created, -1, err
		}
		if madeIt {
			identity, idErr := runtimeDirectoryIdentity(child)
			if idErr != nil {
				_ = unix.Close(child)
				_ = unix.Close(parent)
				return created, -1, fmt.Errorf("identify the sandbox runtime directory created at %s: %w", path, idErr)
			}
			created = append(created, windowsCreatedRuntimeDir{path: path, identity: identity, identified: true})
		}
		_ = unix.Close(parent)
		parent = child
	}
	return created, parent, nil
}

// openOrCreateRuntimeChildNoFollow opens name under parent, creating it if it is
// not there, and never follows a link at that name.
//
// madeIt distinguishes the directory this process created from one that was
// already there, because only the former belongs on the rollback ledger.
func openOrCreateRuntimeChildNoFollow(parent int, name string) (fd int, madeIt bool, err error) {
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	child, openErr := unix.Openat(parent, name, flags, 0)
	if openErr == nil {
		return child, false, nil
	}
	if !errors.Is(openErr, unix.ENOENT) {
		// ELOOP or ENOTDIR here IS the finding: a link sits where a directory of
		// ours should be. Returned as-is rather than translated, because the two
		// platforms spell it differently and the caller only needs it to fail.
		return -1, false, openErr
	}
	if mkErr := unix.Mkdirat(parent, name, 0o700); mkErr != nil {
		if !errors.Is(mkErr, unix.EEXIST) {
			return -1, false, mkErr
		}
		// Somebody created it between the open and the create. That is an ordinary
		// race with another Zero process, and the reopen below is still no-follow,
		// so a link that arrived in the same window is refused rather than used.
		child, openErr = unix.Openat(parent, name, flags, 0)
		if openErr != nil {
			return -1, false, openErr
		}
		return child, false, nil
	}
	child, openErr = unix.Openat(parent, name, flags, 0)
	if openErr != nil {
		return -1, false, openErr
	}
	return child, true, nil
}

// refuseForeignRuntimeDirectory proves the directory reached belongs to this
// user and is not group- or world-writable.
//
// Asked of the DESCRIPTOR, so it describes the object the descent is holding
// rather than whatever the name resolves to now. Ownership is the part that
// matters on a shared temp root: a component another account created first is
// theirs, and continuing into it would put the sandbox runtime inside a tree they
// control even though nothing about it is a link.
func refuseForeignRuntimeDirectory(fd int, path string) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect sandbox runtime component %s: %w", path, err)
	}
	if uid := unix.Getuid(); uid >= 0 && stat.Uid != uint32(uid) {
		return fmt.Errorf("refusing to use the sandbox runtime component %s: it is owned by uid %d, not %d, so another account chose what the sandbox writes into",
			path, stat.Uid, uid)
	}
	if stat.Mode&(unix.S_IWGRP|unix.S_IWOTH) != 0 {
		return fmt.Errorf("refusing to use the sandbox runtime component %s: mode %#o lets another account replace what is inside it",
			path, stat.Mode&0o7777)
	}
	return nil
}

// runtimeDirectoryIdentity names the object behind a descriptor, so rollback can
// tell the directory it created from one that replaced it under the same name.
func runtimeDirectoryIdentity(fd int) (string, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(stat.Dev), 10) + ":" + strconv.FormatUint(uint64(stat.Ino), 10), nil
}
