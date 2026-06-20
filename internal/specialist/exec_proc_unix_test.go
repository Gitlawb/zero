//go:build !windows

package specialist

import (
	"os/exec"
	"testing"
)

func TestHardenSpecialistChild(t *testing.T) {
	cmd := exec.Command("true")
	hardenSpecialistChild(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("expected Setpgid (own process group) so cancel can group-kill grandchildren (M6)")
	}
	if cmd.WaitDelay == 0 {
		t.Error("expected a WaitDelay backstop")
	}
	if cmd.Cancel == nil {
		t.Error("expected a custom group-kill Cancel")
	}
}
