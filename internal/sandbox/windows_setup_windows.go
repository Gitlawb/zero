//go:build windows

package sandbox

import (
	"fmt"
	"io"
)

func runWindowsSandboxSetup(config WindowsSandboxSetupConfig, stderr io.Writer) int {
	plan, err := BuildWindowsACLPlan(config.commandConfig())
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	networkPlan, err := BuildWindowsNetworkPlan(config.commandConfig())
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	rollback, err := applyWindowsACLPlan(plan)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	if err := applyWindowsNetworkPlan(networkPlan); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			fmt.Fprintf(stderr, "%s: %v; rollback failed: %v\n", WindowsSandboxSetupName, err, rollbackErr)
			return 1
		}
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	if _, err := WriteWindowsSandboxSetupMarker(config); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			fmt.Fprintf(stderr, "%s: %v; rollback failed: %v\n", WindowsSandboxSetupName, err, rollbackErr)
			return 1
		}
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	return 0
}
