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

	_ = TerminateCommand(cmd)
	if cmd.ProcessState == nil {
		t.Fatal("exited process was not reaped")
	}
}
