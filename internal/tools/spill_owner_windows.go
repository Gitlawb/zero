//go:build windows

package tools

import "os"

// checkSpillDirOwner is a no-op on Windows: %TEMP% is per-user by default and
// there is no portable uid to compare. The Lstat symlink/dir check in spillDir
// still applies.
func checkSpillDirOwner(os.FileInfo) error {
	return nil
}
