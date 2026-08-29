// Package fsutil provides small filesystem helpers shared across packages.
package fsutil

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// WriteFileAtomic writes data to a temporary file in the same directory as filename,
// flushes and syncs it to disk, and replaces filename atomically via ReplaceWithRetry.
// For new files, it honors the process umask by creating the temporary file with
// os.OpenFile(..., perm). For existing regular files, it preserves the existing file mode.
//
// Platform differences on symlink destinations: On Unix (Linux/macOS), replacing a
// symlink destination replaces the symlink itself with the new regular file. On Windows,
// ReplaceFileW refuses symlink destinations outright and returns an error.
// Hard links to destination files are broken by design (temp-and-rename publishes a new inode).
func WriteFileAtomic(filename string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var existingMode *os.FileMode
	info, err := os.Lstat(filename)
	switch {
	case err == nil:
		if info.Mode().IsRegular() {
			m := info.Mode().Perm()
			existingMode = &m
		}
	case !os.IsNotExist(err):
		return err
	}

	tmpFile, err := createTempFile(dir, perm)
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
		_ = os.Remove(tmpName)
	}()

	if existingMode != nil {
		if err := tmpFile.Chmod(*existingMode); err != nil {
			return err
		}
		if err := preserveOwner(tmpFile, info); err != nil {
			return err
		}
	}
	if _, err := tmpFile.Write(data); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}
	closed = true
	if err := tmpFile.Close(); err != nil {
		return err
	}

	replaceErr := ReplaceWithRetry(tmpName, filename, nil)
	if replaceErr == nil || isCommittedReplacement(replaceErr) {
		syncDir(dir)
	}
	return replaceErr
}

func createTempFile(dir string, perm os.FileMode) (*os.File, error) {
	for i := 0; i < 10000; i++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, fmt.Sprintf(".zero-tmp-%x", b))
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, errors.New("fsutil: failed to create temporary file after repeated attempts")
}

func isCommittedReplacement(err error) bool {
	var committed *CommittedReplacementCleanupError
	return errors.As(err, &committed)
}

func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}

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
// handles. On Windows it uses ReplaceFileW so an existing destination keeps its
// DACL and selected metadata instead of receiving the temporary file's inherited
// DACL. ReplaceFileW is not observer-atomic and may briefly leave dst absent;
// callers that cannot tolerate that must synchronize their own readers. On Unix
// replacement uses os.Rename.
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
