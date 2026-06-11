//go:build windows

package tools

import (
	"os/exec"
	"time"
)

// bashWaitDelay bounds how long Wait blocks for the I/O pipes to drain after the
// process has exited or the context's Cancel has run, so a backgrounded child
// holding the pipes cannot make Run() hang past the timeout. Var (not const) so
// tests can shorten it.
var bashWaitDelay = 2 * time.Second

// hardenProcessLifetime sets WaitDelay so a leaked child cannot block Wait
// indefinitely. Windows lacks POSIX process groups; tree-killing the shell's
// descendants (taskkill /T) is not wired for the synchronous bash path, so the
// default Cancel (Process.Kill on the shell) plus WaitDelay is used.
func hardenProcessLifetime(command *exec.Cmd) {
	command.WaitDelay = bashWaitDelay
}
