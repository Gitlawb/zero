//go:build !windows

package worktrees

import (
	"errors"
	"os"
	"syscall"
)

// osProcessAlive reports whether pid is a live process on POSIX. Signal 0
// does not deliver a signal; it only checks existence/permission. ESRCH means
// no such process (dead); EPERM means it exists but we may not signal it
// (alive).
func osProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	return errors.Is(err, syscall.EPERM)
}
