//go:build windows

package sandbox

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	grantLockfileFailImmediately = 0x00000001
	grantLockfileExclusiveLock   = 0x00000002
	grantErrorLockViolation      = syscall.Errno(33)
	grantErrorSharingViolation   = syscall.Errno(32)
)

var (
	grantKernel32         = syscall.NewLazyDLL("kernel32.dll")
	grantProcLockFileEx   = grantKernel32.NewProc("LockFileEx")
	grantProcUnlockFileEx = grantKernel32.NewProc("UnlockFileEx")
)

func tryLockGrantFile(file *os.File) (bool, error) {
	var overlapped syscall.Overlapped
	result, _, err := grantProcLockFileEx.Call(
		file.Fd(),
		uintptr(grantLockfileExclusiveLock|grantLockfileFailImmediately),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if result != 0 {
		return true, nil
	}
	if errors.Is(err, grantErrorLockViolation) || errors.Is(err, grantErrorSharingViolation) {
		return false, nil
	}
	if err == syscall.Errno(0) {
		return false, nil
	}
	return false, err
}

func unlockGrantFile(file *os.File) error {
	var overlapped syscall.Overlapped
	result, _, err := grantProcUnlockFileEx.Call(
		file.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if result != 0 || err == syscall.Errno(0) {
		return nil
	}
	return err
}
