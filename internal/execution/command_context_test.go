package execution

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestRunCommandKillsDescendantAfterRootExit(t *testing.T) {
	switch os.Getenv("ZERO_COMMAND_TREE_HELPER") {
	case "root":
		child := exec.Command(os.Args[0], "-test.run=^TestRunCommandKillsDescendantAfterRootExit$")
		child.Env = append(os.Environ(), "ZERO_COMMAND_TREE_HELPER=child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("ZERO_COMMAND_TREE_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(3)
		}
		return
	case "child":
		time.Sleep(30 * time.Second)
		return
	}

	pidFile := t.TempDir() + string(os.PathSeparator) + "child.pid"
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunCommandKillsDescendantAfterRootExit$")
	cmd.Env = append(os.Environ(),
		"ZERO_COMMAND_TREE_HELPER=root",
		"ZERO_COMMAND_TREE_PID_FILE="+pidFile,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	started := time.Now()
	err := RunCommand(ctx, cmd)
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("RunCommand remained blocked by descendant output handles for %s", elapsed)
	}
	if err == nil {
		t.Fatal("timed-out command unexpectedly succeeded")
	}
	pidData, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("read descendant PID: %v; command output: %s", readErr, output.String())
	}
	pid, parseErr := strconv.Atoi(string(pidData))
	if parseErr != nil {
		t.Fatalf("parse descendant PID %q: %v", pidData, parseErr)
	}
	awaitProcessExit(t, pid)
}
