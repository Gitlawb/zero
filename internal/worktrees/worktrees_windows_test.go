//go:build windows

package worktrees

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOpenProcessErrorMeansAliveOnAccessDenied(t *testing.T) {
	if !openProcessErrorMeansAlive(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("ERROR_ACCESS_DENIED must be treated as alive: the PID could belong to a live process this caller lacks rights to query")
	}
}

func TestOpenProcessErrorMeansAliveOnInvalidParameter(t *testing.T) {
	if openProcessErrorMeansAlive(windows.ERROR_INVALID_PARAMETER) {
		t.Fatal("ERROR_INVALID_PARAMETER means no such process; must be treated as dead")
	}
}

func TestOsProcessAliveReportsLiveSelf(t *testing.T) {
	if !osProcessAlive(os.Getpid()) {
		t.Fatal("current process must report alive")
	}
}

func TestOsProcessAliveReportsDeadAfterExit(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "exit", "0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for child process: %v", err)
	}
	if osProcessAlive(pid) {
		t.Fatal("exited process must not report alive")
	}
}
