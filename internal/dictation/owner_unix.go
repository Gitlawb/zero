//go:build !windows

package dictation

import (
	"fmt"
	"os"
	"syscall"
)

// checkAudioDirOwner rejects a dictation temp directory not owned by the
// current user — on a shared /tmp another user could have pre-created the
// path and would then control its lifetime. Mirrors
// internal/tools/spill_owner_unix.go.
func checkAudioDirOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("dictation temp directory is owned by uid %d, not the current user", stat.Uid)
	}
	return nil
}
