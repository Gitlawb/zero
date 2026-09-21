package tools

import (
	"errors"
	"io"
	"os"

	"github.com/Gitlawb/zero/internal/fsutil"
)

var errFileChangedDuringWrite = errors.New("file changed on disk before the write committed")

// fileWriteBeforeCommit is a deterministic test hook. Production leaves it
// nil; tests use it to replace a path after observation but before opening the
// object that will actually be mutated.
var fileWriteBeforeCommit func(path string)

// fileWriteStat is a deterministic test seam for proving that the opened-file
// identity is captured before the final preimage comparison. Production uses
// the file descriptor directly.
var fileWriteStat = func(file *os.File) (os.FileInfo, error) { return file.Stat() }

// commitFileContents binds an overwrite to the file identity and bytes that
// the caller observed, then publishes the new content through a private
// same-directory temporary file and an atomic replacement (fsutil.WriteFileAtomic).
//
// The identity checks run against the object opened at commit time, before any
// mutation: a path replacement between observation and commit therefore fails
// instead of publishing stale content, and no reader ever observes a truncated
// destination (invariant #921). The exclusive-create branch refuses a path that
// appeared after the caller observed it missing.
//
// The binding covers observation through the pre-publication check only. This
// handle is closed before publishFileContents replaces the path, so a swap
// between the check and the replace is overwritten rather than refused; that
// window is inherent to temp-and-replace.
//
// The returned warning string is non-empty only when the replacement already
// committed but its backup cleanup failed; the caller reports success and
// surfaces the warning. A non-nil error means nothing was published.
func commitFileContents(path string, expectedInfo os.FileInfo, expectedContent *string, content string) (string, error) {
	if fileWriteBeforeCommit != nil {
		fileWriteBeforeCommit(path)
	}

	if expectedInfo == nil {
		if _, err := os.Lstat(path); err == nil {
			return "", errFileChangedDuringWrite
		} else if !os.IsNotExist(err) {
			return "", err
		}
		return publishFileContents(path, content)
	}

	file, err := os.OpenFile(path, fileCommitOpenFlags(expectedContent), 0)
	if err != nil {
		return "", err
	}
	openedInfo, err := fileWriteStat(file)
	if err != nil {
		_ = file.Close()
		return "", err
	}
	if !os.SameFile(expectedInfo, openedInfo) {
		_ = file.Close()
		return "", errFileChangedDuringWrite
	}
	pathInfo, err := os.Stat(path)
	if err != nil || !os.SameFile(openedInfo, pathInfo) {
		_ = file.Close()
		return "", errFileChangedDuringWrite
	}
	if expectedContent != nil {
		current, readErr := io.ReadAll(file)
		if readErr != nil {
			_ = file.Close()
			return "", readErr
		}
		if string(current) != *expectedContent {
			_ = file.Close()
			return "", errFileChangedDuringWrite
		}
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return publishFileContents(path, content)
}

// fileCommitOpenFlags selects the descriptor used to bind an overwrite to the
// observed object. When the caller has preimage bytes to compare, the descriptor
// must be readable (and writable, to prove the same authorization an in-place
// write would have required); when it does not, write access alone is enough.
func fileCommitOpenFlags(expectedContent *string) int {
	if expectedContent != nil {
		return os.O_RDWR
	}
	return os.O_WRONLY
}

// publishFileContents performs the atomic replacement and treats a committed
// replacement whose backup cleanup failed as a successful write, returning the
// warning instead of flipping the tool status to error.
func publishFileContents(path, content string) (string, error) {
	err := fsutil.WriteFileAtomic(path, []byte(content), 0o644)
	if err == nil {
		return "", nil
	}
	var committed *fsutil.CommittedReplacementCleanupError
	if errors.As(err, &committed) {
		return "replacement committed, but backup cleanup failed", nil
	}
	return "", err
}
