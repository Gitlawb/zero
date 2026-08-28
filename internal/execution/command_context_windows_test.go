//go:build windows

package execution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const processStillActive = 259

func awaitProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for processIsActive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d is still running after command cancellation", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processIsActive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == processStillActive
}

func TestRunCommandPreservesDetachedChildAfterSuccessfulExit(t *testing.T) {
	switch os.Getenv("ZERO_SUCCESSFUL_COMMAND_TREE_HELPER") {
	case "root":
		nullFile, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			os.Exit(2)
		}
		defer nullFile.Close()
		child := exec.Command(os.Args[0], "-test.run=^TestRunCommandPreservesDetachedChildAfterSuccessfulExit$")
		child.Env = append(os.Environ(), "ZERO_SUCCESSFUL_COMMAND_TREE_HELPER=child")
		child.Stdin = nullFile
		child.Stdout = nullFile
		child.Stderr = nullFile
		child.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		}
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(os.Getenv("ZERO_SUCCESSFUL_COMMAND_TREE_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(4)
		}
		return
	case "child":
		time.Sleep(30 * time.Second)
		return
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx := context.Background()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunCommandPreservesDetachedChildAfterSuccessfulExit$")
	command.Env = append(os.Environ(),
		"ZERO_SUCCESSFUL_COMMAND_TREE_HELPER=root",
		"ZERO_SUCCESSFUL_COMMAND_TREE_PID_FILE="+pidFile,
	)
	if err := RunCommand(ctx, command); err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read detached child PID: %v", err)
	}
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		t.Fatalf("parse detached child PID %q: %v", pidData, err)
	}
	t.Cleanup(func() {
		if !processIsActive(pid) {
			return
		}
		process, findErr := os.FindProcess(pid)
		if findErr != nil {
			t.Errorf("find detached child %d: %v", pid, findErr)
			return
		}
		if killErr := process.Kill(); killErr != nil {
			t.Errorf("kill detached child %d: %v", pid, killErr)
			return
		}
		awaitProcessExit(t, pid)
	})
	if !processIsActive(pid) {
		t.Fatalf("successful RunCommand terminated detached child %d", pid)
	}
}
