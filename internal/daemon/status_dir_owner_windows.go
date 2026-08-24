//go:build windows

package daemon

import "os"

// Windows has no portable uid to compare. os.Root still binds every operation
// to one directory handle and rejects reparse-point traversal, while the normal
// daemon directory lives below the current user's profile.
func checkStatusDirOwner(os.FileInfo) error {
	return nil
}
