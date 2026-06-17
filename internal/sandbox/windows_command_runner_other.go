//go:build !windows

package sandbox

import (
	"fmt"
	"io"
)

func runWindowsSandboxCommand(config WindowsSandboxCommandConfig, stderr io.Writer) int {
	fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": Windows sandbox command runner is only available on Windows")
	return 1
}
