package tools

import (
	"io"
	"os/exec"
	"syscall"
)

func startExecProcess(command *exec.Cmd, output *execOutputBuffer, ttyRequested bool) (io.WriteCloser, bool, func(), error) {
	if ttyRequested {
		origSysProcAttr := command.SysProcAttr
		if stdin, cleanup, err := startPTYProcessFunc(command, output); err == nil {
			return stdin, true, cleanup, nil
		}
		resetExecCommandForPipeFallback(command, origSysProcAttr)
	}
	return startPipeProcess(command, output)
}

var startPTYProcessFunc = startPTYProcess

func resetExecCommandForPipeFallback(command *exec.Cmd, origSysProcAttr *syscall.SysProcAttr) {
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	command.SysProcAttr = origSysProcAttr
	command.Cancel = nil
	command.WaitDelay = 0
}

func startPipeProcess(command *exec.Cmd, output *execOutputBuffer) (io.WriteCloser, bool, func(), error) {
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, false, nil, err
	}
	command.Stdout = output
	command.Stderr = output
	hardenProcessLifetime(command)
	if err := command.Start(); err != nil {
		return nil, false, nil, err
	}
	return stdin, false, func() {}, nil
}
