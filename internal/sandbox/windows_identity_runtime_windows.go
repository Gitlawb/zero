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
	"os"
	"strings"

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

// provisionWindowsSandboxPrincipalForSetup does the elevated half: create the
// account, grant it the batch logon right, and store its password locked to the
// invoking user. Called from `zero sandbox setup`.
//
// The password is written BEFORE the caller applies any ACL plan, so a setup
// that fails partway leaves a principal that can at least be logged on and
// therefore cleaned up, rather than an account nothing holds the secret for.
func provisionWindowsSandboxPrincipalForSetup(config WindowsSandboxCommandConfig) (windowsSandboxIdentity, error) {
	key := windowsSandboxWorkspaceKey(config.WorkspaceRoots)
	identity, password, err := provisionWindowsSandboxIdentity(key)
	if err != nil {
		return windowsSandboxIdentity{}, err
	}
	if err := grantWindowsSandboxLogonRights(identity.SID); err != nil {
		return windowsSandboxIdentity{}, err
	}
	secretPath, err := windowsSandboxSecretPath(config.SandboxHome, identity.Username)
	if err != nil {
		return windowsSandboxIdentity{}, err
	}
	// A pre-existing account keeps its old password, which this new one does not
	// match, so the secret is rewritten every run to stay in step with whatever
	// NetUserAdd left in place. On a fresh account the two agree by construction;
	// on an existing one the caller resets it via ensureWindowsSandboxUser.
	if err := writeWindowsSandboxSecret(secretPath, password); err != nil {
		return windowsSandboxIdentity{}, err
	}
	return identity, nil
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
	identity, err := provisionWindowsSandboxPrincipalForSetup(config)
	if err != nil {
		return nil, err
	}
	removePrincipal := func() error { return removeWindowsSandboxPrincipalForSetup(config) }

	filesystem := config.PermissionProfile.FileSystem
	plan, err := buildWindowsPrincipalACLPlan(windowsPrincipalACLInput{
		PrincipalSID: identity.SID.String(),
		WriteRoots:   filesystem.WriteRoots,
		ReadRoots:    filesystem.ReadRoots,
		DenyRead:     filesystem.DenyRead,
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
	return removeWindowsSandboxIdentity(username)
}
