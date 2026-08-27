//go:build !windows

package background

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminateOwnedProcessDoesNotReap(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	ConfigureChildProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	if err := TerminateOwnedProcess(cmd); err != nil {
		t.Fatalf("TerminateOwnedProcess: %v", err)
	}
	if cmd.ProcessState != nil {
		t.Fatal("TerminateOwnedProcess must not Wait; the caller still owns the reap")
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected the terminated command's Wait to report a signal")
	}
}

func TestTerminateOwnedProcessKillsChildAfterLeaderExits(t *testing.T) {
	grace, poll := terminationGracePeriod, terminationPollInterval
	terminationGracePeriod, terminationPollInterval = 2*time.Second, 20*time.Millisecond
	t.Cleanup(func() { terminationGracePeriod, terminationPollInterval = grace, poll })

	// The leader exits immediately after launching the child and is left
	// unreaped. TerminateOwnedProcess must signal the launch-time process
	// group rather than rediscovering it via Getpgid (Darwin ESRCH, #861).
	cmd := exec.Command("sh", "-c", "sleep 300 & echo $!; exit 0")
	ConfigureChildProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read forked child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse forked child pid %q: %v", line, err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		if cmd.ProcessState == nil {
			_ = cmd.Wait()
		}
	})

	if err := TerminateOwnedProcess(cmd); err != nil {
		t.Fatalf("TerminateOwnedProcess: %v", err)
	}
	if cmd.ProcessState != nil {
		t.Fatal("TerminateOwnedProcess must not reap the leader")
	}
	if err := cmd.Wait(); err != nil && terminatingSignal(err) == 0 {
		// Leader may have exited 0 before the group signal, or have been
		// signalled; either is a successful reap.
		t.Fatalf("Wait after TerminateOwnedProcess: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !processStopped(childPID) {
		if time.Now().After(deadline) {
			t.Fatalf("forked child %d survived TerminateOwnedProcess — group kill failed", childPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
