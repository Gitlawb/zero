//go:build !windows

package sandbox

import (
	"errors"
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

func tryAcquireExclusiveRuntimeLease(path string) (runtimeLeaseHandle, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
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

func (lease runtimeLeaseHandle) release() {
	if lease.file == nil {
		return
	}
	_ = unix.Flock(int(lease.file.Fd()), unix.LOCK_UN)
	_ = lease.file.Close()
}
