//go:build !windows

package config

import (
	"io"
	"os/exec"
	"syscall"
)

type commandProcess struct {
	groupID     int
	anchor      *exec.Cmd
	anchorInput io.WriteCloser
}

// startCommandProcess retains a live process-group leader until cleanup. The
// provider shell may be reaped before Wait finishes draining descendant-held
// pipes, so its PID cannot safely identify the group after Wait returns.
func startCommandProcess(cmd *exec.Cmd) (*commandProcess, error) {
	anchor := exec.Command("sh", "-c", "read _")
	anchor.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	anchorInput, err := anchor.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := anchor.Start(); err != nil {
		_ = anchorInput.Close()
		return nil, err
	}

	proc := &commandProcess{groupID: anchor.Process.Pid, anchor: anchor, anchorInput: anchorInput}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = proc.groupID
	if err := cmd.Start(); err != nil {
		proc.Close()
		return nil, err
	}
	return proc, nil
}

func (p *commandProcess) Terminate() {
	if p.groupID != 0 {
		_ = syscall.Kill(-p.groupID, syscall.SIGKILL)
	}
}

func (p *commandProcess) Close() {
	if p.anchorInput != nil {
		_ = p.anchorInput.Close()
		p.anchorInput = nil
	}
	if p.anchor != nil {
		_ = p.anchor.Wait()
		p.anchor = nil
	}
	p.groupID = 0
}
