//go:build windows

package lockutil

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// RestoreLockFile attempts to restore a sidelined lock file on Windows. It
// calls MoveFileExW directly via golang.org/x/sys/windows (the standard
// library's syscall package does not export MoveFileEx on this platform)
// with no flags, so if the destination already exists it fails with
// ERROR_ALREADY_EXISTS, which satisfies errors.Is(err, os.ErrExist) (verified
// against the real Win32 call), instead of overwriting it the way os.Rename
// does on Windows (it passes MOVEFILE_REPLACE_EXISTING to match POSIX rename
// semantics cross-platform).
func RestoreLockFile(reclaimed, path string) error {
	fromPtr, err := windows.UTF16PtrFromString(reclaimed)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, 0)
}

// RemoveLockFile attempts to remove a lock file on Windows, retrying on sharing violations
// or access denied errors that are common under heavy concurrent contention.
func RemoveLockFile(path string) error {
	var err error
	for i := 0; i < 15; i++ {
		err = os.Remove(path)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		var errno syscall.Errno
		if errors.As(err, &errno) && (errno == 32 || errno == 5) {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		return err
	}
	return err
}
