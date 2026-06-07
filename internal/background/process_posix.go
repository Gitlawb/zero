//go:build !windows

package background

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// terminationGracePeriod is how long a process has to exit after SIGTERM before
// it is force-killed with SIGKILL. Vars (not consts) so tests can shorten them.
var (
	terminationGracePeriod  = 3 * time.Second
	terminationPollInterval = 50 * time.Millisecond
)

// terminateProcess stops a background process. It first asks politely with
// SIGTERM (so the process can flush/clean up), then escalates to SIGKILL if the
// process is still alive after terminationGracePeriod — so a process that traps
// or ignores SIGTERM cannot leak. It returns nil once the process is gone.
func terminateProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		if processGoneError(err) {
			return nil
		}
		return err
	}

	// Poll liveness so we return promptly once the process exits, rather than
	// always waiting out the full grace period.
	deadline := time.Now().Add(terminationGracePeriod)
	for time.Now().Before(deadline) {
		if !processAlive(process) {
			return nil
		}
		time.Sleep(terminationPollInterval)
	}
	if !processAlive(process) {
		return nil
	}

	// Still alive after the grace period: force-kill.
	if err := process.Kill(); err != nil && !processGoneError(err) {
		return err
	}
	return nil
}

// processAlive reports whether the process can still be signalled (signal 0 is
// the standard liveness probe — it performs error checking without sending a
// signal).
func processAlive(process *os.Process) bool {
	return process.Signal(syscall.Signal(0)) == nil
}

// processGoneError reports whether an error means the process has already exited
// (so termination is effectively done).
func processGoneError(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}
