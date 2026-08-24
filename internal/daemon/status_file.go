package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Gitlawb/zero/internal/fsutil"
)

const statusTempPattern = ".daemon-status-*"

// statusFileCommittedError reports a warning that happened after the complete
// status document was already published. Callers must not tear down the daemon
// as though publication failed.
type statusFileCommittedError struct {
	cause error
}

func (err *statusFileCommittedError) Error() string {
	return fmt.Sprintf("status file publication committed with warning: %v", err.cause)
}

func (err *statusFileCommittedError) Unwrap() error {
	return err.cause
}

// writeStatusFileAtomically stages a complete, synced sibling file before
// publishing it over path, so it never truncates the live document in place.
// Unix replacement is observer-atomic; Windows uses fsutil's DACL-preserving
// replacement and may briefly leave the path absent, but never exposes partial
// contents.
func writeStatusFileAtomically(
	path string,
	data []byte,
	perm os.FileMode,
	replace func(src, dst string) error,
	syncParent func(dir string) error,
) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, statusTempPattern)
	if err != nil {
		return fmt.Errorf("create temporary status file: %w", err)
	}
	tempPath := temp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(perm); err != nil {
		return fmt.Errorf("set temporary status file permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary status file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary status file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary status file: %w", err)
	}
	closed = true

	var committedWarning error
	if err := fsutil.ReplaceWithRetry(tempPath, path, replace); err != nil {
		var committed *fsutil.CommittedReplacementCleanupError
		if !errors.As(err, &committed) {
			return fmt.Errorf("replace status file: %w", err)
		}
		committedWarning = fmt.Errorf("clean up replaced status file: %w", err)
	}
	if syncParent == nil {
		syncParent = syncStatusParentDir
	}
	if err := syncParent(dir); err != nil {
		committedWarning = errors.Join(committedWarning, fmt.Errorf("sync status directory: %w", err))
	}
	if committedWarning != nil {
		return &statusFileCommittedError{cause: committedWarning}
	}
	return nil
}

// syncStatusParentDir makes the replacement directory entry durable on
// platforms that support syncing directory handles. Windows replacement is
// best-effort durable because Go cannot fsync a directory there.
func syncStatusParentDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
