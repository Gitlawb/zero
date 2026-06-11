//go:build !windows

package tools

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// bashWaitDelay bounds how long Wait blocks for the I/O pipes to drain after the
// process has exited or the context's Cancel has run. Without it, a backgrounded
// child that inherited the shell's stdout/stderr keeps those pipes open and
// Run() hangs long past the timeout waiting for EOF. Var (not const) so tests
// can shorten it.
var bashWaitDelay = 2 * time.Second

// hardenProcessLifetime makes a bash command killable as a single unit so a
// timeout cannot leak backgrounded children.
//
// Setpgid puts the shell into a new process group; since `sh -c` runs without
// job control, every process it forks (including `&` jobs) inherits that group.
// On context cancellation we signal the whole group (negative pid) instead of
// just the shell, so orphaned children die with it. WaitDelay is the backstop:
// if a child still holds the I/O pipes after the group is killed, Wait gives up
// rather than blocking forever.
func hardenProcessLifetime(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setpgid = true
	command.WaitDelay = bashWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		// Negative pid targets the process group led by the shell.
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
}
