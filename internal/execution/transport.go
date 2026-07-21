package execution

import (
	"io"
	"os/exec"
	"syscall"
	"time"
)

const processWaitDelay = 2 * time.Second

type processTransportStarter func(*exec.Cmd, io.Writer, bool) (io.WriteCloser, bool, func(), error)

func startProcessTransport(command *exec.Cmd, output io.Writer, ttyRequested bool) (io.WriteCloser, bool, func(), error) {
	if ttyRequested {
		original := cloneSysProcAttr(command.SysProcAttr)
		if stdin, cleanup, err := startPTYProcess(command, output); err == nil {
			return stdin, true, cleanup, nil
		}
		resetCommandAfterPTYFallback(command, original)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, false, nil, err
	}
	command.Stdout = output
	command.Stderr = output
	ConfigureProcessGroup(command)
	command.WaitDelay = processWaitDelay
	if err := command.Start(); err != nil {
		return nil, false, nil, err
	}
	return stdin, false, func() {}, nil
}

func cloneSysProcAttr(attributes *syscall.SysProcAttr) *syscall.SysProcAttr {
	if attributes == nil {
		return nil
	}
	cloned := *attributes
	return &cloned
}

func resetCommandAfterPTYFallback(command *exec.Cmd, original *syscall.SysProcAttr) {
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	command.SysProcAttr = original
	command.Cancel = nil
	command.WaitDelay = 0
}
