//go:build windows

package sandbox

import (
	"fmt"
	"io"
)

func runWindowsSandboxSetup(config WindowsSandboxSetupConfig, stderr io.Writer) int {
	if _, err := BuildWindowsACLPlan(config.commandConfig()); err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	fmt.Fprintln(stderr, WindowsSandboxSetupName+": Windows ACL setup is not complete")
	return 1
}
