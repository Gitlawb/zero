//go:build windows

package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func configureCommandProcess(cmd *exec.Cmd) {}

func terminateCommandProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	taskkill := taskkillPath()
	_ = exec.Command(taskkill, "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
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
