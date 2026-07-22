//go:build linux

package execution

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestPTYFallbackRestoresOriginalProcessAttributes(t *testing.T) {
	original := &syscall.SysProcAttr{Setpgid: true}
	command := exec.Command("/bin/true")
	command.SysProcAttr = original
	saved := cloneSysProcAttr(command.SysProcAttr)
	hardenPTYProcess(command)
	resetCommandAfterPTYFallback(command, saved)

	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatalf("fallback attributes = %#v, want original Setpgid", command.SysProcAttr)
	}
	if command.SysProcAttr.Setsid || command.SysProcAttr.Setctty {
		t.Fatalf("fallback retained PTY attributes: %#v", command.SysProcAttr)
	}
}
