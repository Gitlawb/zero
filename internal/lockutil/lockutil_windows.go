//go:build windows

package lockutil

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// RestoreLockFile restores a sidelined lock file on Windows. It moves the file
// with a no-replace MoveFileEx so a competing lock created at path in the
// meantime wins: the move fails with os.ErrExist instead of overwriting it. If
// the move fails for any other reason (ERROR_SHARING_VIOLATION or
// ERROR_ACCESS_DENIED under concurrent access to the file), it falls back to
// an O_EXCL copy rather than leaving path missing and the live holder's lock
// stranded in reclaimed.
func RestoreLockFile(reclaimed, path string) error {
	err := moveFileNoReplace(reclaimed, path)
	if err == nil || errors.Is(err, os.ErrExist) {
		return err
	}
	return restoreByCopy(reclaimed, path)
}

// moveFileNoReplace renames from to to, failing if to already exists. It calls
// MoveFileExW directly via golang.org/x/sys/windows (the standard library's
// syscall package does not export MoveFileEx on this platform) with no flags,
// so an existing destination fails with ERROR_ALREADY_EXISTS, which satisfies
// errors.Is(err, os.ErrExist) (pinned by
// TestMoveFileNoReplaceMapsAlreadyExistsToErrExist), instead of overwriting it
// the way os.Rename does on Windows (it passes MOVEFILE_REPLACE_EXISTING to
// match POSIX rename semantics cross-platform).
func moveFileNoReplace(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, 0)
}

// RemoveLockFile removes a lock file on Windows, retrying on the sharing
// violation or access denied errors that are common under heavy concurrent
// contention. Removing an already-missing file reports nil, matching the
// non-Windows implementation, so callers see one cross-platform contract.
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
