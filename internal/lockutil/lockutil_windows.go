//go:build windows

package lockutil

import (
	"syscall"
)

// RestoreLockFile attempts to restore a sidelined lock file on Windows.
// It uses syscall.MoveFileEx without replacement flags so it fails if the destination already exists.
func RestoreLockFile(reclaimed, path string) error {
	fromPtr, err := syscall.UTF16PtrFromString(reclaimed)
	if err != nil {
		return err
	}
	toPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return syscall.MoveFileEx(fromPtr, toPtr, 0)
}
