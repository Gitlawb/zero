//go:build !windows

package kimiidentity

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid is a live process on POSIX. Signal 0 does
// not deliver a signal; it only checks existence/permission.
//
// Fail closed: return false only when the process is established to be gone
// (ESRCH). nil and EPERM mean alive; any other error is treated as alive so
// an inconclusive liveness check never makes a repair lease reclaimable.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	// Only ESRCH proves the process is dead.
	return !errors.Is(err, syscall.ESRCH)
}
