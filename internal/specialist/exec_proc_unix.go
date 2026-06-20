//go:build !windows

package specialist

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// specialistWaitDelay bounds how long Wait blocks for the child's stdout pipe to
// drain after the process exits or its context is cancelled, so a leaked
// grandchild holding the pipe cannot hang the parent past cancel/timeout. Var
// (not const) so tests can shorten it.
var specialistWaitDelay = 2 * time.Second

// hardenSpecialistChild makes a foreground specialist child killable as a single
// unit. Setpgid puts the child into its own process group, so on cancel/timeout
// we signal the whole group (negative pid) — any build/server/bash the sub-agent
// forked dies with it instead of being orphaned. WaitDelay is the backstop if a
// grandchild still holds the stdout pipe after the group is killed. Must be
// called before command.Start (M6).
func hardenSpecialistChild(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setpgid = true
	command.WaitDelay = specialistWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		// Negative pid targets the whole process group led by the child.
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
}
