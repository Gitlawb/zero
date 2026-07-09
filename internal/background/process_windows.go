//go:build windows

package background

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// ConfigureChildProcessGroup is a no-op on Windows: terminateProcess uses
// `taskkill /T` to kill the whole process tree, so no launch-time process-group
// setup is required (the POSIX build sets Setpgid here instead).
func ConfigureChildProcessGroup(cmd *exec.Cmd) {}

func terminateProcess(pid int) error {
	taskkill := taskkillPath()
	if err := exec.Command(taskkill, "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err == nil {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
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
