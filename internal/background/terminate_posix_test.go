//go:build !windows

package background

import (
	"bufio"
	"io"
	"os"
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
	var childPID int
	t.Cleanup(func() {
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
			if cmd.ProcessState == nil {
				_ = cmd.Wait()
			}
		}
	})

	childPID = readPIDWithTimeout(t, stdout, 3*time.Second)

	// echo $! races ahead of exit 0. Wait until the leader is an unreaped
	// zombie without calling Wait, so this exercises Darwin Getpgid ESRCH.
	waitUntilUnreapedZombie(t, cmd.Process.Pid)

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

func TestTerminateOwnedProcessNil(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TerminateOwnedProcess(nil) panicked: %v", r)
		}
	}()
	if err := TerminateOwnedProcess(nil); err == nil {
		t.Fatal("TerminateOwnedProcess(nil) must return an error")
	}
}

func TestTerminateOwnedProcessUnstarted(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TerminateOwnedProcess(unstarted) panicked: %v", r)
		}
	}()
	cmd := exec.Command("true")
	if err := TerminateOwnedProcess(cmd); err == nil {
		t.Fatal("TerminateOwnedProcess on an unstarted command must return an error")
	}
}

// waitUntilUnreapedZombie polls until pid is a zombie without calling Wait.
func waitUntilUnreapedZombie(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !isUnreapedZombie(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d did not become an unreaped zombie before terminate", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func isUnreapedZombie(pid int) bool {
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		s := string(data)
		i := strings.LastIndexByte(s, ')')
		if i < 0 || i+2 >= len(s) {
			return false
		}
		return s[i+2] == 'Z'
	}
	state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(state)), "Z")
}

func readPIDWithTimeout(t *testing.T, r io.Reader, timeout time.Duration) int {
	t.Helper()
	type res struct {
		pid int
		err error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			ch <- res{err: err}
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		ch <- res{pid: pid, err: err}
	}()
	select {
	case out := <-ch:
		if out.err != nil {
			t.Fatalf("read child PID: %v", out.err)
		}
		return out.pid
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for child PID after %v", timeout)
		return 0
	}
}
