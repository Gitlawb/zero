//go:build windows

package execution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRunCommandFailsBeforeDescendantCanEscapeWhenJobAssignmentFails(t *testing.T) {
	switch os.Getenv("ZERO_ASSIGNMENT_FAILURE_TREE_HELPER") {
	case "root":
		child := exec.Command(os.Args[0], "-test.run=^TestRunCommandFailsBeforeDescendantCanEscapeWhenJobAssignmentFails$")
		child.Env = append(os.Environ(), "ZERO_ASSIGNMENT_FAILURE_TREE_HELPER=child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("ZERO_ASSIGNMENT_FAILURE_TREE_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(3)
		}
		return
	case "child":
		time.Sleep(30 * time.Second)
		return
	}

	originalAssign := assignCommandProcessToJob
	assignCommandProcessToJob = func(windows.Handle, windows.Handle) error {
		return windows.ERROR_ACCESS_DENIED
	}
	t.Cleanup(func() { assignCommandProcessToJob = originalAssign })

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx := context.Background()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunCommandFailsBeforeDescendantCanEscapeWhenJobAssignmentFails$")
	command.Env = append(os.Environ(),
		"ZERO_ASSIGNMENT_FAILURE_TREE_HELPER=root",
		"ZERO_ASSIGNMENT_FAILURE_TREE_PID_FILE="+pidFile,
	)
	started := time.Now()
	err := RunCommand(ctx, command)
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("RunCommand took %s after job assignment failed", elapsed)
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("RunCommand error = %v, want ERROR_ACCESS_DENIED", err)
	}
	if command.ProcessState == nil || !command.ProcessState.Exited() {
		t.Fatalf("suspended command was not killed and reaped: state = %v", command.ProcessState)
	}
	if _, statErr := os.Stat(pidFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("suspended command spawned a descendant after job assignment failed: PID file error = %v", statErr)
	}
}
