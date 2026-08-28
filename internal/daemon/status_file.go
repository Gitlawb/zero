package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Gitlawb/zero/internal/fsutil"
)

const (
	statusTempPrefix  = ".daemon-status-"
	statusTempPattern = statusTempPrefix + "*"
)

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
// All operations after opening the parent use one traversal-resistant Root, so
// swapping a named ancestor cannot redirect replacement or cleanup.
func writeStatusFileAtomically(
	path string,
	data []byte,
	perm os.FileMode,
	beforeReplace func(),
	replace func(root *os.Root, src, dst string) error,
	syncParent func(root *os.Root) error,
) (returnErr error) {
	dir := filepath.Dir(path)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open status directory: %w", err)
	}
	committed := false
	defer func() {
		if err := root.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close status directory: %w", err))
		}
		if committed && returnErr != nil {
			var committedErr *statusFileCommittedError
			if !errors.As(returnErr, &committedErr) {
				returnErr = &statusFileCommittedError{cause: returnErr}
			}
		}
	}()
	if err := validateStatusRoot(root); err != nil {
		return err
	}

	temp, tempName, err := createStatusTemp(root, perm)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			if err := temp.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close temporary status file during cleanup: %w", err))
			}
			closed = true
		}
		if err := root.Remove(tempName); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary status file: %w", err))
		}
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
	closeErr := temp.Close()
	closed = true
	if closeErr != nil {
		return fmt.Errorf("close temporary status file: %w", closeErr)
	}
	if beforeReplace != nil {
		beforeReplace()
	}

	statusName := filepath.Base(path)
	rename := root.Rename
	if replace != nil {
		rename = func(src, dst string) error { return replace(root, src, dst) }
	}
	var committedWarning error
	if err := fsutil.RenameWithRetry(tempName, statusName, rename); err != nil {
		var committedReplacement *fsutil.CommittedReplacementCleanupError
		if !errors.As(err, &committedReplacement) {
			return fmt.Errorf("replace status file: %w", err)
		}
		committedWarning = fmt.Errorf("clean up replaced status file: %w", err)
	}
	committed = true
	if syncParent == nil {
		syncParent = syncStatusRoot
	}
	if err := syncParent(root); err != nil {
		committedWarning = errors.Join(committedWarning, fmt.Errorf("sync status directory: %w", err))
	}
	if committedWarning != nil {
		return committedWarning
	}
	return nil
}

func validateStatusRoot(root *os.Root) error {
	info, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect status directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("status directory is not a directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("status directory permissions are %04o, want owner-only", info.Mode().Perm())
	}
	if err := checkStatusDirOwner(root, info); err != nil {
		return err
	}
	return nil
}

func createStatusTemp(root *os.Root, perm os.FileMode) (*os.File, string, error) {
	for range 100 {
		var suffix [16]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, "", fmt.Errorf("generate temporary status file name: %w", err)
		}
		name := statusTempPrefix + hex.EncodeToString(suffix[:])
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return file, name, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", fmt.Errorf("create temporary status file: %w", err)
	}
	return nil, "", fmt.Errorf("create temporary status file: exhausted unique names")
}

// syncStatusRoot makes the replacement directory entry durable through the
// same bound root used for creation and replacement. Windows directory sync is
// best-effort because Go cannot fsync a directory there.
func syncStatusRoot(root *os.Root) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := root.Open(".")
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
