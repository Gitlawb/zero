//go:build windows

package sandbox

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/sys/windows"
)

// Seams for the setup transaction's external effects.
//
// Every step below needs something a test machine cannot be asked for: an
// elevated token, a machine-global mutex, real DACLs, the WFP engine, a local
// account. That left the ORDER of those steps and the handling of each failure
// — which is where this function's bugs actually live — with no coverage at all.
// Each var defaults to its production function and is only ever reassigned by a
// test. removeWindowsSandboxPrincipalForSetupFn and applyWindowsACLPlanFn
// already existed in windows_identity_windows.go for the same reason; the rest
// follow them, and the ACL call below now goes through that one so a single stub
// covers both plans.
var (
	windowsProcessIsElevatedFn       = windowsProcessIsElevated
	acquireWindowsSandboxSetupLockFn = acquireWindowsSandboxSetupLock
	applyWindowsNetworkPlanFn        = applyWindowsNetworkPlan
	setupWindowsSandboxPrincipalFn   = setupWindowsSandboxPrincipal
	writeWindowsSandboxSetupMarkerFn = WriteWindowsSandboxSetupMarker
)

func runWindowsSandboxSetup(config WindowsSandboxSetupConfig, stderr io.Writer) int {
	// Applying the WFP network filters and workspace ACLs requires Administrator
	// rights; without them WFP fails deep inside with a raw ACCESS_DENIED (0x5).
	// Check up front and return an actionable message instead.
	if !windowsProcessIsElevatedFn() {
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
	lock, err := acquireWindowsSandboxSetupLockFn(windowsSandboxWorkspaceKey(config.WorkspaceRoots))
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

	// Before anything is provisioned: a principal this helper could not hand back
	// to the caller must not be created at all.
	if config.PrincipalOptIn {
		if err := assertWindowsSetupRunsAsCaller(config.CallerSID); err != nil {
			fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
			return 1
		}
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
	rollback, err := applyWindowsACLPlanFn(plan)
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
		principalRollback, err := setupWindowsSandboxPrincipalFn(config.commandConfig())
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
	} else if err := removeWindowsSandboxPrincipalForSetupFn(config.commandConfig()); err != nil {
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
		// Fatal, and the first cut of this branch had it wrong: it printed and
		// carried on to write an opted-out marker and exit 0.
		//
		// The reasoning was that teardown is idempotent, so a machine that never
		// had a principal passes straight through and a failure here means real
		// residue rather than a missing account. Both halves are true; the
		// conclusion does not follow. Real residue is precisely the case that must
		// not be reported as success, because the marker then says "no principal"
		// while the account, its secret, its logon right, its ACEs and its ledger
		// are all still there — and nothing afterwards looks for a principal it
		// believes was never provisioned, so the leftovers are permanent. Exiting
		// non-zero is the only thing that keeps the operator in the loop, and
		// re-running setup is the repair.
		if rollbackErr := rollback(); rollbackErr != nil {
			fmt.Fprintf(stderr, "%s: opted out, but retiring the existing sandbox principal failed: %v; rollback failed: %v\n",
				WindowsSandboxSetupName, err, rollbackErr)
			return 1
		}
		fmt.Fprintf(stderr, "%s: opted out, but retiring the existing sandbox principal failed: %v\n",
			WindowsSandboxSetupName, err)
		return 1
	}
	if err := applyWindowsNetworkPlanFn(networkPlan); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			fmt.Fprintf(stderr, "%s: %v; rollback failed: %v\n", WindowsSandboxSetupName, err, rollbackErr)
			return 1
		}
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	if _, err := writeWindowsSandboxSetupMarkerFn(config); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			fmt.Fprintf(stderr, "%s: %v; rollback failed: %v\n", WindowsSandboxSetupName, err, rollbackErr)
			return 1
		}
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 1
	}
	return 0
}

// assertWindowsSetupRunsAsCaller refuses to provision a sandbox principal when
// this elevated helper belongs to a different Windows user than the caller that
// launched it.
//
// A principal is unusable across that boundary, and not for a reason more
// plumbing can fix. Its password is sealed with CryptProtectData, which derives
// its key from the CALLING user, so a blob written here is decryptable only by
// this administrator; the caller's next command would find the account, find the
// secret file, and fail to unseal it. Threading the caller's SID through the
// setup args fixes the account NAME, the ledger and the ACEs — everything that
// is merely named after the invoking user — but it cannot re-key DPAPI for a
// user whose token this process does not hold.
//
// So the answer is to say so before anything exists, rather than provision an
// account, a secret and a grant that resolve to a sandbox nobody can log into.
//
// Same-account elevation is unaffected: a UAC consent prompt splits the caller's
// token but leaves the user SID identical, which is the ordinary path. Only
// over-the-shoulder elevation and `runas /user:` land here. An unknown caller
// SID (an older caller, or a token query that failed) is treated as a match:
// that is the pre-existing behaviour, and refusing on absence would break setup
// on machines where nothing is wrong.
//
// Scoped to the opt-in by its caller, deliberately. The default restricted-token
// sandbox stores no secret and names no account after the user, so it works
// perfectly well across this boundary and must keep doing so.
func assertWindowsSetupRunsAsCaller(callerSID string) error {
	caller := strings.TrimSpace(callerSID)
	if caller == "" {
		return nil
	}
	current := windowsCurrentUserSID()
	if current == "" || strings.EqualFold(current, caller) {
		return nil
	}
	return fmt.Errorf("this elevated setup is running as a different Windows user (%s) than the one that started it (%s), "+
		"and a sandbox principal's password is sealed to the user that stores it, so the account provisioned here could never be used. "+
		"Re-run `zero sandbox setup` from a terminal elevated as your own account, or unset %s to use the restricted-token sandbox",
		current, caller, windowsSandboxIdentityEnv)
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
