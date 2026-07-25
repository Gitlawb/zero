//go:build !windows

package execution

import (
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestTerminateProcessTreeTreatsZombieGroupAsExited is the regression test for
// counting unreaped processes as alive: kill(2) with signal 0 succeeds for a
// zombie, so a group whose only remaining member is waiting to be reaped used to
// sit through both grace periods, take a pointless SIGKILL, and then be reported
// as "did not exit after SIGKILL" — a spurious cleanup failure whose timing
// depended on when the reaper ran.
func TestTerminateProcessTreeTreatsZombieGroupAsExited(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	ConfigureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	// Deliberately do NOT Wait: the child stays a zombie in its own group, which
	// is exactly the state a terminated tree passes through.
	t.Cleanup(func() { _ = cmd.Wait() })

	deadline := time.Now().Add(5 * time.Second)
	for {
		if zombie, ok := processIsZombie(pid); ok && zombie {
			break
		}
		if time.Now().After(deadline) {
			t.Skip("child did not reach the zombie state; ps output is unavailable in this environment")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if syscall.Kill(-pid, syscall.Signal(0)) != nil {
		t.Skip("the zombie group is no longer signalable; cannot exercise the case")
	}

	if signalTargetRunning(-pid) {
		t.Fatal("a group holding only a zombie must not count as running")
	}
	if err := TerminateProcessTree(pid, 50*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("TerminateProcessTree on an already-exited tree: %v", err)
	}
}

// TestTerminateProcessTreeStopsRunningGroup is the positive control: a group with
// a live member must still be seen as running and then actually stopped, so the
// zombie tolerance above cannot degrade into ignoring real processes.
func TestTerminateProcessTreeStopsRunningGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	ConfigureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = cmd.Wait() }()

	if !signalTargetRunning(-pid) {
		t.Fatal("a group with a live member must count as running")
	}
	if err := TerminateProcessTree(pid, time.Second, 10*time.Millisecond); err != nil {
		t.Fatalf("TerminateProcessTree: %v", err)
	}
	if running, ok := processGroupHasRunningMember(pid); ok && running {
		t.Fatal("the group still has a running member after termination")
	}
}

// processIsZombie reports a process's zombie state via ps. ok is false when the
// state could not be read.
func processIsZombie(pid int) (zombie bool, ok bool) {
	output, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false, false
	}
	state := string(output)
	for len(state) > 0 && (state[0] == ' ' || state[0] == '\n' || state[0] == '\t') {
		state = state[1:]
	}
	if state == "" {
		return false, false
	}
	return state[0] == 'Z', true
}
