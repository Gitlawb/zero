//go:build !windows

package background

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// terminationGracePeriod is how long a process has to exit after SIGTERM before
// it is force-killed with SIGKILL. Vars (not consts) so tests can shorten them.
var (
	terminationGracePeriod  = 3 * time.Second
	terminationPollInterval = 50 * time.Millisecond
)

// ConfigureChildProcessGroup puts a child into its own process group so the whole
// group can be signalled as a unit. terminateProcess depends on this: it signals
// the negative PID (the group), so any process the child forks dies with it
// instead of being orphaned. Must be called before cmd.Start.
func ConfigureChildProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminateProcess stops a background process. It first asks politely with
// SIGTERM (so processes can flush/clean up), then escalates to SIGKILL if it is
// still alive after terminationGracePeriod — so a process that traps or ignores
// SIGTERM cannot leak. It returns nil once the target is gone.
//
// When pid is its own process-group leader (the invariant ConfigureChildProcessGroup
// establishes for our children, pgid == pid), the whole group is signalled via the
// negative PID, so forked children die with the leader instead of being orphaned.
// If pid is NOT a leader, its group is some other group (possibly OUR OWN), so
// signalling -pgid there could kill unrelated processes — in that case we fall
// back to signalling only the individual PID, which also avoids reporting a false
// success when a non-leader group-signal returns ESRCH.
func terminateProcess(pid int) error {
	// Guard pid <= 1: kill(-0) would target our OWN process group and kill(-1)
	// every process we can signal. A real child PID is always > 1, so never let a
	// bogus 0/1 expand into either.
	if pid <= 1 {
		return fmt.Errorf("refusing to terminate invalid pid %d", pid)
	}

	// Signal the whole group only when pid leads its own group; otherwise the
	// individual process. See the doc comment for why a non-leader is signalled
	// directly rather than via its (foreign) group.
	target := pid
	if pgid, err := syscall.Getpgid(pid); err == nil {
		if pgid == pid {
			target = -pid
		}
	} else if processGoneError(err) {
		return nil // already gone
	}

	alive := func() bool { return syscall.Kill(target, syscall.Signal(0)) == nil }

	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		if processGoneError(err) {
			return nil
		}
		return err
	}

	// Poll liveness so we return promptly once it exits, rather than always
	// waiting out the full grace period.
	deadline := time.Now().Add(terminationGracePeriod)
	for time.Now().Before(deadline) {
		if !alive() {
			return nil
		}
		time.Sleep(terminationPollInterval)
	}
	if !alive() {
		return nil
	}

	// Still alive after the grace period: force-kill.
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !processGoneError(err) {
		return err
	}

	// SIGKILL is asynchronous: the kernel may not have reaped it yet. Poll again
	// so this helper only reports success once the target is actually gone —
	// otherwise the caller gets nil while descendants are still racing.
	deadline = time.Now().Add(terminationGracePeriod)
	for time.Now().Before(deadline) {
		if !alive() {
			return nil
		}
		time.Sleep(terminationPollInterval)
	}
	if alive() {
		return fmt.Errorf("process %d did not exit after SIGKILL", pid)
	}
	return nil
}

// processGoneError reports whether an error means the process group has already
// exited (so termination is effectively done). syscall.Kill reports ESRCH when
// no process in the target group remains.
func processGoneError(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}
