//go:build windows

package swarm

import (
	"errors"
	"os"
	"syscall"
)

// Windows error codes that an O_EXCL create (or a racing delete) can surface
// when another writer holds the lock file open, instead of ERROR_FILE_EXISTS.
// Treating them as "contended" makes the lock retry/wait rather than fail the
// whole send/read spuriously under contention.
const (
	errorAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	errorSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
)

// isLockContended reports whether an O_EXCL lock-create error means the lock is
// currently held (so the caller should retry/wait) rather than a hard failure.
// On Windows a held lock can appear as ERROR_FILE_EXISTS (mapped to os.ErrExist)
// but also as ERROR_ACCESS_DENIED / ERROR_SHARING_VIOLATION when the file is
// concurrently open or pending deletion.
func isLockContended(err error) bool {
	if errors.Is(err, os.ErrExist) {
		return true
	}
	return errors.Is(err, errorAccessDenied) || errors.Is(err, errorSharingViolation)
}
