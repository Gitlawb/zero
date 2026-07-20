//go:build windows

package execution

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func ConfigureProcessGroup(cmd *exec.Cmd) {}

func KillProcessTree(pid int) error {
	if err := exec.Command(taskkillPath(), "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err == nil {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

func TerminateProcessTree(pid int, _, _ time.Duration) error { return KillProcessTree(pid) }

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
