package execution

import (
	"os/exec"
)

// HardenCommandContext makes a context-bound command terminate its process
// tree and prevents inherited output handles from blocking Wait indefinitely.
// Call this before Start or Run.
func HardenCommandContext(command *exec.Cmd) {
	if command == nil {
		return
	}
	ConfigureProcessGroup(command)
	command.WaitDelay = processWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return KillProcessTree(command.Process.Pid)
	}
}
