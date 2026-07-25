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
