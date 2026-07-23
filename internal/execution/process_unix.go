//go:build !windows

package execution

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// ConfigureProcessGroup makes cmd the leader of a process group so lifecycle
// operations cover descendants instead of orphaning them.
func ConfigureProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillProcessTree immediately kills pid and, when it is a group leader, its
// descendant process group.
func KillProcessTree(pid int) error {
	target, err := processSignalTarget(pid)
	if err != nil {
		return err
	}
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// TerminateProcessTree requests graceful termination, then force-kills the
// process tree after grace. Callers retain their distinct persistence models;
// this function owns only the OS lifecycle primitive.
func TerminateProcessTree(pid int, grace, poll time.Duration) error {
	target, err := processSignalTarget(pid)
	if err != nil {
		return err
	}
	alive := func() bool { return syscall.Kill(target, syscall.Signal(0)) == nil }
	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	if poll <= 0 {
		poll = 50 * time.Millisecond
	}
	if grace < 0 {
		grace = 0
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !alive() {
			return nil
		}
		time.Sleep(poll)
	}
	if !alive() {
		return nil
	}
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline = time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !alive() {
			return nil
		}
		time.Sleep(poll)
	}
	if alive() {
		return fmt.Errorf("process %d did not exit after SIGKILL", pid)
	}
	return nil
}

func processSignalTarget(pid int) (int, error) {
	if pid <= 1 {
		return 0, fmt.Errorf("refusing to signal invalid pid %d", pid)
	}
	target := pid
	if pgid, err := syscall.Getpgid(pid); err == nil {
		if pgid == pid {
			target = -pid
		}
	} else if errors.Is(err, syscall.ESRCH) {
		// Preserve the individual target; the signal call below treats ESRCH as
		// already gone, which is a successful lifecycle outcome.
		return pid, nil
	}
	return target, nil
}
