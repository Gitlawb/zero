//go:build !windows

package execution

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ConfigureProcessGroup makes cmd the leader of a process group so lifecycle
// operations cover descendants instead of orphaning them.
func ConfigureProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillProcessTree immediately kills pid and, when it is a group leader, its
// descendant process group.
func KillProcessTree(pid int) error {
	target, err := processSignalTarget(pid)
	if err != nil {
		return err
	}
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// TerminateProcessTree requests graceful termination, then force-kills the
// process tree after grace. Callers retain their distinct persistence models;
// this function owns only the OS lifecycle primitive.
func TerminateProcessTree(pid int, grace, poll time.Duration) error {
	target, err := processSignalTarget(pid)
	if err != nil {
		return err
	}
	alive := func() bool { return signalTargetRunning(target) }
	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	if poll <= 0 {
		poll = 50 * time.Millisecond
	}
	if grace < 0 {
		grace = 0
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !alive() {
			return nil
		}
		time.Sleep(poll)
	}
	if !alive() {
		return nil
	}
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline = time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !alive() {
			return nil
		}
		time.Sleep(poll)
	}
	if alive() {
		return fmt.Errorf("process %d did not exit after SIGKILL", pid)
	}
	return nil
}

// signalTargetRunning reports whether the signal target still has a process that
// is actually RUNNING, as opposed to one waiting to be reaped.
//
// kill(2) with signal 0 succeeds for a zombie: the PID still exists until its
// parent collects it. That matters because the target here is usually a process
// GROUP, and a terminated leader's child is briefly a zombie before init/launchd
// reaps it. Treating that window as "still alive" made termination sit through
// both grace periods, SIGKILL an already-dead group, and then report
// "did not exit after SIGKILL" for a tree that had in fact stopped — turning a
// successful cleanup into a spurious failure (and a flaky one, since it depends on
// reap timing).
//
// Zombie detection goes through ps, which is available on every Unix we target and
// avoids a /proc dependency Darwin does not have. It is only consulted when the
// cheap kill check says something is still there, so the common path costs nothing
// extra. If ps cannot be run or its output cannot be parsed, the conservative
// pre-existing answer (the kill result) stands.
func signalTargetRunning(target int) bool {
	if syscall.Kill(target, syscall.Signal(0)) != nil {
		return false
	}
	pgid := target
	if pgid < 0 {
		pgid = -pgid
	}
	running, ok := processGroupHasRunningMember(pgid)
	if !ok {
		return true
	}
	return running
}

// processGroupHasRunningMember reports whether any member of pgid is in a state
// other than zombie. ok is false when the group's states could not be determined.
func processGroupHasRunningMember(pgid int) (running bool, ok bool) {
	output, err := exec.Command("ps", "-A", "-o", "pid=,pgid=,stat=").Output()
	if err != nil {
		return false, false
	}
	found := false
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		memberPgid, err := strconv.Atoi(fields[1])
		if err != nil || memberPgid != pgid {
			continue
		}
		found = true
		if !strings.HasPrefix(fields[2], "Z") {
			return true, true
		}
	}
	// No rows at all means ps could not see the group (a race with exit, or an
	// environment where the listing is restricted); only claim knowledge when at
	// least one member was observed.
	return false, found
}

func processSignalTarget(pid int) (int, error) {
	if pid <= 1 {
		return 0, fmt.Errorf("refusing to signal invalid pid %d", pid)
	}
	target := pid
	if pgid, err := syscall.Getpgid(pid); err == nil {
		if pgid == pid {
			target = -pid
		}
	} else if errors.Is(err, syscall.ESRCH) {
		// Preserve the individual target; the signal call below treats ESRCH as
		// already gone, which is a successful lifecycle outcome.
		return pid, nil
	}
	return target, nil
}
