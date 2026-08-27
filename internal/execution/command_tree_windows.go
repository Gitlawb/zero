//go:build windows

package execution

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type commandTree struct {
	job   windows.Handle
	ready chan struct{}
}

func prepareCommandTree(command *exec.Cmd) (*commandTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("execution: create process job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("execution: configure process job: %w", err)
	}
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
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(process.Pid),
	)
	if err != nil {
		return err
	}
	if err := windows.AssignProcessToJobObject(tree.job, handle); err != nil {
		return errors.Join(err, windows.CloseHandle(handle))
	}
	if err := windows.CloseHandle(handle); err != nil {
		return err
	}
	return resumeProcess(uint32(process.Pid))
}

func (tree *commandTree) cancel() error {
	<-tree.ready
	return windows.TerminateJobObject(tree.job, 1)
}

func (tree *commandTree) close() error { return windows.CloseHandle(tree.job) }

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
