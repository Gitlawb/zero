//go:build windows

package background

import (
	"errors"
	"fmt"
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

func terminateCommand(cmd *exec.Cmd) error {
	taskkillErr := exec.Command(taskkillPath(), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	var killErr error
	if taskkillErr != nil {
		killErr = cmd.Process.Kill()
	}
	if err := waitForTerminatedCommandWithin(cmd, commandReapTimeout); err != nil {
		return err
	}
	if taskkillErr == nil {
		return nil
	}
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return fmt.Errorf("terminate process tree: %v (fallback kill failed: %w)", taskkillErr, killErr)
	}
	// ErrProcessDone proves only that the leader is gone. Keep the taskkill
	// failure actionable because its former descendants cannot be verified by
	// PID after the leader exits.
	return fmt.Errorf("terminate process tree: %w", taskkillErr)
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
