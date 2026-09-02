//go:build !windows

package worktrees

import (
	"errors"
	"os"
	"syscall"
)

// osProcessAlive reports whether pid is a live process on POSIX. Signal 0
// does not deliver a signal; it only checks existence/permission. Only ESRCH
// and os.ErrProcessDone mean the process is gone; EPERM means it exists but
// we may not signal it (alive). Any other error is treated as alive
// (fail-closed) so an ambiguous answer can only keep a lease, never expire
// one.
func osProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		// FindProcess rarely fails on Unix; fail closed if it does.
		return true
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false
	}
	// EPERM and any other error: process may still exist.
	return true
}
