//go:build !windows

package update

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const stagingDirPrefix = ".zero-stage-"

// openStagingDirectory is a test seam for the creation-to-open race.
var openStagingDirectory = func(parentFD int, name string) (int, error) {
	return unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}

// createStagedBinary creates a private directory next to targetPath, binds that
// directory and its parent to descriptors, and creates the staged file through
// the directory descriptor. No later staging operation re-walks the directory
// pathname.
func createStagedBinary(targetPath string) (*stagedBinary, error) {
	parentPath := filepath.Dir(targetPath)
	parentHandle, err := os.Open(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open staging parent: %w", err)
	}
	dir, err := os.MkdirTemp(parentPath, stagingDirPrefix)
	if err != nil {
		_ = parentHandle.Close()
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	createdInfo, err := os.Lstat(dir)
	if err != nil {
		_ = parentHandle.Close()
		return nil, fmt.Errorf("stat new staging directory: %w", err)
	}
	dirHandle, err := openAndVerifyStagingDirectory(parentHandle, filepath.Base(dir), createdInfo)
	if err != nil {
		_ = parentHandle.Close()
		return nil, fmt.Errorf("open staging directory: %w", err)
	}
	path := filepath.Join(dir, filepath.Base(targetPath))
	file, err := createStagingFileAt(dirHandle, filepath.Base(path), path)
	if err != nil {
		(&stagedBinary{
			path:         path,
			dir:          dir,
			dirHandle:    dirHandle,
			parentHandle: parentHandle,
		}).discardPaths()
		return nil, err
	}
	return &stagedBinary{
		file:         file,
		path:         path,
		dir:          dir,
		dirHandle:    dirHandle,
		parentHandle: parentHandle,
	}, nil
}

func openAndVerifyStagingDirectory(parent *os.File, name string, createdInfo os.FileInfo) (*os.File, error) {
	fd, err := openStagingDirectory(int(parent.Fd()), name)
	if err != nil {
		return nil, err
	}
	handle := os.NewFile(uintptr(fd), name)
	handleInfo, err := handle.Stat()
	if err != nil {
		_ = handle.Close()
		return nil, err
	}
	stat, ok := handleInfo.Sys().(*unix.Stat_t)
	if !ok ||
		!os.SameFile(createdInfo, handleInfo) ||
		handleInfo.Mode().Perm() != 0o700 ||
		int(stat.Uid) != os.Geteuid() {
		_ = handle.Close()
		return nil, fmt.Errorf("staging directory entry was replaced before it could be bound")
	}
	return handle, nil
}

// refreshLiveness is intentionally a no-op. Crash leftovers cannot be
// authenticated as updater-owned, so CleanupStaleBinary preserves them.
func (staged *stagedBinary) refreshLiveness() {}

// createStagingFile remains the direct-path primitive exercised by the link
// regression tests.
func createStagingFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
}

func createStagingFileAt(dir *os.File, name string, displayPath string) (*os.File, error) {
	fd, err := unix.Openat(
		int(dir.Fd()),
		name,
		unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o755,
	)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", displayPath, err)
	}
	return os.NewFile(uintptr(fd), displayPath), nil
}

func (staged *stagedBinary) promote(targetPath string) error {
	if err := staged.file.Chmod(0o755); err != nil {
		return err
	}
	if err := staged.verifyStagedIdentity(); err != nil {
		return err
	}
	if err := unix.Renameat(
		int(staged.dirHandle.Fd()),
		filepath.Base(staged.path),
		unix.AT_FDCWD,
		targetPath,
	); err != nil {
		return fmt.Errorf("rename staged binary onto %s: %w", targetPath, err)
	}
	staged.path = targetPath
	staged.promoted = true
	return nil
}

func (staged *stagedBinary) verifyStagedIdentity() error {
	var handleStat unix.Stat_t
	if err := unix.Fstat(int(staged.file.Fd()), &handleStat); err != nil {
		return fmt.Errorf("stat staged binary: %w", err)
	}
	var childStat unix.Stat_t
	if err := unix.Fstatat(
		int(staged.dirHandle.Fd()),
		filepath.Base(staged.path),
		&childStat,
		unix.AT_SYMLINK_NOFOLLOW,
	); err != nil {
		return fmt.Errorf("stat staged binary path %s: %w", staged.path, err)
	}
	if childStat.Ino != handleStat.Ino || childStat.Dev != handleStat.Dev {
		return fmt.Errorf("staged binary %s was replaced after it was written", staged.path)
	}
	return nil
}

// CleanupStaleBinary preserves random staging directories because their public
// filename shape is not proof that this updater created them.
func CleanupStaleBinary(targetPath string) {}

// discardPaths removes the child through the bound directory descriptor and
// removes the directory only while its original parent entry still names it.
func (staged *stagedBinary) discardPaths() {
	if staged.dirHandle != nil && !staged.promoted {
		_ = unix.Unlinkat(int(staged.dirHandle.Fd()), filepath.Base(staged.path), 0)
	}
	if staged.dirHandle != nil {
		var bound unix.Stat_t
		var current unix.Stat_t
		boundErr := unix.Fstat(int(staged.dirHandle.Fd()), &bound)
		currentErr := unix.Fstatat(
			int(staged.parentHandle.Fd()),
			filepath.Base(staged.dir),
			&current,
			unix.AT_SYMLINK_NOFOLLOW,
		)
		_ = staged.dirHandle.Close()
		if boundErr == nil &&
			currentErr == nil &&
			bound.Dev == current.Dev &&
			bound.Ino == current.Ino {
			_ = unix.Unlinkat(
				int(staged.parentHandle.Fd()),
				filepath.Base(staged.dir),
				unix.AT_REMOVEDIR,
			)
		}
	}
	if staged.parentHandle != nil {
		_ = staged.parentHandle.Close()
	}
}
