//go:build windows

package sandbox

import (
	"errors"
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
		// Fatal only when the ACCOUNT survived, which is a narrower rule than
		// either of the two this branch has had.
		//
		// It first printed and carried on, which was wrong: a marker that says "no
		// principal" while the account is still installed is a lie nothing
		// afterwards can detect, because nothing looks for a principal it believes
		// was never provisioned.
		//
		// Then it failed on any error at all, which was wrong in the other
		// direction. removeWindowsSandboxPrincipalForSetup deliberately continues
		// past a secret file owned by ANOTHER administrator's setup and reports it
		// at the end, so that one unlinkable file does not strand the account, its
		// logon rights and its ACEs. Treating that report as fatal made the whole
		// default sandbox unsetupable on such a machine, permanently, over inert
		// residue: the account is already gone and the leftover is a file with a
		// DACL we do not own.
		//
		// So the question is not whether teardown reported a problem, it is
		// whether the thing the marker is about to deny is still there. Asked of
		// the account rather than inferred from the error, because the error is
		// a join and its parts are not separable at this distance.
		if windowsSandboxPrincipalIsInstalled(config.commandConfig()) {
			if rollbackErr := rollback(); rollbackErr != nil {
				fmt.Fprintf(stderr, "%s: opted out, but the existing sandbox principal is still installed: %v; rollback failed: %v\n",
					WindowsSandboxSetupName, err, rollbackErr)
				return 1
			}
			fmt.Fprintf(stderr, "%s: opted out, but the existing sandbox principal is still installed: %v\n",
				WindowsSandboxSetupName, err)
			return 1
		}
		// Retired, with something inert left over. Named rather than swallowed:
		// whoever has to clean it up needs to know it is there.
		fmt.Fprintf(stderr, "%s: opted out; the sandbox principal was retired, but some of its residue could not be removed: %v\n",
			WindowsSandboxSetupName, err)
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

// windowsSandboxPrincipalIsInstalled reports whether this workspace's principal
// account still resolves.
//
// Fails CLOSED. Only an outright "not provisioned" counts as gone; a name that
// resolves to something unexpected, or a lookup that fails for its own reasons,
// is treated as still installed. The consumer uses this to decide whether an
// opted-out marker would be a lie, and the expensive mistake there is claiming a
// principal is gone when nobody actually checked.
func windowsSandboxPrincipalIsInstalled(config WindowsSandboxCommandConfig) bool {
	_, err := lookupWindowsSandboxIdentityFn(windowsSandboxPrincipalKey(config))
	return !errors.Is(err, errWindowsSandboxIdentityUnavailable)
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
// token but leaves the user SID identical, which is the ordinary path. An
// unknown caller SID (an older caller, or a token query that failed) is treated
// as a match: that is the pre-existing behaviour, and refusing on absence would
// break setup on machines where nothing is wrong.
//
// What this does NOT do, stated plainly because the name suggests otherwise:
// it cannot fire on any path Zero itself takes. Zero never elevates the helper.
// runSandboxSetupHelper (internal/cli/app.go) is a plain exec.Command with no
// token work, and runWindowsSandboxSetup refuses unless the process is ALREADY
// elevated, so the operator supplies elevation by opening an elevated terminal.
// Both halves then run in that terminal: BuildWindowsSandboxSetupArgs resolves
// the caller SID from the very process that later becomes the helper, so the two
// are equal by construction and this returns nil every time.
//
// It is kept rather than deleted because it is a correctness assertion, not a
// no-op. It becomes load-bearing the moment anyone adds a ShellExecute "runas"
// path, which is the natural way this command grows, and it already covers a
// helper .exe invoked directly with hand-written args. What it must not do is be
// mistaken for a live defence against over-the-shoulder elevation today.
//
// The residual hazard that boundary was supposed to describe is real but has a
// different shape: an operator who elevates a terminal as a DIFFERENT
// administrator runs both halves as that administrator, so setup provisions into
// THAT account's sandbox home. Their own unelevated session resolves its own
// home, finds no marker, and is told to run setup. Loud, not silent, and not a
// weakened sandbox.
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
