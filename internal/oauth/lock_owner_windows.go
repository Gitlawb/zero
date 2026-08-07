//go:build windows

package oauth

import "os"

// checkOAuthLockDirOwner is a no-op on Windows: the process temp directory is
// per-user by default, and keyringFallbackLockDir returns it directly.
func checkOAuthLockDirOwner(os.FileInfo) error {
	return nil
}
