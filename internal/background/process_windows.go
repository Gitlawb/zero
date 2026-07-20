//go:build windows

package background

import (
	"os/exec"

	"github.com/Gitlawb/zero/internal/execution"
)

// ConfigureChildProcessGroup is a no-op on Windows: terminateProcess uses
// `taskkill /T` to kill the whole process tree, so no launch-time process-group
// setup is required (the POSIX build sets Setpgid here instead).
func ConfigureChildProcessGroup(cmd *exec.Cmd) { execution.ConfigureProcessGroup(cmd) }

func terminateProcess(pid int) error {
	return execution.TerminateProcessTree(pid, 0, 0)
}
