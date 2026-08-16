//go:build windows

package kimiidentity

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// TestProcessAliveExitCode259IsDead is the regression for treating STILL_ACTIVE
// (259) as a process exit code rather than a liveness flag. A dead child that
// exits with 259 must be reported as not alive so a stale repair lease can be
// reclaimed.
func TestProcessAliveExitCode259IsDead(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "exit /b 259")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := cmd.Process.Pid
	waitRan := false
	defer func() {
		if !waitRan {
			if err := cmd.Process.Kill(); err != nil {
				t.Errorf("Kill cleanup: %v", err)
			}
			if err := cmd.Wait(); err != nil {
				t.Errorf("Wait cleanup: %v", err)
			}
		}
	}()

	// cmd.Wait() can close the process handle held by cmd.Process, and once no
	// handle to the process remains, OpenProcess(pid) fails with
	// ERROR_INVALID_PARAMETER, which would take the invalid-PID branch instead
	// of the WAIT_OBJECT_0 branch this test must exercise for a terminated
	// process.
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess: %v", err)
	}
	if _, err := windows.WaitForSingleObject(handle, windows.INFINITE); err != nil {
		_ = windows.CloseHandle(handle)
		t.Fatalf("WaitForSingleObject: %v", err)
	}
	if processAlive(pid) {
		_ = windows.CloseHandle(handle)
		t.Fatalf("processAlive(%d) = true after exit code 259; want false (dead)", pid)
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Errorf("CloseHandle: %v", err)
	}

	waitRan = true
	err = cmd.Wait()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 259 {
		t.Fatalf("Wait: %v (want ExitError with code 259)", err)
	}
}

func TestProcessAliveSelfIsLive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("processAlive(self) = false; want true")
	}
}

func TestProcessAliveNonPositiveIsDead(t *testing.T) {
	if processAlive(0) {
		t.Fatal("processAlive(0) = true; want false")
	}
	if processAlive(-1) {
		t.Fatal("processAlive(-1) = true; want false")
	}
}

func TestProcessAliveRunningChildIsLive(t *testing.T) {
	cmd := exec.Command("powershell", "-Command", "Start-Sleep -Seconds 10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := cmd.Process.Pid
	waitRan := false
	// Keep our own SYNCHRONIZE handle open across the post-kill probe:
	// cmd.Wait() can close the process handle held by cmd.Process, and without
	// a retained handle OpenProcess(pid) could fail with
	// ERROR_INVALID_PARAMETER, taking the invalid-PID branch instead of the
	// WAIT_OBJECT_0 branch this test must exercise for a killed process.
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess: %v", err)
	}
	defer func() {
		if err := windows.CloseHandle(handle); err != nil {
			t.Errorf("CloseHandle: %v", err)
		}
	}()
	defer func() {
		if !waitRan {
			if err := cmd.Process.Kill(); err != nil {
				t.Errorf("Kill cleanup: %v", err)
			}
			waitRan = true
			if err := cmd.Wait(); err != nil {
				t.Errorf("Wait cleanup: %v", err)
			}
		}
	}()

	if !processAlive(pid) {
		t.Fatalf("processAlive(%d) = false while running; want true", pid)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Errorf("Kill: %v", err)
	}
	if _, err := windows.WaitForSingleObject(handle, windows.INFINITE); err != nil {
		t.Fatalf("WaitForSingleObject: %v", err)
	}
	if processAlive(pid) {
		t.Fatalf("processAlive(%d) = true after kill; want false (dead)", pid)
	}
	waitRan = true
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Errorf("Wait: %v", err)
		}
	}
}
