//go:build windows

package background

import (
	"os/exec"

	"github.com/Gitlawb/zero/internal/execution"
)

// ConfigureChildProcessGroup is a no-op on Windows: process-tree termination is
// delegated to execution.TerminateProcessTree, so no launch-time process-group
// setup is required (the POSIX build sets Setpgid here instead).
func ConfigureChildProcessGroup(cmd *exec.Cmd) { execution.ConfigureProcessGroup(cmd) }

func terminateProcess(pid int) error {
	return execution.TerminateProcessTree(pid, 0, 0)
}

// terminateOwnedProcess terminates cmd's process. Windows has no process-group
// rediscovery concern — KillProcessTree always operates on the whole process
// tree via taskkill /T regardless of how the PID was obtained — so this is the
// same as terminateProcess.
func terminateOwnedProcess(cmd *exec.Cmd) error {
	return terminateProcess(cmd.Process.Pid)
}
