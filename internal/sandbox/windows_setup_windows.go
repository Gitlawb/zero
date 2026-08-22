//go:build windows

package sandbox

import (
	"fmt"
	"io"
	"strings"

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
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+runWindowsSandboxSetupCompensations(cause, nil, runtimeRollback).Error())
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
	// THE STAMP RIDES WITH THE ACE. Computed before the apply so the apply can
	// write it through the very handle it grants the capability on, which is the
	// only way the two are provably about one object. Writing it afterwards by
	// pathname left a window in which the predictable root could be replaced by an
	// ordinary directory that then collected a valid-looking stamp while carrying
	// no ACE at all.
	marker, err := BuildWindowsSandboxSetupMarker(config)
	if err != nil {
		return failed(err)
	}
	var stamp *windowsACLStampRequest
	if root := windowsSandboxSelectedRuntimeRoot(config.PermissionProfile); strings.TrimSpace(root) != "" {
		stamp = &windowsACLStampRequest{Root: root, PlanHash: marker.ACLPlanHash}
		// Snapshotted BEFORE the apply, because the apply is now what writes the
		// stamp. Taking it afterwards would record this run's own stamp as the
		// state to restore, so a failed setup would put its own artifact back
		// rather than what it found.
		runtimeRollback.stamp = snapshotWindowsSandboxRuntimeStamp(root)
	}
	rollback, err := applyWindowsACLPlanWithStamp(plan, stamp)
	if err != nil {
		return failed(err)
	}
	// From here both have to be undone, ACLs first so the directories are empty
	// of our grants before they are removed. This used to return as soon as the
	// ACL rollback reported an error, so the runtime rollback never ran and a
	// failed setup kept the persistent directories it had just created; one undo
	// failing is the moment the others matter most.
	failedAfterACL := func(cause error) int {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+runWindowsSandboxSetupCompensations(cause, rollback, runtimeRollback).Error())
		return 1
	}
	if err := applyWindowsNetworkPlan(networkPlan); err != nil {
		return failedAfterACL(err)
	}
	// The stamp is already on disk, written through the handle the capability ACE
	// was applied on, so this records the marker only and never names the runtime
	// tree again. The rollback already owns that stamp, snapshotted above, so a
	// failure here still leaves the root removable.
	if err := writeWindowsSandboxSetupMarkerFile(config, marker); err != nil {
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
