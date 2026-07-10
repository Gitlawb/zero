//go:build !windows

package lockutil

import (
	"os"
)

// RestoreLockFile attempts to restore a sidelined lock file on non-Windows platforms.
// It uses os.Link to fail if the destination already exists.
func RestoreLockFile(reclaimed, path string) error {
	if err := os.Link(reclaimed, path); err != nil {
		return err
	}
	return os.Remove(reclaimed)
}
