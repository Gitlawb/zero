package tools

import (
	"errors"
	"io"
	"os"

	"github.com/Gitlawb/zero/internal/fsutil"
)

var errFileChangedDuringWrite = errors.New("file changed on disk before the write committed")

// errSymlinkDestination refuses any write whose destination is, or has become,
// a symbolic link. Observation and validation inspect the link (Lstat) and the
// publication must replace the very object it validated: replacing the symlink
// itself would strand the pointed-to file with stale bytes while destroying the
// link. Failing closed is the only consistent contract.
var errSymlinkDestination = errors.New("refusing to write through a symbolic link")

// fileWriteBeforeCommit is a deterministic test hook. Production leaves it
// nil; tests use it to replace a path after observation but before opening the
// object that will actually be mutated.
var fileWriteBeforeCommit func(path string)

// fileCreateBeforeExclusivePublish is a deterministic test hook. It runs after
// commitFileContents has observed the path missing but before the exclusive
// publication, so a test can prove a file appearing in that window is refused
// rather than overwritten.
var fileCreateBeforeExclusivePublish func(path string)

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
// appeared after the caller observed it missing, and publishes with an atomic
// no-replace primitive so a racing creator is refused rather than overwritten.
// A symlink destination is refused in both branches.
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

	pathInfo, statErr := os.Lstat(path)
	if statErr == nil {
		if pathInfo.Mode()&os.ModeSymlink != 0 {
			return "", errSymlinkDestination
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}

	if expectedInfo == nil {
		if statErr == nil {
			return "", errFileChangedDuringWrite
		}
		if fileCreateBeforeExclusivePublish != nil {
			fileCreateBeforeExclusivePublish(path)
		}
		return publishFileContentsExclusive(path, content)
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
	pathInfo, err = os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, pathInfo) {
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

// publishFileContentsExclusive creates the destination with an atomic no-replace
// publication. A destination that already exists, or that appears concurrently,
// is reported as errFileChangedDuringWrite instead of being overwritten.
func publishFileContentsExclusive(path, content string) (string, error) {
	err := fsutil.WriteFileAtomicExclusive(path, []byte(content), 0o644)
	if err == nil {
		return "", nil
	}
	if errors.Is(err, os.ErrExist) {
		return "", errFileChangedDuringWrite
	}
	return "", err
}
