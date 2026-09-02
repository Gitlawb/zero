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
	finalized := false
	// Phase 1: Arm generic handle cleanup immediately upon launch.
	t.Cleanup(func() {
		if finalized {
			return
		}
		_ = h.Kill()
		_, _ = h.Wait()
	})

	// Phase 2: Obtain descendant PID and arm direct descendant fallback before leader exits.
	childPID := readWorkerPIDLine(t, h)
	t.Cleanup(func() {
		if finalized {
			return
		}
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})

	w, ok := h.(*execWorker)
	if !ok {
		t.Fatalf("launcher returned %T, want *execWorker", h)
	}
	if w.cmd != nil && w.cmd.Process != nil {
		leaderPID := w.cmd.Process.Pid
		t.Cleanup(func() {
			if finalized {
				return
			}
			_ = syscall.Kill(-leaderPID, syscall.SIGKILL)
			_ = syscall.Kill(leaderPID, syscall.SIGKILL)
		})
	}

	// Phase 3: Setup validation — only now assert process group configuration.
	if w.cmd.SysProcAttr == nil || !w.cmd.SysProcAttr.Setpgid || w.cmd.SysProcAttr.Pgid != 0 {
		t.Fatal("worker was not configured as its own process-group leader")
	}

	// Phase 4: Wait until leader is unreaped zombie.
	waitUntilUnreapedZombie(t, w.cmd.Process.Pid)

	// Phase 5: Production action.
	if err := w.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if w.cmd.ProcessState != nil {
		t.Fatal("Kill must not reap; Wait still owns the child")
	}

	// Phase 6: Finalization.
	if _, err := w.Wait(); err != nil {
		t.Fatalf("Wait after Kill: %v", err)
	}
	assertProcessStopped(t, childPID)
	finalized = true
}

func TestExecWorkerKillAfterWaitIsNoop(t *testing.T) {
	launcher, err := NewExecLauncher(ExecLauncherConfig{
		Executable: "/bin/sh",
		BaseArgs:   []string{"-c", "echo hello; exit 0"},
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
	if _, err := w.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// Calling Kill after Wait must be an idempotent no-op and never signal
	if err := w.Kill(); err != nil {
		t.Fatalf("Kill after Wait returned error: %v", err)
	}
}

func TestExecLauncherCancelTerminatesProcessGroup(t *testing.T) {
	// CommandContext Cancel must use the same launch-time group identity as
	// Kill, not TerminateProcess(pid). The leader exits without being reaped,
	// while its descendant keeps the stdout pipe open. That makes cancellation
	// exercise the Darwin Getpgid -> ESRCH failure shape fixed by this change.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	launcher, err := NewExecLauncher(ExecLauncherConfig{
		Executable: "/bin/sh",
		BaseArgs:   []string{"-c", "sleep 300 & echo $!; exit 0"},
		Env:        []string{},
	})
	if err != nil {
		t.Fatalf("NewExecLauncher: %v", err)
	}
	h, err := launcher(ctx, WorkerSpec{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	finalized := false
	// Phase 1: Arm generic handle cleanup immediately upon launch.
	t.Cleanup(func() {
		if finalized {
			return
		}
		_ = h.Kill()
		_, _ = h.Wait()
	})

	// Phase 2: Obtain descendant PID and arm direct descendant fallback.
	childPID := readWorkerPIDLine(t, h)
	t.Cleanup(func() {
		if finalized {
			return
		}
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})

	w, ok := h.(*execWorker)
	if !ok {
		t.Fatalf("launcher returned %T, want *execWorker", h)
	}
	if w.cmd != nil && w.cmd.Process != nil {
		leaderPID := w.cmd.Process.Pid
		t.Cleanup(func() {
			if finalized {
				return
			}
			_ = syscall.Kill(-leaderPID, syscall.SIGKILL)
			_ = syscall.Kill(leaderPID, syscall.SIGKILL)
		})
	}

	// Phase 3: Setup validation.
	if w.cmd.SysProcAttr == nil || !w.cmd.SysProcAttr.Setpgid || w.cmd.SysProcAttr.Pgid != 0 {
		t.Fatal("worker was not configured as its own process-group leader")
	}

	// Phase 4: Wait until leader is unreaped zombie.
	waitUntilUnreapedZombie(t, w.cmd.Process.Pid)

	// Phase 5: Production action.
	cancel()

	// Phase 6: Finalization.
	if _, err := w.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait after cancel: %v", err)
	}
	if w.cmd.ProcessState == nil {
		t.Fatal("Wait after cancel did not reap the exited worker leader")
	}
	assertProcessStopped(t, childPID)
	finalized = true
}

func readWorkerPIDLine(t *testing.T, h WorkerHandle) int {
	t.Helper()
	type res struct {
		pid int
		err error
	}
	ch := make(chan res, 1)
	go func() {
		line, ok, err := h.Stdout().Next()
		if err != nil {
			ch <- res{err: err}
			return
		}
		if !ok {
			ch <- res{err: errors.New("worker stdout ended before printing the forked child pid")}
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		ch <- res{pid: pid, err: err}
	}()

	select {
	case out := <-ch:
		if out.err != nil {
			t.Fatalf("read worker stdout: %v", out.err)
		}
		return out.pid
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for worker child pid")
		return 0
	}
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
