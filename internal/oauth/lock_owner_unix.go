//go:build !windows

package oauth

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// isLockCreateContention reports whether a failed O_EXCL lock create should be
// treated as contention (another holder's lock exists) rather than a hard
// error. On non-Windows platforms the only contention errno is EEXIST
// (os.ErrExist); EACCES (os.ErrPermission) is a genuine permission failure and
// must surface immediately rather than spin to the lock timeout.
func isLockCreateContention(err error) bool {
	return errors.Is(err, os.ErrExist)
}

// checkOAuthLockDirOwner rejects a fallback lock directory not owned by the
// current user: on a shared temp root another user could have pre-created the
// path and would then control its lifetime (deletion/renaming), permanently
// denying OAuth keyring operations.
func checkOAuthLockDirOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("oauth lock fallback directory ownership metadata unavailable")
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("oauth lock fallback directory is owned by uid %d, not the current user", stat.Uid)
	}
	return nil
}

// identityLockRoot is never called on non-Windows: keyringFallbackLockDir
// resolves the uid-anchored home and the fixed /tmp root directly. It exists so
// the shared fallback can name one helper without a build-tagged call site.
func identityLockRoot() (string, error) {
	return "", fmt.Errorf("oauth: identityLockRoot is windows-only")
}
