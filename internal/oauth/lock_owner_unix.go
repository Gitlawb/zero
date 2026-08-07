//go:build !windows

package oauth

import (
	"fmt"
	"os"
	"syscall"
)

// checkOAuthLockDirOwner rejects a fallback lock directory not owned by the
// current user: on a shared temp root another user could have pre-created the
// path and would then control its lifetime (deletion/renaming), permanently
// denying OAuth keyring operations.
func checkOAuthLockDirOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("oauth lock fallback directory is owned by uid %d, not the current user", stat.Uid)
	}
	return nil
}
