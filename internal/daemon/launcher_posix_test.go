//go:build !windows

package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecWorkerKillTerminatesProcessGroup(t *testing.T) {
	// The leader forks a child then exits and is left unreaped. Kill must
	// signal the launch-time process group (ConfigureChildProcessGroup) rather
	// than TerminateProcess's Getpgid rediscovery, so the child dies even
	// while Darwin Getpgid would return ESRCH on the zombie leader (#861).
	launcher, err := NewExecLauncher(ExecLauncherConfig{
		Executable: "/bin/sh",
		BaseArgs:   []string{"-c", "sleep 300 & echo $!; exit 0"},
		Env:        []string{},
	})
	if err != nil {
		t.Fatalf("NewExecLauncher: %v", err)
	}
	h, err := launcher(context.Background(), WorkerSpec{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	w, ok := h.(*execWorker)
	if !ok {
		t.Fatalf("launcher returned %T, want *execWorker", h)
	}
	if w.cmd.SysProcAttr == nil || !w.cmd.SysProcAttr.Setpgid || w.cmd.SysProcAttr.Pgid != 0 {
		t.Fatal("worker was not configured as its own process-group leader")
	}

	childPID := readWorkerPIDLine(t, w)
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		if w.cmd.ProcessState == nil {
			_, _ = w.Wait()
		}
	})

	// echo $! races ahead of exit 0. Wait until the leader is an unreaped
	// zombie without calling Wait, so Kill exercises Darwin Getpgid ESRCH.
	waitUntilUnreapedZombie(t, w.cmd.Process.Pid)

	if err := w.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if w.cmd.ProcessState != nil {
		t.Fatal("Kill must not reap; Wait still owns the child")
	}
	if _, err := w.Wait(); err != nil {
		t.Fatalf("Wait after Kill: %v", err)
	}
	assertProcessStopped(t, childPID)
}

func TestExecLauncherCancelTerminatesProcessGroup(t *testing.T) {
	// CommandContext Cancel must use the same launch-time group identity as
	// Kill, not TerminateProcess(pid). The leader stays alive (wait) so the
	// context-cancel goroutine is the path under test.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	launcher, err := NewExecLauncher(ExecLauncherConfig{
		Executable: "/bin/sh",
		BaseArgs:   []string{"-c", "sleep 300 & echo $!; wait"},
		Env:        []string{},
	})
	if err != nil {
		t.Fatalf("NewExecLauncher: %v", err)
	}
	h, err := launcher(ctx, WorkerSpec{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	w, ok := h.(*execWorker)
	if !ok {
		t.Fatalf("launcher returned %T, want *execWorker", h)
	}
	if w.cmd.SysProcAttr == nil || !w.cmd.SysProcAttr.Setpgid || w.cmd.SysProcAttr.Pgid != 0 {
		t.Fatal("worker was not configured as its own process-group leader")
	}

	childPID := readWorkerPIDLine(t, w)
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		if w.cmd.ProcessState == nil {
			_, _ = w.Wait()
		}
	})

	cancel()
	if _, err := w.Wait(); err != nil {
		t.Fatalf("Wait after cancel: %v", err)
	}
	assertProcessStopped(t, childPID)
}

func readWorkerPIDLine(t *testing.T, w *execWorker) int {
	t.Helper()
	line, ok, err := w.Stdout().Next()
	if err != nil {
		t.Fatalf("read worker stdout: %v", err)
	}
	if !ok {
		t.Fatal("worker stdout ended before printing the forked child pid")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse forked child pid %q: %v", line, err)
	}
	return pid
}

func assertProcessStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !launcherProcessStopped(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("forked child %d survived process-group termination", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func launcherProcessStopped(pid int) bool {
	if errors.Is(syscall.Kill(pid, syscall.Signal(0)), syscall.ESRCH) {
		return true
	}
	state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return errors.Is(syscall.Kill(pid, syscall.Signal(0)), syscall.ESRCH)
	}
	return strings.HasPrefix(strings.TrimSpace(string(state)), "Z")
}

// waitUntilUnreapedZombie polls until pid is a zombie without calling Wait.
func waitUntilUnreapedZombie(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !isUnreapedZombie(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d did not become an unreaped zombie before Kill", pid)
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
