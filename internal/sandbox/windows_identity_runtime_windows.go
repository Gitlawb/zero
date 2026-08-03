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

// The opt-in itself (windowsSandboxIdentityEnv and windowsSandboxIdentityEnabled)
// lives in windows_setup.go: it is part of the setup protocol, which the elevated
// half and the command half both have to read the same way, so it cannot be
// Windows-only.

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
		// Deliberately silent, and this is a change of mind worth recording.
		//
		// Announcing it looks right: deny is the DEFAULT network mode, so an
		// operator who opted in never gets a principal for ordinary commands, and
		// that is worth knowing. But the warning cannot be delivered here. This
		// runner is re-exec'd per command as `zero __windows-command-runner`, so the
		// sync.Once below is once per COMMAND, not once per session — the notice
		// would land on the stderr of essentially every sandboxed tool call. Noise
		// that repeats gets filtered by the reader rather than acted on, which is
		// the exact failure the helper's own comment warns about.
		//
		// It is also not actionable per command: windowsSandboxPrincipalEligible
		// prefers network enforcement over read confinement on purpose, so there is
		// nothing to do differently. A standing configuration fact belongs on a
		// surface read once — `zero doctor`, which carries the opt-in now.
		return 0, false, nil
	}
	key := windowsSandboxWorkspaceKey(config.WorkspaceRoots)
	identity, err := lookupWindowsSandboxPrincipalForCommand(key)
	if err != nil {
		if errors.Is(err, errWindowsSandboxIdentityUnavailable) {
			// Not provisioned. On the restricted-token tier the marker check has
			// already refused a command whose opt-in disagrees with setup, so reaching
			// here means the unelevated tier, which validates no marker at all and
			// cannot provision an account (that needs Administrator). Falling back is
			// right — refusing would break every machine-wide opt-in that relies on
			// the unelevated tier — but it must not be silent.
			warnWindowsSandboxPrincipalNotUsed("no sandbox principal is provisioned for this workspace; `zero sandbox setup` from an elevated (Administrator) terminal provisions one")
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

// warnWindowsSandboxPrincipalNotUsed covers the other ways an opted-in command
// ends up on the restricted token: the principal is ineligible for this
// command's policy, or none is provisioned on a tier that validates no marker.
// Neither is an error — both are correct fallbacks — but both leave the operator
// believing an account boundary is isolating them when it is not, which is the
// one thing this backend must never do quietly.
//
// Once per process and behind its own sync.Once, so it neither silences nor is
// silenced by the provisioned-but-secretless warning above.
var warnWindowsSandboxPrincipalNotUsed = func(reason string) {
	windowsSandboxPrincipalNotUsedWarnOnce.Do(func() {
		fmt.Fprintf(os.Stderr,
			"[zero] %s is set, but this command is not running as a sandbox principal: %s. "+
				"Falling back to the restricted-token sandbox, which does not confine reads.\n",
			windowsSandboxIdentityEnv, reason)
	})
}

var windowsSandboxPrincipalNotUsedWarnOnce sync.Once

// provisionWindowsSandboxPrincipalForSetup does the elevated half: create the
// account, grant it the batch logon right, and store its password locked to the
// invoking user. Called from `zero sandbox setup`.
//
// The password is written BEFORE the caller applies any ACL plan, so a setup
// that fails partway leaves a principal that can at least be logged on and
// therefore cleaned up, rather than an account nothing holds the secret for.
func provisionWindowsSandboxPrincipalForSetup(config WindowsSandboxCommandConfig) (windowsSandboxIdentity, bool, error) {
	key := windowsSandboxWorkspaceKey(config.WorkspaceRoots)
	identity, password, created, err := provisionWindowsSandboxIdentityFn(key)

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
	undo := func() error {
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
		//
		// This one failure is reported rather than swallowed. The others below
		// leave residue; this one leaves a CREDENTIAL for a password that no
		// longer works, which is the stale-secret state the invariant above
		// exists to prevent. Staying quiet would claim the invariant was restored
		// when it was not, and the operator would instead meet a
		// provisioned-but-unusable principal on the next command.
		var cleanupErr error
		if secretPath != "" && (created || rotated) {
			if err := removeWindowsSandboxSecretFn(secretPath); err != nil {
				cleanupErr = fmt.Errorf(
					"sandbox secret %s is stale and could not be removed, so the next command will "+
						"fail to log the principal on rather than falling back; delete it and re-run "+
						"`zero sandbox setup`: %w", secretPath, err)
			}
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
		return cleanupErr
	}

	if err != nil {
		// provisionWindowsSandboxIdentity can fail after creating the account, so
		// this path needs the same cleanup even though nothing below ran.
		return windowsSandboxIdentity{}, false, errors.Join(err, undo())
	}
	rightsAttempted = true
	if err := grantWindowsSandboxLogonRightsFn(identity.SID); err != nil {
		return windowsSandboxIdentity{}, false, errors.Join(err, undo())
	}
	if secretPathErr != nil {
		return windowsSandboxIdentity{}, false, errors.Join(secretPathErr, undo())
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
			return windowsSandboxIdentity{}, false, errors.Join(err, undo())
		}
		rotated = true
	}
	if err := writeWindowsSandboxSecretFn(secretPath, password); err != nil {
		return windowsSandboxIdentity{}, false, errors.Join(err, undo())
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
	writeRoots := filesystem.WriteRoots
	// The runtime tree has to be granted here, at setup, because nothing grants it
	// later.
	//
	// permissionProfileWithRuntime appends the per-workspace runtime root to
	// WriteRoots on every COMMAND, and redirects HOME, GOCACHE, npm_config_cache
	// and friends into it. That root lives under the user cache, not the
	// workspace, so the profile setup sees never contains it. On the
	// restricted-token path that costs nothing, since the child still runs as the
	// caller and already has rights there. A principal is a separate local account
	// with none, so without this every npm install, go build or pip install fails
	// on a cache write with a bare ACCESS_DENIED and nothing pointing at the
	// sandbox as the cause.
	if runtimeRoot, err := setupWindowsSandboxRuntimeRoot(config); err != nil {
		_ = removePrincipal()
		return nil, err
	} else if runtimeRoot != "" {
		writeRoots = append(append([]WritableRoot{}, writeRoots...), WritableRoot{Root: runtimeRoot})
	}
	revertACL, err := applyWindowsPrincipalACLs(identity.SID.String(), filesystem, writeRoots)
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

// removeWindowsSandboxPrincipalForSetup retires a workspace's principal in the
// order that leaves nothing behind: secret, then ACEs, then LSA logon rights,
// then the account itself. Everything keyed to the SID has to go while the SID
// still resolves.
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
		// ACEs first, for the same reason: once the account is gone its SID stops
		// resolving and every ACE naming it becomes an orphaned raw-SID entry on
		// the user's own tree, which is precisely the residue the capability-SID
		// model left behind and this one exists to avoid. Revocation is by
		// trustee, so it clears grants written by older versions too.
		//
		// Failing to revoke is not fatal. A path the user has since deleted or
		// renamed cannot be cleaned, and refusing to remove the account over it
		// would strand the principal and its logon rights permanently — a worse
		// outcome than a leftover ACE on a path that may not exist any more.
		//
		// The rollback is discarded here on purpose, unlike at setup: this is
		// teardown, the account is about to be deleted, and putting its ACEs back
		// is the opposite of what the caller asked for.
		if paths, pathsErr := windowsPrincipalTeardownPaths(config, identity.SID.String()); pathsErr == nil {
			_, _ = revokeWindowsPrincipalACEs(identity.SID.String(), paths)
		}
		if err := revokeWindowsSandboxLogonRights(identity.SID); err != nil {
			return err
		}
	} else if !errors.Is(err, errWindowsSandboxIdentityUnavailable) {
		return err
	}
	return removeWindowsSandboxIdentity(username)
}

// setupWindowsSandboxRuntimeRoot resolves this workspace's runtime root and
// makes sure it exists, so the principal ACL plan can name it.
//
// It is created here rather than left to the first command because
// applyWindowsACLPlan skips a target that does not exist: granting write on a
// directory that setup never made would silently no-op, and the failure would
// only show up later as a denied cache write. Creating it under the elevated
// setup process is safe, since it lives under the invoking user's own cache
// directory and prepareSandboxRuntime would create it on the same path anyway.
//
// An empty return means there is no runtime root to grant (no workspace root
// configured), which is not an error: the caller simply grants nothing extra.
func windowsSandboxRuntimeRootPath(config WindowsSandboxCommandConfig) (string, error) {
	workspaceRoot := ""
	for _, candidate := range config.WorkspaceRoots {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			workspaceRoot = canonicalSandboxWorkspaceRoot(trimmed)
			break
		}
	}
	if workspaceRoot == "" {
		return "", nil
	}
	cacheRoot, err := sandboxUserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory for sandbox runtime: %w", err)
	}
	// Same canonicalization as the workspace root above: sandboxRuntimeRootFor
	// compares them, so they have to be the same spelling of the same path.
	cacheRoot = canonicalSandboxWorkspaceRoot(cacheRoot)
	if cacheRoot == "" || cacheRoot == "." {
		return "", errors.New("user cache directory is unavailable for sandbox runtime")
	}
	return sandboxRuntimeRootFor(workspaceRoot, cacheRoot)
}

// windowsSandboxDeterministicRuntimeRootPath names the cache-derived runtime
// tree without creating anything, and returns "" when that tree is unusable
// because it would land inside the workspace.
//
// Teardown needs this rather than windowsSandboxRuntimeRootPath: that one ends
// in sandboxRuntimeRootFor, whose fallback calls os.MkdirTemp, so merely asking
// for the name would make a directory on the way out.
func windowsSandboxDeterministicRuntimeRootPath(config WindowsSandboxCommandConfig) (string, error) {
	workspaceRoot := ""
	for _, candidate := range config.WorkspaceRoots {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			workspaceRoot = canonicalSandboxWorkspaceRoot(trimmed)
			break
		}
	}
	if workspaceRoot == "" {
		return "", nil
	}
	cacheRoot, err := sandboxUserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory for sandbox runtime: %w", err)
	}
	cacheRoot = canonicalSandboxWorkspaceRoot(cacheRoot)
	if cacheRoot == "" || cacheRoot == "." {
		return "", errors.New("user cache directory is unavailable for sandbox runtime")
	}
	root, ok := deterministicSandboxRuntimeRoot(workspaceRoot, cacheRoot)
	if !ok {
		return "", nil
	}
	return root, nil
}

// setupWindowsSandboxRuntimeRoot resolves the runtime root AND creates it.
// Teardown wants the name without the side effect, so the derivation lives in
// windowsSandboxRuntimeRootPath above and this only adds the mkdir.
func setupWindowsSandboxRuntimeRoot(config WindowsSandboxCommandConfig) (string, error) {
	root, err := windowsSandboxRuntimeRootPath(config)
	if err != nil || root == "" {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create sandbox runtime root: %w", err)
	}
	return root, nil
}

// revokeWindowsPrincipalACEs drops every ACE naming principalSID on paths and
// returns a rollback that puts them back.
//
// A path that does not exist is skipped rather than failing: revocation is
// cleanup, and there is nothing to clean on a path that was never created.
func revokeWindowsPrincipalACEs(principalSID string, paths []string) (func() error, error) {
	if len(paths) == 0 {
		return func() error { return nil }, nil
	}
	plan, err := windowsPrincipalRevokePlan(principalSID, paths)
	if err != nil {
		return nil, err
	}
	return applyWindowsACLPlanFn(plan)
}

// applyWindowsPrincipalACLs writes the principal's ACEs for one policy: it
// revokes whatever this trustee already had on the paths the plan touches, then
// applies the plan.
//
// The order is the whole point. applyWindowsACLPlan MERGES into the existing
// DACL, so without the revocation first a re-run after narrowing a write root
// or shortening a deny list leaves the previous, wider ACEs beside the new ones
// and the principal keeps access the current policy no longer grants — the
// sandbox silently widens as a result of tightening it. Setup does get the
// chance to notice: marker validation refuses commands with "permission roots
// or deny lists changed" until setup runs again.
//
// Revocation is by TRUSTEE, so it drops every ACE naming this principal on
// these paths whatever an older version of Zero granted. Its rollback is
// discarded on purpose: the only failure path from here removes the principal
// outright, and restoring stale ACEs for an account about to be deleted is the
// residue this exists to prevent.
func applyWindowsPrincipalACLs(principalSID string, filesystem FileSystemPolicy, writeRoots []WritableRoot) (func() error, error) {
	plan, err := buildWindowsPrincipalACLPlan(windowsPrincipalACLInput{
		PrincipalSID: principalSID,
		WriteRoots:   writeRoots,
		ReadRoots:    filesystem.ReadRoots,
		DenyRead:     filesystem.DenyRead,
		DenyWrite:    filesystem.DenyWrite,
	})
	if err != nil {
		return nil, err
	}
	// The revocation's own rollback matters, and discarding it was a real bug.
	//
	// It was discarded on the reasoning that the only failure path from here
	// removes the principal outright, so restoring stale ACEs for an account
	// about to be deleted would be pointless. That holds for a principal this
	// run CREATED. It is false for one this run ADOPTED: setup keeps a
	// pre-existing account on failure rather than destroying someone else's
	// working principal, so discarding the snapshot left that account alive with
	// its previous ACEs stripped and the new ones rolled back — logged on and
	// unable to reach its own workspace.
	//
	// Restoring the pre-revocation DACL first, then the grant, unwinds in the
	// reverse order they were applied.
	restoreRevoked, err := revokeWindowsPrincipalACEs(principalSID, windowsACLPlanPaths(plan))
	if err != nil {
		return nil, err
	}
	revertGrant, err := applyWindowsACLPlanFn(plan)
	if err != nil {
		if restoreRevoked != nil {
			_ = restoreRevoked()
		}
		return nil, err
	}
	return func() error {
		grantErr := revertGrant()
		// Restore the pre-revocation ACEs even when reverting the grant failed:
		// leaving the principal with neither set is the state this exists to
		// avoid. Report the grant error, since that is the one leaving residue.
		var restoreErr error
		if restoreRevoked != nil {
			restoreErr = restoreRevoked()
		}
		if grantErr != nil {
			return grantErr
		}
		return restoreErr
	}, nil
}

// windowsPrincipalTeardownPaths names every path this principal could hold an
// ACE on: the policy's roots plus the per-workspace runtime tree.
//
// The runtime root is derived through deterministicSandboxRuntimeRoot rather
// than the resolver setup uses, because teardown must create nothing on its way
// out and that resolver's fallback calls os.MkdirTemp. When the deterministic
// root is unusable there is simply no runtime tree to revoke: the fallback root
// commands used was random and per-process, so nothing here could name it
// anyway.
func windowsPrincipalTeardownPaths(config WindowsSandboxCommandConfig, principalSID string) ([]string, error) {
	filesystem := config.PermissionProfile.FileSystem
	writeRoots := filesystem.WriteRoots
	runtimeRoot, err := windowsSandboxDeterministicRuntimeRootPath(config)
	if err != nil {
		return nil, err
	}
	if runtimeRoot != "" {
		writeRoots = append(append([]WritableRoot{}, writeRoots...), WritableRoot{Root: runtimeRoot})
	}
	plan, err := buildWindowsPrincipalACLPlan(windowsPrincipalACLInput{
		PrincipalSID: principalSID,
		WriteRoots:   writeRoots,
		ReadRoots:    filesystem.ReadRoots,
		DenyRead:     filesystem.DenyRead,
		DenyWrite:    filesystem.DenyWrite,
	})
	if err != nil {
		return nil, err
	}
	return windowsACLPlanPaths(plan), nil
}

// Seams for the two elevated calls the provisioning rollback depends on, so the
// stale-secret recovery path is reachable in tests without an elevated machine.
var (
	provisionWindowsSandboxIdentityFn = provisionWindowsSandboxIdentity
	removeWindowsSandboxSecretFn      = removeWindowsSandboxSecret
	writeWindowsSandboxSecretFn       = writeWindowsSandboxSecret
)
