//go:build windows

package mcp

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
	errorLockViolation      = syscall.Errno(33)
	errorSharingViolation   = syscall.Errno(32)
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func tryLockPermissionFile(file *os.File) (bool, error) {
	var overlapped syscall.Overlapped
	result, _, err := procLockFileEx.Call(
		file.Fd(),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if result != 0 {
		return true, nil
	}
	if errors.Is(err, errorLockViolation) || errors.Is(err, errorSharingViolation) {
		return false, nil
	}
	if err == syscall.Errno(0) {
		return false, nil
	}
	return false, err
}

func unlockPermissionFile(file *os.File) error {
	var overlapped syscall.Overlapped
	result, _, err := procUnlockFileEx.Call(
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
