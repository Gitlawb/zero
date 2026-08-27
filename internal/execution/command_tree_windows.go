//go:build windows

package execution

import (
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type commandTree struct {
	job   windows.Handle
	ready chan struct{}
}

func prepareCommandTree(_ *exec.Cmd) (*commandTree, error) {
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
	defer windows.CloseHandle(handle)
	return windows.AssignProcessToJobObject(tree.job, handle)
}

func (tree *commandTree) cancel() error {
	<-tree.ready
	return windows.TerminateJobObject(tree.job, 1)
}

func (tree *commandTree) close() error { return windows.CloseHandle(tree.job) }
