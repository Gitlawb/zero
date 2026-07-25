//go:build windows

package sandbox

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type runtimeLeaseHandle struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireSharedRuntimeLease(path string) (runtimeLeaseHandle, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return runtimeLeaseHandle{}, err
	}
	handle := runtimeLeaseHandle{file: file}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), 0, 0, 1, 0, &handle.overlapped); err != nil {
		_ = file.Close()
		return runtimeLeaseHandle{}, err
	}
	return handle, nil
}

func tryAcquireExclusiveRuntimeLease(path string) (runtimeLeaseHandle, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return runtimeLeaseHandle{}, false, err
	}
	handle := runtimeLeaseHandle{file: file}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &handle.overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return runtimeLeaseHandle{}, true, nil
		}
		return runtimeLeaseHandle{}, false, err
	}
	return handle, false, nil
}

func (lease runtimeLeaseHandle) release() {
	if lease.file == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(lease.file.Fd()), 0, 1, 0, &lease.overlapped)
	_ = lease.file.Close()
}
