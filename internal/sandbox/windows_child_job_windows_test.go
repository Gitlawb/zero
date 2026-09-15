//go:build windows

package sandbox

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A CHILD CREATED AFTER THE JOB IS JOINED DIES WITH THE HELPER.
//
// This is the property the orphaned-suspended-child fix rests on, driven
// against the real Windows kernel rather than argued from the flag name: join
// the kill-on-close job, create a child the way the helper does (suspended, so
// it has executed nothing and cannot exit on its own), then close the last job
// handle the way TerminateProcess on the helper would, and require the child to
// be gone.
//
// Without the job, a suspended child survives its parent indefinitely, which is
// exactly the leak this pins: it holds the inherited pipe handles and a reader
// waits forever.
func TestJoiningTheKillJobTakesASuspendedChildDownWithIt(t *testing.T) {
	job, err := joinWindowsChildKillJob()
	if err != nil {
		t.Skipf("kill-on-close job unavailable on this host: %v", err)
	}

	// Created suspended, like the sandboxed child between CreateProcessAsUser
	// and ResumeThread: it exists and has run nothing.
	command := exec.Command(os.Getenv("COMSPEC"), "/c", "ver")
	command.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := command.Start(); err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("start suspended child: %v", err)
	}
	pid := uint32(command.Process.Pid)
	// Never leave one behind, whatever this test concludes.
	t.Cleanup(func() { _ = command.Process.Kill() })

	if alive, err := windowsProcessAlive(pid); err != nil || !alive {
		_ = windows.CloseHandle(job)
		t.Fatalf("suspended child is not alive before the job closes: alive=%v err=%v", alive, err)
	}

	// The helper being terminated closes its handles. That is the whole
	// mechanism: nothing in the helper gets to run.
	if err := windows.CloseHandle(job); err != nil {
		t.Fatalf("close job: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		alive, err := windowsProcessAlive(pid)
		if err != nil {
			t.Fatalf("query child: %v", err)
		}
		if !alive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the suspended child outlived the job, so a terminated helper would leave it holding the inherited pipes")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// windowsProcessAlive reports whether pid still exists. A suspended process is
// alive, so WaitForSingleObject returning WAIT_TIMEOUT is the positive answer.
func windowsProcessAlive(pid uint32) (bool, error) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		// The process is gone, which is what the caller is asking about.
		return false, nil
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false, err
	}
	return state == uint32(windows.WAIT_TIMEOUT), nil
}
