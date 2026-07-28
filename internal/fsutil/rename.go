// Package fsutil provides small filesystem helpers shared across packages.
package fsutil

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
)

// CommittedReplacementCleanupError reports that a replacement was committed,
// but the old destination retained at BackupPath could not be removed. Callers
// must treat the replacement itself as successful and surface the cleanup
// problem separately.
type CommittedReplacementCleanupError struct {
	BackupPath string
	Cause      error
}

func (err *CommittedReplacementCleanupError) Error() string {
	return fmt.Sprintf("replacement committed, but backup %s could not be removed: %v", err.BackupPath, err.Cause)
}

func (err *CommittedReplacementCleanupError) Unwrap() error {
	return err.Cause
}

// ReplaceWithRetry publishes src over dst using the platform's replacement
// primitive, retrying on the same transient Windows lock errors RenameWithRetry
// handles. Prefer it over RenameWithRetry when dst may already exist and is
// expected to keep its identity: on Windows it replaces through ReplaceFileW,
// which is a single operation (os.Rename is documented non-atomic there) and
// carries the destination's security descriptor over to the replacement instead
// of publishing the temporary file's inherited ACL. On Unix it is os.Rename.
//
// replace overrides the platform primitive so tests can exercise the retry path;
// pass nil for the default.
func ReplaceWithRetry(src, dst string, replace func(src, dst string) error) error {
	if replace == nil {
		replace = replaceExisting
	}
	return RenameWithRetry(src, dst, replace)
}

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
		var committed *CommittedReplacementCleanupError
		if errors.As(err, &committed) {
			// The source has already been consumed. In particular, never retry if
			// the cleanup cause itself is a transient Windows sharing violation.
			break
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
