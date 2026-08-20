//go:build windows

package sandbox

import (
	"fmt"
	"io"

	"golang.org/x/sys/windows"
)

func runWindowsSandboxSetup(config WindowsSandboxSetupConfig, stderr io.Writer) int {
	// Applying the WFP network filters and workspace ACLs requires Administrator
	// rights; without them WFP fails deep inside with a raw ACCESS_DENIED (0x5).
	// Check up front and return an actionable message instead.
	if !windowsProcessIsElevated() {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": Administrator rights are required. Re-run `zero sandbox setup` from an elevated (Run as administrator) terminal.")
		return 1
	}
	// Provisions the runtime candidate roots, then builds the plan that grants
	// them. One call because a granted-but-absent write root fails the whole apply.
	plan, runtimeRollback, err := buildWindowsSandboxSetupACLPlan(config)
	// SETUP IS TRANSACTIONAL FOR THE STATE IT CREATED. Runtime roots are
	// materialized before the network plan, the ACL apply, the network apply and
	// the marker write, and every one of those can fail. Previously only ACL
	// snapshots were restored, so a run that reported failure still left new
	// persistent runtime directories behind, and it could not have cleaned them up
	// even in principle because provisioning returned nothing about what it made.
	//
	// Composed once here so no later failure path can forget it. It removes only
	// directories THIS run created, innermost first, and refuses to remove a
	// non-empty one, so a pre-existing cache or temp tree is never touched.
	failed := func(cause error) int {
		if rollbackErr := runtimeRollback.run(); rollbackErr != nil {
			fmt.Fprintf(stderr, "%s: %v; runtime rollback failed: %v\n", WindowsSandboxSetupName, cause, rollbackErr)
			return 1
		}
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+cause.Error())
		return 1
	}
	if err != nil {
		return failed(err)
	}
	// Always provision the mode-INDEPENDENT infrastructure: the outbound block
	// filters scoped to the offline-marker SID. Runtime gates network per command
	// by whether the token carries that SID, so one setup serves both modes.
	networkPlan, err := BuildWindowsNetworkInfraPlan(config.commandConfig())
	if err != nil {
		return failed(err)
	}
	rollback, err := applyWindowsACLPlan(plan)
	if err != nil {
		return failed(err)
	}
	// From here both have to be undone, ACLs first so the directories are empty
	// of our grants before they are removed.
	failedAfterACL := func(cause error) int {
		if rollbackErr := rollback(); rollbackErr != nil {
			fmt.Fprintf(stderr, "%s: %v; rollback failed: %v\n", WindowsSandboxSetupName, cause, rollbackErr)
			return 1
		}
		return failed(cause)
	}
	if err := applyWindowsNetworkPlan(networkPlan); err != nil {
		return failedAfterACL(err)
	}
	// The runtime stamp is written by WriteWindowsSandboxSetupMarker below, which
	// is the one place setup records that it completed. It is reached only after
	// the ACL and network plans have applied, so the stamp still means "this tree
	// carries these permissions" and every caller that records a marker records
	// the stamp with it.
	if _, err := WriteWindowsSandboxSetupMarker(config); err != nil {
		return failedAfterACL(err)
	}
	return 0
}

// windowsProcessIsElevated reports whether the current process runs with an
// elevated (Administrator) token. On any error obtaining the token it returns
// true so the setup proceeds and surfaces the real WFP/ACL error rather than a
// false "needs admin" claim.
func windowsProcessIsElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return true
	}
	defer token.Close()
	return token.IsElevated()
}
