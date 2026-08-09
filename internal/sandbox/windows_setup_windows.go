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
	// Serialize the WHOLE transaction, not the individual writes.
	//
	// Setup rotates the principal's password, stores the secret,
	// read-modify-writes the ACL ledger, installs WFP filters and writes the
	// marker. Two setups for this workspace at once interleave those steps: the
	// account ends up with one process's password while the stored secret is the
	// other's, so every later command fails to log on, and the ledger's
	// read-modify-write silently loses a path set. Each write being individually
	// atomic does nothing about interleaving BETWEEN writes, which is the actual
	// failure, so the lock is taken here and held to the end.
	//
	// Immediately after the elevation check, because acquiring a Global object
	// needs the rights that check just confirmed.
	lock, err := acquireWindowsSandboxSetupLock(windowsSandboxWorkspaceKey(config.WorkspaceRoots))
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	defer lock.release()
	if lock.Abandoned() {
		// The previous holder died somewhere inside the transaction, so this
		// machine may be half set up. Setup is idempotent and re-running it is
		// the repair, but say so: a silent recovery hides that something crashed
		// while holding the machine's sandbox state open.
		fmt.Fprintf(stderr, "%s: a previous setup for this workspace exited without finishing; re-running to repair it.\n",
			WindowsSandboxSetupName)
	}

	plan, err := BuildWindowsACLPlan(config.commandConfig())
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	// Always provision the mode-INDEPENDENT infrastructure: the outbound block
	// filters scoped to the offline-marker SID. Runtime gates network per command
	// by whether the token carries that SID, so one setup serves both modes.
	networkPlan, err := BuildWindowsNetworkInfraPlan(config.commandConfig())
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	rollback, err := applyWindowsACLPlan(plan)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	// Provision this workspace's sandbox principal, when opted in. A principal is
	// a separate local account, so it is created only on an explicit opt-in: it
	// is visible in `net user`, and account creation is exactly the kind of thing
	// endpoint protection and enterprise policy object to. Without the opt-in the
	// capability-SID backend above is the whole of setup, unchanged.
	if windowsSandboxIdentityEnabled(config.commandConfig().Env) {
		principalRollback, err := setupWindowsSandboxPrincipal(config.commandConfig())
		if err != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				fmt.Fprintf(stderr, "%s: %v; rollback failed: %v\n", WindowsSandboxSetupName, err, rollbackErr)
				return 1
			}
			fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
			return 1
		}
		// Fold the principal into the existing rollback so every later failure
		// path undoes it too, rather than each one having to remember.
		aclRollback := rollback
		rollback = func() error {
			principalErr := principalRollback()
			aclErr := aclRollback()
			if principalErr != nil {
				return principalErr
			}
			return aclErr
		}
	} else if err := removeWindowsSandboxPrincipalForSetup(config.commandConfig()); err != nil {
		// Opting out has to actually retire the principal, because that is what
		// we tell people it does. ValidateWindowsSandboxSetupMarker sends an
		// operator here in as many words: re-run setup from an elevated terminal
		// without the opt-in to retire the principal. Until now this branch did
		// not exist, so the marker flipped to opted-out while the account, its
		// secret, its logon rights, its ACEs and its ledger all stayed exactly
		// where they were. The instruction was a lie, and the leftovers were
		// invisible, because nothing afterwards looks for a principal it believes
		// was never provisioned.
		//
		// Not fatal. Teardown is idempotent and a machine that never had a
		// principal passes straight through, so a failure here means genuine
		// residue rather than a missing account: say so and carry on rather than
		// refusing to complete a setup whose sandbox is otherwise fine.
		fmt.Fprintf(stderr, "%s: opted out, but retiring the existing sandbox principal did not complete: %v\n",
			WindowsSandboxSetupName, err)
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
