//go:build !windows

package sandbox

import (
	"os"

	"golang.org/x/sys/unix"
)

type runtimeLeaseHandle struct {
	file *os.File
}

func acquireSharedRuntimeLease(path string) (runtimeLeaseHandle, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return runtimeLeaseHandle{}, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH); err != nil {
		_ = file.Close()
		return runtimeLeaseHandle{}, err
	}
	return runtimeLeaseHandle{file: file}, nil
}

// BOTH SIDES HAVE TO MEAN THE SAME OBJECT.
//
// Cleanup opened the lease by full pathname while acquisition opened it relative
// to a verified parent, so a symlink at <digest>.lease gave the two of them
// different files: a live command held a shared lock on one, cleanup took an
// exclusive lock on the other, both succeeded, and cleanup went on to remove a
// runtime root that was still in use.
func tryAcquireExclusiveRuntimeLease(root string) (runtimeLeaseHandle, bool, error) {
	return tryAcquireExclusiveRuntimeLeaseRootedUnix(root)
}

func (lease runtimeLeaseHandle) release() {
	if lease.file == nil {
		return
	}
	_ = unix.Flock(int(lease.file.Fd()), unix.LOCK_UN)
	_ = lease.file.Close()
}
