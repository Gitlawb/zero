//go:build windows

package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

// bashWaitDelay bounds how long Wait blocks for the I/O pipes to drain after the
// process has exited or the context's Cancel has run, so a backgrounded child
// holding the pipes cannot make Run() hang past the timeout. Var (not const) so
// tests can shorten it.
var bashWaitDelay = 2 * time.Second

// hardenProcessLifetime makes a Windows shell command killable as a process
// tree. cmd.exe starts helper commands as child processes, so killing only the
// shell can leave a long-running child alive and holding cwd/temp handles after
// Zero exits.
func hardenProcessLifetime(command *exec.Cmd) {
	command.WaitDelay = bashWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		taskkill := taskkillPath()
		_ = exec.Command(taskkill, "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run()
		return nil
	}
}

func taskkillPath() string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = os.Getenv("windir")
	}
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	return filepath.Join(systemRoot, "System32", "taskkill.exe")
}

// applyWindowsShellCommandLine overrides command's raw child command line so
// commandText reaches cmd.exe unescaped instead of auto-quoted the way
// exec.Cmd would normally encode a single Args element. Skipped when wrapped
// is true: the sandbox engine then routes execution through a separate
// zero-windows-command-runner process, which builds its own child command
// line from scratch (internal/sandbox/windows_process_windows.go) rather than
// inheriting whatever this outer exec.Cmd is configured with.
func applyWindowsShellCommandLine(command *exec.Cmd, commandText string, wrapped bool) {
	if wrapped {
		return
	}
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: zeroSandbox.WindowsShellCommandLine(commandText)}
}
