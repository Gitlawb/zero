package sandbox

import (
	"fmt"
	"io"
)

func RunWindowsSandboxCommandRunner(args []string, stderr io.Writer) int {
	config, err := ParseWindowsSandboxCommandArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 2
	}
	if _, err := LoadOrCreateWindowsCapabilitySIDs(config.SandboxHome); err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	return runWindowsSandboxCommand(config, stderr)
}
