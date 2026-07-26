//go:build windows

package background

import (
	"os/exec"
	"testing"
	"time"
)

func TestTerminateCommandReapsExitedLeader(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Let the child exit without calling Wait so cleanup exercises the Windows
	// taskkill/TerminateProcess race while it still owns the process handle.
	time.Sleep(500 * time.Millisecond)

	// jatmn's #774 finding: taskkill /T racing an already-exited PID (and the
	// Process.Kill fallback) both fail here, but the reap below still succeeds —
	// that's authoritative proof the leader is gone, so TerminateCommand must
	// report success rather than surfacing the earlier kill-attempt error.
	if err := TerminateCommand(cmd); err != nil {
		t.Fatalf("TerminateCommand: %v, want nil (leader was already gone and got reaped)", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("exited process was not reaped")
	}
}
