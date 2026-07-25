// Package fsutil provides small filesystem helpers shared across packages.
package fsutil

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"
)

// RenameWithRetry renames src to dst, retrying briefly on Windows when the
// destination is transiently locked (antivirus scanners, search indexers, or
// a concurrent reader holding the file open). rename overrides os.Rename so
// tests can exercise the retry path; pass nil to use os.Rename.
func RenameWithRetry(src, dst string, rename func(src, dst string) error) error {
	if rename == nil {
		rename = os.Rename
	}
	var err error
	for i := 0; i < 10; i++ {
		err = rename(src, dst)
		if err == nil {
			return nil
		}
		if runtime.GOOS == "windows" {
			if os.IsPermission(err) || isWindowsSharingOrLockViolation(err) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}
		break
	}
	return err
}

func isWindowsSharingOrLockViolation(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		const ERROR_SHARING_VIOLATION syscall.Errno = 32
		const ERROR_LOCK_VIOLATION syscall.Errno = 33
		return errno == ERROR_SHARING_VIOLATION || errno == ERROR_LOCK_VIOLATION
	}
	return false
}
