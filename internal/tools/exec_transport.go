package tools

import (
	"io"
	"os/exec"
)

func startExecProcess(command *exec.Cmd, output *execOutputBuffer, ttyRequested bool) (io.WriteCloser, bool, func(), error) {
	if ttyRequested {
		if stdin, cleanup, err := startPTYProcess(command, output); err == nil {
			return stdin, true, cleanup, nil
		}
		command.Stdin = nil
		command.Stdout = nil
		command.Stderr = nil
	}
	return startPipeProcess(command, output)
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
