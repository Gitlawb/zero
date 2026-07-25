//go:build !windows

package background

import (
	"os/exec"
	"time"

	"github.com/Gitlawb/zero/internal/execution"
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
	execution.ConfigureProcessGroup(cmd)
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
	return execution.TerminateProcessTree(pid, terminationGracePeriod, terminationPollInterval)
}
