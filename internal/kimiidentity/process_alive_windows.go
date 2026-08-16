//go:build windows

package kimiidentity

import (
	"errors"

	"golang.org/x/sys/windows"
)

// processAlive reports whether pid is a live process on Windows. It opens the
// process with SYNCHRONIZE and probes termination via WaitForSingleObject with
// a zero timeout. Exit-code checks are not used: a terminated process can
// legitimately return exit code 259 (STILL_ACTIVE), which would look alive.
//
// Fail closed on inconclusive results: only a proven-dead process makes a
// repair lease reclaimable. Access-denied means the process exists but we
// cannot open it (alive). ERROR_INVALID_PARAMETER is the usual free/invalid
// PID signal (dead). Any other OpenProcess failure, WaitForSingleObject
// failure, or unexpected wait result is treated as alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// Access denied: process exists under a tighter ACL (or higher
		// integrity). Do not reclaim its repair lock.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return true
		}
		// Free/invalid PID typically returns ERROR_INVALID_PARAMETER.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false
		}
		// Unknown open failure: fail closed (do not reclaim).
		return true
	}
	defer windows.CloseHandle(handle)

	// Zero timeout: WAIT_OBJECT_0 if already terminated, WAIT_TIMEOUT if still
	// running. WaitForSingleObject only returns err on WAIT_FAILED.
	event, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return true
	}
	switch event {
	case windows.WAIT_OBJECT_0:
		return false // signaled = process has exited
	case uint32(windows.WAIT_TIMEOUT):
		return true // still running
	default:
		// Unexpected wait result: fail closed.
		return true
	}
}
