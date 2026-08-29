//go:build windows

package execution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type commandTree struct {
	job           windows.Handle
	processHandle windows.Handle
	process       *os.Process
	contained     bool
	ready         chan struct{}
}

const commandProcessStillActive = uint32(259) // STILL_ACTIVE

func prepareCommandTree(command *exec.Cmd) (*commandTree, error) {
	// Job containment is preferred but optional. The process still starts
	// suspended so attach can retain its identity and either assign the job or
	// establish the fallback before any child process can escape.
	job, _ := windows.CreateJobObject(nil, nil)
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return &commandTree{job: job, ready: make(chan struct{})}, nil
}

func (tree *commandTree) attach(process *os.Process) error {
	defer close(tree.ready)
	if process == nil {
		return nil
	}
	tree.process = process
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(process.Pid),
	)
	if err == nil {
		tree.processHandle = handle
		if tree.job != 0 && assignCommandProcessToJob(tree.job, handle) == nil {
			tree.contained = true
		}
	} else {
		// PROCESS_SET_QUOTA is needed only for job assignment. If the host
		// denies that setup right, retry with the narrower rights needed by the
		// retained identity-safe fallback.
		tree.processHandle, _ = windows.OpenProcess(
			windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
			false,
			uint32(process.Pid),
		)
	}
	// Job creation, process opening, and assignment can all fail when the host
	// already constrains this process. They must not strand it suspended.
	return resumeProcess(uint32(process.Pid))
}

func (tree *commandTree) cancel() error {
	<-tree.ready
	if tree.contained {
		return windows.TerminateJobObject(tree.job, 1)
	}
	if tree.processHandle == 0 {
		if tree.process == nil {
			return nil
		}
		return tree.process.Kill()
	}

	// The retained handle both identifies the original process and prevents its
	// PID from being reused. Only ask taskkill to walk by PID while that exact
	// process is still active; after Wait has reaped it, numeric PID targeting
	// could otherwise hit an unrelated process.
	var exitCode uint32
	if err := windows.GetExitCodeProcess(tree.processHandle, &exitCode); err != nil {
		return errors.Join(err, windows.TerminateProcess(tree.processHandle, 1))
	}
	if exitCode != commandProcessStillActive {
		return nil
	}
	if err := cancelCommandTreeByPID(tree.process.Pid); err == nil {
		return nil
	} else {
		return errors.Join(err, windows.TerminateProcess(tree.processHandle, 1))
	}
}

func (tree *commandTree) close() (err error) {
	if tree.job != 0 {
		err = errors.Join(err, windows.CloseHandle(tree.job))
		tree.job = 0
	}
	if tree.processHandle != 0 {
		err = errors.Join(err, windows.CloseHandle(tree.processHandle))
		tree.processHandle = 0
	}
	return err
}

var assignCommandProcessToJob = windows.AssignProcessToJobObject

var cancelCommandTreeByPID = func(pid int) error {
	ctx, cancel := context.WithTimeout(context.Background(), processWaitDelay)
	defer cancel()
	return exec.CommandContext(ctx, taskkillPath(), "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}

func resumeProcess(pid uint32) (err error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(snapshot)) }()

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			return errors.Join(resumeErr, windows.CloseHandle(thread))
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return fmt.Errorf("execution: no thread found for suspended process %d", pid)
			}
			return err
		}
	}
}
