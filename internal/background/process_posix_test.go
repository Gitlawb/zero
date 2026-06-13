//go:build !windows

package background

import (
	"bufio"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// terminatingSignal returns the signal that killed a process from its cmd.Wait()
// error, or 0 if it exited normally / for another reason.
func terminatingSignal(err error) syscall.Signal {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return status.Signal()
		}
	}
	return 0
}

func TestTerminateProcessEscalatesToSIGKILL(t *testing.T) {
	grace, poll := terminationGracePeriod, terminationPollInterval
	terminationGracePeriod, terminationPollInterval = 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { terminationGracePeriod, terminationPollInterval = grace, poll })

	// A process that traps and ignores SIGTERM — only SIGKILL can stop it. The
	// while-loop keeps the trap-holding shell as the process (a trailing single
	// command would be exec-optimized, dropping the trap); "ready" is printed
	// AFTER the trap is installed so we don't signal before it takes effect.
	cmd := exec.Command("sh", "-c", "trap '' TERM; echo ready; while true; do sleep 0.2; done")
	ConfigureChildProcessGroup(cmd) // group leader, so the negative-pid kill targets it
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatalf("waiting for trap to install: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	start := time.Now()
	if err := terminateProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("terminateProcess: %v", err)
	}

	select {
	case waitErr := <-done:
		if sig := terminatingSignal(waitErr); sig != syscall.SIGKILL {
			t.Fatalf("process terminated by %v, want SIGKILL (it ignores SIGTERM)", sig)
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process not killed — SIGKILL escalation failed")
	}
	if elapsed := time.Since(start); elapsed < terminationGracePeriod {
		t.Fatalf("process died in %v, before the grace period — SIGTERM should be tried first", elapsed)
	}
}

func TestTerminateProcessGracefulSIGTERM(t *testing.T) {
	// Longer grace so we can prove a well-behaved process dies on SIGTERM,
	// well before any SIGKILL escalation would fire.
	grace, poll := terminationGracePeriod, terminationPollInterval
	terminationGracePeriod, terminationPollInterval = 5*time.Second, 20*time.Millisecond
	t.Cleanup(func() { terminationGracePeriod, terminationPollInterval = grace, poll })

	cmd := exec.Command("sh", "-c", "sleep 30") // default disposition: SIGTERM kills it
	ConfigureChildProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	start := time.Now()
	if err := terminateProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("terminateProcess: %v", err)
	}
	select {
	case waitErr := <-done:
		// Must die from SIGTERM, not SIGKILL — proves we ask politely first and
		// don't regress to an immediate force-kill.
		if sig := terminatingSignal(waitErr); sig != syscall.SIGTERM {
			t.Fatalf("process terminated by %v, want SIGTERM", sig)
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process not killed on SIGTERM")
	}
	if elapsed := time.Since(start); elapsed >= terminationGracePeriod {
		t.Fatalf("took %v — should have died on SIGTERM, not waited for SIGKILL", elapsed)
	}
}

func TestTerminateProcessAlreadyExited(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	ConfigureChildProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = cmd.Wait() // reap; the pid is now gone

	if err := terminateProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("terminateProcess on an exited process should be a no-op, got %v", err)
	}
}

func TestTerminateProcessKillsForkedChildren(t *testing.T) {
	grace, poll := terminationGracePeriod, terminationPollInterval
	terminationGracePeriod, terminationPollInterval = 2*time.Second, 20*time.Millisecond
	t.Cleanup(func() { terminationGracePeriod, terminationPollInterval = grace, poll })

	// The parent shell forks a long-lived child and prints its PID. Without
	// group termination the child would be reparented to init and outlive the
	// parent; group termination must take it down too.
	cmd := exec.Command("sh", "-c", "sleep 300 & echo $!; wait")
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
	go func() { _ = cmd.Wait() }()

	if err := terminateProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("terminateProcess: %v", err)
	}

	// The forked child must be gone too (poll until reaped by init).
	deadline := time.Now().Add(2 * time.Second)
	for {
		// Only ESRCH proves the child is gone; any other error (e.g. EPERM) would
		// wrongly pass the test, so treat it as still-present and keep polling.
		if errors.Is(syscall.Kill(childPID, syscall.Signal(0)), syscall.ESRCH) {
			break // child no longer exists
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
			t.Fatalf("forked child %d survived terminateProcess — group kill failed", childPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
