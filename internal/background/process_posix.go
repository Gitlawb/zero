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

// terminateProcess stops a background process group. It first asks politely with
// SIGTERM (so processes can flush/clean up), then escalates to SIGKILL if the
// group is still alive after terminationGracePeriod — so a process that traps or
// ignores SIGTERM cannot leak. Signalling the negative PID targets the whole
// process group created by ConfigureChildProcessGroup, so forked children die
// with the leader instead of being orphaned. It returns nil once the group is
// gone.
func terminateProcess(pid int) error {
	// Guard pid <= 1: kill(-0) would target our OWN process group and kill(-1)
	// every process we can signal. A real child PID is always > 1, so never let a
	// bogus 0/1 expand into either.
	if pid <= 1 {
		return fmt.Errorf("refusing to terminate invalid pid %d", pid)
	}

	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if processGoneError(err) {
			return nil
		}
		return err
	}

	// Poll liveness so we return promptly once the group exits, rather than
	// always waiting out the full grace period.
	deadline := time.Now().Add(terminationGracePeriod)
	for time.Now().Before(deadline) {
		if !processGroupAlive(pid) {
			return nil
		}
		time.Sleep(terminationPollInterval)
	}
	if !processGroupAlive(pid) {
		return nil
	}

	// Still alive after the grace period: force-kill the group.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !processGoneError(err) {
		return err
	}

	// SIGKILL is asynchronous: the kernel may not have reaped the group yet.
	// Poll again so this helper only reports success once the group is actually
	// gone — otherwise the caller gets nil while descendants are still racing.
	deadline = time.Now().Add(terminationGracePeriod)
	for time.Now().Before(deadline) {
		if !processGroupAlive(pid) {
			return nil
		}
		time.Sleep(terminationPollInterval)
	}
	if processGroupAlive(pid) {
		return fmt.Errorf("process group %d did not exit after SIGKILL", pid)
	}
	return nil
}

// processGroupAlive reports whether any process in the group still exists (signal
// 0 performs error checking without delivering a signal).
func processGroupAlive(pid int) bool {
	return syscall.Kill(-pid, syscall.Signal(0)) == nil
}

// processGoneError reports whether an error means the process group has already
// exited (so termination is effectively done). syscall.Kill reports ESRCH when
// no process in the target group remains.
func processGoneError(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}
