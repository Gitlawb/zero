//go:build windows

package execution

import (
	"context"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRunCommandContinuesWhenJobAssignmentFails(t *testing.T) {
	originalAssign := assignCommandProcessToJob
	assignCommandProcessToJob = func(windows.Handle, windows.Handle) error {
		return windows.ERROR_ACCESS_DENIED
	}
	t.Cleanup(func() { assignCommandProcessToJob = originalAssign })

	ctx := context.Background()
	command := exec.CommandContext(ctx, "cmd", "/C", "exit /b 0")
	if err := RunCommand(ctx, command); err != nil {
		t.Fatalf("RunCommand failed after optional job assignment failed: %v", err)
	}
}

func TestCommandTreeFallbackDoesNotTargetExitedProcessPID(t *testing.T) {
	originalAssign := assignCommandProcessToJob
	assignCommandProcessToJob = func(windows.Handle, windows.Handle) error {
		return windows.ERROR_ACCESS_DENIED
	}
	t.Cleanup(func() { assignCommandProcessToJob = originalAssign })

	taskkillCalls := 0
	originalCancelByPID := cancelCommandTreeByPID
	cancelCommandTreeByPID = func(int) error {
		taskkillCalls++
		return nil
	}
	t.Cleanup(func() { cancelCommandTreeByPID = originalCancelByPID })

	command := exec.Command("cmd", "/C", "exit /b 0")
	tree, err := prepareCommandTree(command)
	if err != nil {
		t.Fatalf("prepareCommandTree: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := tree.close(); closeErr != nil {
			t.Errorf("close command tree: %v", closeErr)
		}
	})
	if err := command.Start(); err != nil {
		_ = tree.attach(nil)
		t.Fatalf("Start: %v", err)
	}
	if err := tree.attach(command.Process); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("attachCommandTree: %v", err)
	}
	if tree.contained {
		t.Fatal("command unexpectedly reported job containment after forced assignment failure")
	}
	if tree.processHandle == 0 {
		t.Fatal("command tree did not retain the fallback process identity")
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if err := tree.cancel(); err != nil {
		t.Fatalf("cancel after root exit: %v", err)
	}
	if taskkillCalls != 0 {
		t.Fatalf("fallback targeted an exited root's numeric PID %d time(s)", taskkillCalls)
	}
}
