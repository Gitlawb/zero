//go:build !windows

package tools

import (
	"fmt"
	"os"
	"syscall"
)

// checkSpillDirOwner rejects a spill directory not owned by the current user:
// on a shared /tmp another user could have pre-created the path and would then
// control its lifetime (deletion/renaming) even though the 0600 spill files
// keep their contents private.
func checkSpillDirOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("spill directory is owned by uid %d, not the current user", stat.Uid)
	}
	return nil
}
