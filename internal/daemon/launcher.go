package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Gitlawb/zero/internal/background"
	"github.com/Gitlawb/zero/internal/sandbox"
)

// reentrancyMarkers are the env vars zero sets on a command it has ALREADY
// wrapped in a sandbox. The daemon MUST strip them from every worker's
// environment: a worker that inherited them would trip zero's re-entrancy guard
// (sandbox.IsAlreadySandboxed needs BOTH) and run with a pass-through plan —
// i.e. UNSANDBOXED — silently defeating the sandbox. Stripping them forces each
// worker to (re)establish its own sandbox. This is the daemon's #1 security
// invariant: it must NOT bypass the sandbox.
var reentrancyMarkers = []string{sandbox.EnvSandboxed, sandbox.EnvSandboxBackend}

// scrubWorkerEnv returns env with the sandbox re-entrancy markers removed. Every
// other variable is preserved — including the ZERO_SANDBOX_* policy config the
// worker reads to rebuild its own policy — so the sandbox policy is PROPAGATED,
// not bypassed. The caller's slice is never mutated.
func scrubWorkerEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		drop := false
		for _, marker := range reentrancyMarkers {
			if strings.HasPrefix(kv, marker+"=") {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// execWorker is a WorkerHandle backed by a `zero exec` child process speaking
// stream-json on stdout.
type execWorker struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	lines  *scannerLines
	pid    int
}

func (w *execWorker) Stdout() Lines { return w.lines }
func (w *execWorker) Pid() int      { return w.pid }

func (w *execWorker) Wait() (int, error) {
	err := w.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

func (w *execWorker) Kill() error {
	if w.cmd.Process == nil {
		return nil
	}
	// background.TerminateProcess is the cross-platform terminate (kills the
	// process group on POSIX, taskkill /T on Windows).
	return background.TerminateProcess(w.cmd.Process.Pid)
}

// scannerLines adapts a bufio.Scanner to the Lines interface.
type scannerLines struct {
	sc *bufio.Scanner
}

func (s *scannerLines) Next() (string, bool, error) {
	if s.sc.Scan() {
		return s.sc.Text(), true, nil
	}
	if err := s.sc.Err(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

// ExecLauncherConfig configures the production worker launcher.
type ExecLauncherConfig struct {
	// Executable is the zero binary to spawn (defaults to os.Executable()).
	Executable string
	// BaseArgs are prepended before the per-session args (defaults to the headless
	// stream-json exec invocation `exec --output-format stream-json`).
	BaseArgs []string
	// Env is the base environment for workers (defaults to os.Environ()). The
	// re-entrancy markers are always scrubbed from it.
	Env []string
}

// NewExecLauncher builds a Launcher that spawns `zero exec` workers which speak
// stream-json on stdout. Each worker:
//   - runs with the re-entrancy markers SCRUBBED from its env (so it establishes
//     its own sandbox; the daemon never bypasses the sandbox), while the rest of
//     the policy config/env is propagated;
//   - is placed in its own process group (background.ConfigureChildProcessGroup)
//     so Kill / drain can terminate it and any children cleanly;
//   - has its per-session working directory (spec.Cwd) and flags (spec.Args)
//     applied. The pool does the os/exec itself and uses internal/background only
//     for process-group setup + cross-platform terminate.
func NewExecLauncher(cfg ExecLauncherConfig) (Launcher, error) {
	exe := cfg.Executable
	if exe == "" {
		resolved, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("daemon: locate zero executable: %w", err)
		}
		exe = resolved
	}
	baseArgs := cfg.BaseArgs
	if baseArgs == nil {
		baseArgs = []string{"exec", "--output-format", "stream-json"}
	}
	baseEnv := cfg.Env
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	env := scrubWorkerEnv(baseEnv)

	return func(ctx context.Context, spec WorkerSpec) (WorkerHandle, error) {
		args := make([]string, 0, len(baseArgs)+len(spec.Args))
		args = append(args, baseArgs...)
		args = append(args, spec.Args...)

		cmd := exec.CommandContext(ctx, exe, args...)
		cmd.Dir = spec.Cwd
		cmd.Env = env
		cmd.Stdin = nil // the prompt is passed via flags; no streamed stdin input
		background.ConfigureChildProcessGroup(cmd)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("daemon: worker stdout pipe: %w", err)
		}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("daemon: start worker: %w", err)
		}

		scanner := bufio.NewScanner(stdout)
		// Allow long stream-json lines up to the control-frame cap.
		scanner.Buffer(make([]byte, 0, 64*1024), MaxFrameSize)
		return &execWorker{
			cmd:    cmd,
			stdout: stdout,
			lines:  &scannerLines{sc: scanner},
			pid:    cmd.Process.Pid,
		}, nil
	}, nil
}
