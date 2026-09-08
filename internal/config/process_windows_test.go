//go:build windows

package config

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// TestResumeMainThreadReportsFailureForUnknownPID covers the contract
// attachCommandProcess relies on: when no thread belonging to pid can be
// found (here, because the pid doesn't correspond to a live process),
// resumeMainThread must report false rather than silently doing nothing,
// so a caller can react (e.g. terminate the process) instead of leaving it
// suspended indefinitely.
func TestResumeMainThreadReportsFailureForUnknownPID(t *testing.T) {
	if resumeMainThread(0) {
		t.Fatal("resumeMainThread(0) = true, want false for a pid with no threads")
	}
}

func TestCommandProcessRetainsIdentityAfterWait(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "exit /b 0")
	configureCommandProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	proc := attachCommandProcess(cmd)
	t.Cleanup(proc.Close)
	if proc.processHandle == 0 {
		t.Fatal("attachCommandProcess did not retain a process handle")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	var code uint32
	if err := windows.GetExitCodeProcess(proc.processHandle, &code); err != nil {
		t.Fatalf("GetExitCodeProcess after Wait: %v", err)
	}
	if code == stillActive {
		t.Fatalf("exit code = STILL_ACTIVE after Wait")
	}

	proc.Close()
	if proc.job != 0 || proc.processHandle != 0 {
		t.Fatalf("Close left handles open: job=%v process=%v", proc.job, proc.processHandle)
	}
}

func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}
