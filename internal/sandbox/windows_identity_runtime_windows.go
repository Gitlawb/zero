//go:build windows

package sandbox

// Using a sandbox principal at command time.
//
// This is the seam between the identity model and the existing runner. It is
// deliberately fail-soft: when no principal is provisioned, when the secret is
// missing, or when the opt-in is off, it reports "not available" and the caller
// keeps using today's restricted-token backend. Only an outright failure to log
// on with a principal that IS provisioned surfaces as an error, because that
// means setup ran but the identity is broken, and silently downgrading the
// sandbox in that case would be the wrong kind of quiet.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

// windowsSandboxIdentityEnv opts a machine into the principal backend while it
// is still experimental. Provisioning is inert without it, so an existing
// install keeps the restricted-token behaviour until someone turns this on.
const windowsSandboxIdentityEnv = "ZERO_WINDOWS_SANDBOX_IDENTITY"

// windowsSandboxIdentityEnabled reports whether the principal backend is opted
// into. Kept as a function so the check reads the environment at call time,
// which is what lets a test or an elevated setup run flip it.
func windowsSandboxIdentityEnabled(env map[string]string) bool {
	if value, ok := env[windowsSandboxIdentityEnv]; ok {
		return strings.TrimSpace(value) == "1"
	}
	return strings.TrimSpace(os.Getenv(windowsSandboxIdentityEnv)) == "1"
}

// windowsSandboxWorkspaceKey derives the per-workspace key a principal is named
// after. It hashes the workspace root the same way the sandbox runtime keys its
// own state, so the account name leaks no path and one workspace always maps to
// one principal.
func windowsSandboxWorkspaceKey(workspaceRoots []string) string {
	root := ""
	for _, candidate := range workspaceRoots {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			root = normalizeProfilePath(trimmed)
			break
		}
	}
	if root == "" {
		root = "default"
	}
	digest := sha256.Sum256([]byte(strings.ToLower(root)))
	return hex.EncodeToString(digest[:])
}

// windowsSandboxPrincipalEligible reports whether the principal backend may be
// used for this command at all, before any account or secret is consulted.
//
// Kept separate from the lookup so the decision is observable on its own: on a
// machine with nothing provisioned the lookup declines anyway, which would let a
// missing guard here pass unnoticed.
func windowsSandboxPrincipalEligible(config WindowsSandboxCommandConfig) bool {
	if !windowsSandboxIdentityEnabled(config.Env) {
		return false
	}
	// Network denial is enforced by WFP filters keyed to the offline-marker SID,
	// which the restricted token carries and a principal token cannot: LogonUser
	// mints a token for the account, not for a synthetic capability SID. Using a
	// principal here would leave those filters matching nothing and silently drop
	// network enforcement, which is a worse trade than the read confinement it
	// buys. Fall back to the restricted token, which still enforces the network,
	// until the filters are also keyed to the principal's own SID.
	return config.PermissionProfile.Network.Mode != NetworkDeny
}

// windowsSandboxPrincipalToken returns a token for this workspace's sandbox
// principal.
//
// ok is false, with a nil error, whenever the principal backend simply is not in
// play: the opt-in is off, setup has not provisioned an account, or no secret is
// stored. The caller falls back to the restricted token in those cases. An error
// means the identity exists but could not be used, which is worth surfacing
// rather than downgrading around.
func windowsSandboxPrincipalToken(config WindowsSandboxCommandConfig) (windows.Token, bool, error) {
	if !windowsSandboxPrincipalEligible(config) {
		return 0, false, nil
	}
	key := windowsSandboxWorkspaceKey(config.WorkspaceRoots)
	identity, err := lookupWindowsSandboxIdentity(key)
	if err != nil {
		if errors.Is(err, errWindowsSandboxIdentityUnavailable) {
			// Not provisioned: fall back quietly, this is the default state.
			return 0, false, nil
		}
		// The name resolves to something that is not a usable principal, most
		// likely squatted by a local group or alias. That is a conflict an
		// operator has to see, not a reason to pretend setup never ran.
		return 0, false, err
	}
	secretPath, err := windowsSandboxSecretPath(config.SandboxHome, identity.Username)
	if err != nil {
		return 0, false, err
	}
	password, err := readWindowsSandboxSecret(secretPath)
	if err != nil {
		if errors.Is(err, errWindowsSandboxIdentityUnavailable) {
			// The account exists but its password does not. Setup was interrupted
			// or the secret was removed; fall back rather than fail the command.
			//
			// Falling back is right, staying quiet about it was not. The opt-in is
			// set and an account IS provisioned, so the operator asked for
			// principal isolation and is silently getting the weaker same-user
			// restricted token instead. That is the one fail-soft case worth
			// announcing: the others mean the backend was never set up, while this
			// one means it was and has broken since.
			warnWindowsSandboxPrincipalUnavailable(identity.Username)
			return 0, false, nil
		}
		return 0, false, err
	}
	token, err := logonWindowsSandboxPrincipal(identity.Username, password)
	if err != nil {
		// Provisioned but unusable. Surface it: a wrong password or a revoked
		// batch-logon right is a broken sandbox, not an absent one.
		return 0, false, err
	}
	return token, true, nil
}

// warnWindowsSandboxPrincipalUnavailable tells the operator once per process
// that the backend they opted into is not the one running.
//
// Once, because this sits on the command path: a warning per command would be
// noise on every tool call for the whole session, and noise that repeats gets
// filtered out by the reader rather than acted on. Indirected through a var so a
// test can observe it without capturing stderr.
var warnWindowsSandboxPrincipalUnavailable = func(username string) {
	windowsSandboxPrincipalWarnOnce.Do(func() {
		fmt.Fprintf(os.Stderr,
			"[zero] %s is set and sandbox principal %q is provisioned, but its stored password is missing or unreadable. "+
				"Falling back to the restricted-token sandbox, which does not confine reads. "+
				"Re-run `zero sandbox setup` from an elevated terminal to restore it.\n",
			windowsSandboxIdentityEnv, username)
	})
}

var windowsSandboxPrincipalWarnOnce sync.Once

// provisionWindowsSandboxPrincipalForSetup does the elevated half: create the
// account, grant it the batch logon right, and store its password locked to the
// invoking user. Called from `zero sandbox setup`.
//
// The password is written BEFORE the caller applies any ACL plan, so a setup
// that fails partway leaves a principal that can at least be logged on and
// therefore cleaned up, rather than an account nothing holds the secret for.
func provisionWindowsSandboxPrincipalForSetup(config WindowsSandboxCommandConfig) (windowsSandboxIdentity, bool, error) {
	key := windowsSandboxWorkspaceKey(config.WorkspaceRoots)
	identity, password, created, err := provisionWindowsSandboxIdentity(key)

	// Undo whatever this run actually did, in reverse, on any failure after the
	// account exists. Without it a failure between creating the account and
	// storing its secret left the account behind with no caller able to remove
	// it: the rollback the setup path installs is only built once this function
	// has returned successfully.
	//
	// Scoped to what THIS run created on purpose. An account that already existed
	// and belongs to Zero is a working principal from an earlier setup, and
	// deleting it because a later run failed would turn a partial failure into a
	// total one.
	rightsAttempted := false
	rotated := false
	// Resolved from the account name rather than the identity, so it is known
	// before anything can fail.
	secretPath, secretPathErr := windowsSandboxSecretPath(config.SandboxHome, windowsSandboxUserName(key))
	undo := func() {
		// Only when this run invalidated it. The secret is removed if this run
		// created the account, or if it rotated an existing account's password,
		// because in both cases what is on disk cannot authenticate and absent
		// beats stale: the command path treats a missing secret as "not
		// provisioned" and falls back, while a stale one fails the logon and
		// reports a broken sandbox.
		//
		// Removing it unconditionally, as this used to, destroyed a WORKING
		// secret whenever setup failed before rotation on a machine that was
		// already provisioned. The account kept its old password, the only copy
		// of it was deleted, and the sandbox silently degraded.
		if secretPath != "" && (created || rotated) {
			_ = removeWindowsSandboxSecret(secretPath)
		}
		// Only for an account this run created, and attempted rather than
		// completed.
		//
		// Attempted, because grantWindowsSandboxLogonRights adds rights one at a
		// time and returns on the first failure, so a partial grant is possible
		// and gating on success left those entries behind, keyed to a SID that
		// deleting the account then made unresolvable.
		//
		// Created, because revokeWindowsSandboxLogonRights passes AllRights, which
		// drops every right the account holds and deletes its LSA object outright.
		// On an adopted principal that is not a rollback, it is destruction: a
		// transient failure anywhere below would strip the SeBatchLogonRight and
		// deny-logon rights a previous setup established, leaving exactly the
		// broken-but-present principal this whole function exists to avoid. The
		// rights this run granted are the ones the account is supposed to have, so
		// leaving them in place on an adopted account is the safe direction.
		if identity.SID != nil && rightsAttempted && created {
			_ = revokeWindowsSandboxLogonRightsFn(identity.SID)
		}
		if created {
			_ = removeWindowsSandboxIdentity(identity.Username)
		}
	}

	if err != nil {
		// provisionWindowsSandboxIdentity can fail after creating the account, so
		// this path needs the same cleanup even though nothing below ran.
		undo()
		return windowsSandboxIdentity{}, false, err
	}
	rightsAttempted = true
	if err := grantWindowsSandboxLogonRightsFn(identity.SID); err != nil {
		undo()
		return windowsSandboxIdentity{}, false, err
	}
	if secretPathErr != nil {
		undo()
		return windowsSandboxIdentity{}, false, secretPathErr
	}
	// Rotation happens HERE, immediately before the secret is committed, rather
	// than inside provisioning where it used to.
	//
	// A new account already has this password from NetUserAdd, so only an
	// adopted one needs setting. Doing it at the top of provisioning meant every
	// step in between ran with the account's password already replaced and no
	// copy of it stored, so any failure there stranded a working principal. The
	// two operations are now adjacent, which is the smallest window this can
	// have without a way to restore the previous password, which Windows does
	// not offer.
	if !created {
		if err := resetWindowsSandboxUserPasswordFn(identity.Username, password); err != nil {
			undo()
			return windowsSandboxIdentity{}, false, err
		}
		rotated = true
	}
	if err := writeWindowsSandboxSecret(secretPath, password); err != nil {
		undo()
		return windowsSandboxIdentity{}, false, err
	}
	return identity, created, nil
}

// setupWindowsSandboxPrincipal provisions this workspace's principal and grants
// it the filesystem access its permission profile describes. It returns a
// rollback that undoes everything it created, so a later setup step failing does
// not leave a half-provisioned account behind.
//
// Rollback order is the inverse of creation and matters: ACEs are revoked BEFORE
// the account is deleted, because removing the account first would leave ACEs
// naming a SID that no longer resolves, which is the orphaned-entry residue this
// model exists to avoid.
func setupWindowsSandboxPrincipal(config WindowsSandboxCommandConfig) (func() error, error) {
	identity, created, err := provisionWindowsSandboxPrincipalForSetup(config)
	if err != nil {
		return nil, err
	}
	// Scoped to what this run created, the same contract provisioning already
	// applies to its own rollback.
	//
	// Unconditional removal here meant a transient ACL failure during a re-run of
	// elevated setup deleted a principal that was working before the run started,
	// taking its secret and logon rights with it. Provisioning was careful not to
	// do that and then this undid the care one level up. A pre-existing principal
	// is left alone: its ACEs are still reverted, since this run applied them,
	// but the account itself is not this run's to destroy.
	removePrincipal := func() error {
		if !created {
			return nil
		}
		return removeWindowsSandboxPrincipalForSetup(config)
	}

	filesystem := config.PermissionProfile.FileSystem
	plan, err := buildWindowsPrincipalACLPlan(windowsPrincipalACLInput{
		PrincipalSID: identity.SID.String(),
		WriteRoots:   filesystem.WriteRoots,
		ReadRoots:    filesystem.ReadRoots,
		DenyRead:     filesystem.DenyRead,
		DenyWrite:    filesystem.DenyWrite,
	})
	if err != nil {
		_ = removePrincipal()
		return nil, err
	}
	revertACL, err := applyWindowsACLPlan(plan)
	if err != nil {
		_ = removePrincipal()
		return nil, err
	}
	return func() error {
		aclErr := revertACL()
		// Remove the principal even when the ACL revert failed, so a broken
		// rollback does not also strand an account; report the ACL error since it
		// is the one that leaves state behind.
		removeErr := removePrincipal()
		if aclErr != nil {
			return aclErr
		}
		return removeErr
	}, nil
}

// removeWindowsSandboxPrincipalForSetup retires a workspace's principal: secret
// first, then the account. ACE revocation is the caller's job and must happen
// before this, or ACEs naming a deleted SID are left behind.
func removeWindowsSandboxPrincipalForSetup(config WindowsSandboxCommandConfig) error {
	key := windowsSandboxWorkspaceKey(config.WorkspaceRoots)
	username := windowsSandboxUserName(key)
	secretPath, err := windowsSandboxSecretPath(config.SandboxHome, username)
	if err != nil {
		return err
	}
	if err := removeWindowsSandboxSecret(secretPath); err != nil {
		return err
	}
	// Drop the LSA account rights before the account itself. Deleting the account
	// first would leave its rights behind keyed to a SID that no longer resolves,
	// which is the same orphaned residue the trustee-keyed ACE revocation exists
	// to avoid. A principal that was never provisioned has no SID to resolve and
	// nothing to revoke, so that case is not an error.
	if identity, err := lookupWindowsSandboxIdentity(windowsSandboxWorkspaceKey(config.WorkspaceRoots)); err == nil {
		if err := revokeWindowsSandboxLogonRights(identity.SID); err != nil {
			return err
		}
	} else if !errors.Is(err, errWindowsSandboxIdentityUnavailable) {
		return err
	}
	return removeWindowsSandboxIdentity(username)
}
