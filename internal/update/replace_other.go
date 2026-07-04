//go:build !windows

package update

import "os"

// replaceBinary installs newPath over targetPath. On POSIX systems renaming
// over a running executable is safe: the process currently executing it keeps
// its open inode, and the rename is atomic within the same filesystem.
func replaceBinary(targetPath string, newPath string) error {
	if err := os.Chmod(newPath, 0o755); err != nil {
		return err
	}
	return os.Rename(newPath, targetPath)
}

// CleanupStaleBinary is a no-op outside Windows, which is the only platform
// that requires renaming a running binary aside instead of replacing it directly.
func CleanupStaleBinary(targetPath string) {}
