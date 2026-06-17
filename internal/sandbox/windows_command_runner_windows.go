//go:build windows

package sandbox

import (
	"fmt"
	"io"
)

func runWindowsSandboxCommand(config WindowsSandboxCommandConfig, stderr io.Writer) int {
	if config.SandboxLevel != WindowsSandboxLevelRestrictedToken {
		fmt.Fprintf(stderr, "%s: unsupported Windows sandbox level %q\n", WindowsSandboxCommandRunnerName, config.SandboxLevel)
		return 1
	}
	if err := ValidateWindowsSandboxSetupMarker(WindowsSandboxSetupConfigFromCommand(config)); err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	if err := ValidateWindowsNetworkPolicy(config.PermissionProfile.Network); err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	capabilitySIDs, err := WindowsCapabilitySIDsForConfig(config)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	token, err := createWindowsRestrictedTokenForCapabilitySIDs(capabilitySIDs)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	defer token.Close()
	exitCode, err := runWindowsCommandAsUser(token, config)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	return exitCode
}
