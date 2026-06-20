//go:build windows

package specialist

import (
	"os/exec"
	"time"
)

// specialistWaitDelay bounds how long Wait blocks for the child's stdout pipe to
// drain after the process exits or its context is cancelled, so a leaked
// grandchild holding the pipe cannot hang the parent past cancel/timeout. Var
// (not const) so tests can shorten it.
var specialistWaitDelay = 2 * time.Second

// hardenSpecialistChild sets WaitDelay so a leaked grandchild cannot block Wait
// indefinitely. Windows lacks POSIX process groups; tree-killing is not wired for
// the synchronous specialist path, so the default Cancel (Process.Kill) plus
// WaitDelay is used (M6).
func hardenSpecialistChild(command *exec.Cmd) {
	command.WaitDelay = specialistWaitDelay
}
