//go:build windows

package localcontrol

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

func configureDetachedProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess
}
