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
	offlineSID, err := WindowsOfflineMarkerSID(config.SandboxHome)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	// Compose the restricting-SID set: both modes keep the write-capability SIDs
	// (workspace write-jail); deny additionally carries the offline-marker SID
	// that the persistent WFP block filter matches — so a deny command has no
	// network while an approved allow command reaches it, both write-jailed.
	tokenSIDs := windowsRuntimeTokenSIDs(capabilitySIDs, offlineSID, config.PermissionProfile.Network.Mode)
	token, err := createWindowsRestrictedTokenForCapabilitySIDs(tokenSIDs)
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
