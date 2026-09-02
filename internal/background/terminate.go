package background

import (
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const commandReapTimeout = 3 * time.Second

// TerminateProcess stops a background process by PID — on Windows its process
// tree; on POSIX its whole process group when the PID leads its own group (the
// invariant ConfigureChildProcessGroup establishes for processes started through
// this package), otherwise just the individual PID. The PID-only fallback is
// deliberate — signalling a non-leader's group could hit unrelated processes — but
// it means descendants of a non-leader are NOT reaped; pass a group leader to
// guarantee group termination. Exported for callers that hold a raw PID and cannot
// route through the manager, e.g. cleaning up a just-launched child whose PID could
// not be recorded.
func TerminateProcess(pid int) error {
	return terminateProcess(pid)
}

// TerminateCommand stops a started command and reaps its leader. The caller must
// have exclusive ownership of cmd: it must not have previously called Wait or
// Process.Release, and no goroutine may call either concurrently. On POSIX it
// stops the whole group when the command was configured as its leader; ordinary
// commands safely fall back to PID/tree discovery. On Windows, success for a
// leader that was already dead confirms only that the leader was reaped;
// descendants may survive because Windows cannot rediscover a tree from a dead
// root. `zero daemon start` needs this operation when readiness times out: it
// launched the child, so it must both stop the tree and collect the leader.
//
// The order matters: the tree is signalled first, because Wait releases the
// leader's PID and a later group lookup could then resolve to nothing (or, worse,
// to a recycled PID). Termination stays in the shared execution lifecycle
// primitives rather than introducing a second platform-specific killer.
//
// If the bounded reap times out, the Wait call already started by this function
// continues in its goroutine. From that point onward the caller must not access
// cmd at all, including ProcessState, because that would race with Wait.
func TerminateCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("terminate command: process was never started")
	}
	leaderAlreadyExited, terminateErr := terminateOwnedProcess(cmd)
	reapErr := waitForTerminatedCommandWithin(cmd, commandReapTimeout)
	if reapErr != nil {
		if terminateErr != nil {
			return fmt.Errorf("%v (reap failed: %w)", terminateErr, reapErr)
		}
		return reapErr
	}
	if terminateErr != nil && !leaderAlreadyExited {
		return terminateErr
	}
	// Only discard a termination error when the platform demonstrated before
	// attempting termination that the leader had already exited. A successful
	// reap alone says nothing about whether a live descendant tree was stopped.
	return nil
}

// classifyWaitError treats a non-zero exit status as success: a process being
// terminated is expected to report one (or a signal), so the only interesting
// failure is Wait itself not working.
func classifyWaitError(waitErr error) error {
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return fmt.Errorf("reap process: %w", waitErr)
	}
	return nil
}

// waitForTerminatedCommandWithin bounds the reap so a child that somehow survives
// termination cannot hang the caller — the daemon-start cleanup path runs while a
// user waits at the CLI. On timeout its Wait goroutine is intentionally left to
// finish: exec.Cmd permits only one Wait call, and abandoning it without a reaper
// would leak the child once it eventually exits.
func waitForTerminatedCommandWithin(cmd *exec.Cmd, timeout time.Duration) error {
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case waitErr := <-waitDone:
		return classifyWaitError(waitErr)
	case <-time.After(timeout):
		return fmt.Errorf("process %d did not reap after termination", cmd.Process.Pid)
	}
}
