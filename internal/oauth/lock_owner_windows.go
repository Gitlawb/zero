//go:build windows

package oauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// isLockCreateContention reports whether a failed O_EXCL lock create should be
// treated as contention (another holder's lock exists) rather than a hard
// error. On Windows, a concurrent holder's os.Remove leaves the lock file in a
// "delete pending" state, so the O_EXCL create races it with
// ERROR_ACCESS_DENIED (os.ErrPermission) rather than os.ErrExist; both are
// contention here.
func isLockCreateContention(err error) bool {
	return errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrPermission)
}

// checkOAuthLockDirOwner is a no-op on Windows: identityLockRoot resolves a
// per-user location from the process token rather than a shared root, so there
// is no co-tenant directory to validate ownership of.
func checkOAuthLockDirOwner(os.FileInfo) error {
	return nil
}

// identityLockRoot resolves the last-resort keyring lock directory from the
// caller's own identity instead of the environment. os.TempDir() is not usable
// here for the same reason the Unix branch refuses it: it resolves %TMP%, then
// %TEMP%, then %USERPROFILE%, and the first two are launcher-controlled, so two
// processes of one user can compute different lock paths while writing the same
// fixed keyring account and race it. Being per-user by default is not the
// property that matters; being stable for that user is.
//
// SHGetKnownFolderPath reads the user's profile location through the process
// token, so it ignores %LOCALAPPDATA% and every other temp override. Failing
// closed is deliberate: without a stable identity there is no lock path two
// processes are guaranteed to agree on, and a guessed one silently reintroduces
// the race it is meant to prevent.
func identityLockRoot() (string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", fmt.Errorf("resolve LocalAppData for keyring lock: %w", err)
	}
	dir := filepath.Join(base, "zero", "oauth-locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
