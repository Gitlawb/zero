package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const WindowsSandboxSetupName = "zero-windows-sandbox-setup.exe"

// Bumped to 5 when setup began recording the CONCRETE runtime root it
// provisioned, and stamping that tree, instead of fingerprinting a plan built
// from a root it merely derived. A marker written by the previous version has no
// stamp, and requiring one without a bump would report every already-set-up
// machine as broken rather than as out of date. Bumping says the true thing: the
// setup protocol changed, run it once more.
const windowsSandboxSetupMarkerSchemaVersion = 5

type WindowsSandboxSetupArgsOptions struct {
	SandboxHome       string
	CommandCWD        string
	WorkspaceRoots    []string
	PermissionProfile PermissionProfile
}

type WindowsSandboxSetupConfig struct {
	SandboxHome       string
	CommandCWD        string
	WorkspaceRoots    []string
	PermissionProfile PermissionProfile
}

type WindowsSandboxSetupMarker struct {
	SchemaVersion  int    `json:"schemaVersion"`
	ACLPlanHash    string `json:"aclPlanHash"`
	ACLPlanEntries int    `json:"aclPlanEntries"`
	// NetworkInfraHash fingerprints the mode-INDEPENDENT network infrastructure
	// setup provisioned (block filters scoped to the offline-marker SID), so one
	// marker validly serves both an allow command and a deny command. It replaces
	// the old per-command NetworkPolicyHash/NetworkPlanHash, which locked the
	// marker to a single mode and bricked approved network commands.
	NetworkInfraHash string `json:"networkInfraHash"`
	OfflineFilterSID string `json:"offlineFilterSid"`
	NetworkFilters   int    `json:"networkFilters"`
}

func WindowsSandboxSetupMarkerPath(sandboxHome string) string {
	return filepath.Join(sandboxHome, "windows-setup.json")
}

func BuildWindowsSandboxSetupArgs(options WindowsSandboxSetupArgsOptions) ([]string, error) {
	commandCWD := strings.TrimSpace(options.CommandCWD)
	if commandCWD == "" {
		return nil, errors.New("windows sandbox setup requires command cwd")
	}
	sandboxHome := strings.TrimSpace(options.SandboxHome)
	if sandboxHome == "" {
		var err error
		sandboxHome, err = ResolveWindowsSandboxHome(nil)
		if err != nil {
			return nil, err
		}
	}
	workspaceRoots := trimNonEmptyStrings(options.WorkspaceRoots)
	if len(workspaceRoots) == 0 {
		workspaceRoots = []string{commandCWD}
	}
	// SELECTED, NOT DERIVED, and selected here in the operator's shell because a
	// command runs in that same environment and will reach the same answer.
	//
	// This used to derive the cache-based root and fingerprint a plan naming it.
	// A command derived the same root, failed to LEASE it, and silently relocated
	// to the temp fallback, so its plan named a tree setup had never provisioned
	// and the marker rejected the command with "permission roots or deny lists
	// changed" -- which blames permissions for a runtime-root disagreement.
	// Re-running setup could not recover, because setup chose the same unleasable
	// root again. That is a permanent brick rather than a retry.
	//
	// selectSandboxRuntimeRoot is the function commands use, lease attempt and
	// fallback included, so setup provisions the tree a command will actually
	// select. The lease is released straight away: it is taken here only to learn
	// which root wins, and the command acquires its own.
	//
	// A selection failure is not fatal here. The old derivation is still applied
	// below, so a machine where the lease cannot be taken at all behaves exactly
	// as it did before rather than losing the ability to run setup.
	if selected, lease, selectErr := selectSandboxRuntimeRoot(firstNonEmpty(workspaceRoots...)); selectErr == nil {
		lease.release()
		options.PermissionProfile = permissionProfileWithRuntime(options.PermissionProfile, SandboxRuntime{Root: selected})
	}
	options.PermissionProfile = WindowsSandboxProfileWithRuntimeRoots(options.PermissionProfile, workspaceRoots)
	profileJSON, err := json.Marshal(options.PermissionProfile)
	if err != nil {
		return nil, fmt.Errorf("marshal windows sandbox setup permission profile: %w", err)
	}
	args := []string{
		"--sandbox-home", sandboxHome,
		"--command-cwd", commandCWD,
		"--permission-profile", string(profileJSON),
	}
	for _, root := range workspaceRoots {
		args = append(args, "--workspace-root", root)
	}
	return args, nil
}

func ParseWindowsSandboxSetupArgs(args []string) (WindowsSandboxSetupConfig, error) {
	var config WindowsSandboxSetupConfig
	var profileJSON string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--command-cwd":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxSetupConfig{}, err
			}
			config.CommandCWD = strings.TrimSpace(value)
			index = next
		case "--sandbox-home":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxSetupConfig{}, err
			}
			config.SandboxHome = strings.TrimSpace(value)
			index = next
		case "--workspace-root":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxSetupConfig{}, err
			}
			if root := strings.TrimSpace(value); root != "" {
				config.WorkspaceRoots = append(config.WorkspaceRoots, root)
			}
			index = next
		case "--permission-profile":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxSetupConfig{}, err
			}
			profileJSON = strings.TrimSpace(value)
			index = next
		default:
			return WindowsSandboxSetupConfig{}, fmt.Errorf("unknown windows sandbox setup flag %q", arg)
		}
	}
	if config.CommandCWD == "" {
		return WindowsSandboxSetupConfig{}, errors.New("missing --command-cwd")
	}
	if config.SandboxHome == "" {
		return WindowsSandboxSetupConfig{}, errors.New("missing --sandbox-home")
	}
	if len(config.WorkspaceRoots) == 0 {
		config.WorkspaceRoots = []string{config.CommandCWD}
	}
	if profileJSON == "" {
		return WindowsSandboxSetupConfig{}, errors.New("missing --permission-profile")
	}
	if err := json.Unmarshal([]byte(profileJSON), &config.PermissionProfile); err != nil {
		return WindowsSandboxSetupConfig{}, fmt.Errorf("invalid --permission-profile: %w", err)
	}
	return config, nil
}

func RunWindowsSandboxSetup(args []string, stderr io.Writer) int {
	config, err := ParseWindowsSandboxSetupArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxSetupName+": "+err.Error())
		return 2
	}
	return runWindowsSandboxSetup(config, stderr)
}

func (config WindowsSandboxSetupConfig) commandConfig() WindowsSandboxCommandConfig {
	return WindowsSandboxCommandConfig{
		SandboxHome:       config.SandboxHome,
		CommandCWD:        config.CommandCWD,
		WorkspaceRoots:    cloneStrings(config.WorkspaceRoots),
		PermissionProfile: config.PermissionProfile,
		SandboxLevel:      WindowsSandboxLevelRestrictedToken,
	}
}

func WindowsSandboxSetupConfigFromCommand(config WindowsSandboxCommandConfig) WindowsSandboxSetupConfig {
	return WindowsSandboxSetupConfig{
		SandboxHome:       config.SandboxHome,
		CommandCWD:        config.CommandCWD,
		WorkspaceRoots:    cloneStrings(config.WorkspaceRoots),
		PermissionProfile: config.PermissionProfile,
	}
}

func BuildWindowsSandboxSetupMarker(config WindowsSandboxSetupConfig) (WindowsSandboxSetupMarker, error) {
	plan, err := BuildWindowsACLPlan(config.commandConfig())
	if err != nil {
		return WindowsSandboxSetupMarker{}, err
	}
	hash, err := WindowsACLPlanHash(plan)
	if err != nil {
		return WindowsSandboxSetupMarker{}, err
	}
	// Fingerprint the mode-INDEPENDENT network infrastructure (block filters
	// scoped to the offline-marker SID), NOT the per-command network mode, so the
	// marker validates for both allow and deny commands against this one setup.
	infraPlan, err := BuildWindowsNetworkInfraPlan(config.commandConfig())
	if err != nil {
		return WindowsSandboxSetupMarker{}, err
	}
	infraHash, err := WindowsNetworkInfraHash(infraPlan)
	if err != nil {
		return WindowsSandboxSetupMarker{}, err
	}
	offlineSID := ""
	if len(infraPlan.IdentitySIDs) > 0 {
		offlineSID = infraPlan.IdentitySIDs[0]
	}
	return WindowsSandboxSetupMarker{
		SchemaVersion:    windowsSandboxSetupMarkerSchemaVersion,
		ACLPlanHash:      hash,
		ACLPlanEntries:   len(plan.Entries),
		NetworkInfraHash: infraHash,
		OfflineFilterSID: offlineSID,
		NetworkFilters:   len(infraPlan.Filters),
	}, nil
}

func WriteWindowsSandboxSetupMarker(config WindowsSandboxSetupConfig) (WindowsSandboxSetupMarker, error) {
	marker, err := BuildWindowsSandboxSetupMarker(config)
	if err != nil {
		return WindowsSandboxSetupMarker{}, err
	}
	// Stamped alongside the marker, because this is the one place setup records
	// that it completed and the two have to be recorded together: a marker whose
	// stamp is missing reports setup as current when the tree it provisioned is
	// gone, which is the whole failure. Written FIRST so a marker never outlives
	// its stamp if the process dies between the two.
	if err := writeWindowsSandboxRuntimeStamp(windowsSandboxSelectedRuntimeRoot(config.PermissionProfile), marker.ACLPlanHash); err != nil {
		return WindowsSandboxSetupMarker{}, err
	}
	path := WindowsSandboxSetupMarkerPath(config.SandboxHome)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return WindowsSandboxSetupMarker{}, fmt.Errorf("create windows sandbox setup marker dir: %w", err)
	}
	bytes, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return WindowsSandboxSetupMarker{}, fmt.Errorf("marshal windows sandbox setup marker: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".windows-setup-*.tmp")
	if err != nil {
		return WindowsSandboxSetupMarker{}, fmt.Errorf("create windows sandbox setup marker temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return WindowsSandboxSetupMarker{}, fmt.Errorf("write windows sandbox setup marker temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return WindowsSandboxSetupMarker{}, fmt.Errorf("close windows sandbox setup marker temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return WindowsSandboxSetupMarker{}, fmt.Errorf("replace windows sandbox setup marker: %w", err)
	}
	return marker, nil
}

func ValidateWindowsSandboxSetupMarker(config WindowsSandboxSetupConfig) error {
	expected, err := BuildWindowsSandboxSetupMarker(config)
	if err != nil {
		return err
	}
	path := WindowsSandboxSetupMarkerPath(config.SandboxHome)
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("windows sandbox is not initialized for this workspace — run `zero sandbox setup` from an elevated (Administrator) terminal (missing %s)", filepath.Base(path))
		}
		return fmt.Errorf("read windows sandbox setup marker: %w", err)
	}
	var actual WindowsSandboxSetupMarker
	if err := json.Unmarshal(bytes, &actual); err != nil {
		return fmt.Errorf("parse windows sandbox setup marker: %w", err)
	}
	if actual.SchemaVersion != expected.SchemaVersion {
		return fmt.Errorf("windows sandbox setup is out of date: schema %d, want %d", actual.SchemaVersion, expected.SchemaVersion)
	}
	if actual.ACLPlanHash != expected.ACLPlanHash || actual.ACLPlanEntries != expected.ACLPlanEntries {
		// Name both sides. This message fires when setup and the command derived
		// different runtime roots, and without the hashes the operator cannot tell
		// that case apart from a genuine policy edit.
		return fmt.Errorf("windows sandbox setup is out of date: permission roots or deny lists changed (marker plan %s, %d entries; this command wants %s, %d entries)",
			shortWindowsACLPlanHash(actual.ACLPlanHash), actual.ACLPlanEntries,
			shortWindowsACLPlanHash(expected.ACLPlanHash), expected.ACLPlanEntries)
	}
	// Mode-agnostic: validate the provisioned infrastructure, never the
	// per-command network mode — so an approved (allow) network command and an
	// ordinary (deny) command both validate against this one setup.
	if actual.NetworkInfraHash != expected.NetworkInfraHash {
		return errors.New("windows sandbox setup is out of date: network infrastructure changed")
	}
	if actual.OfflineFilterSID != expected.OfflineFilterSID {
		return errors.New("windows sandbox setup is out of date: offline network identity changed")
	}
	// THE PLAN HASH IS ABOUT PATHNAMES, NOT ABOUT OBJECTS. Everything above
	// compares what setup INTENDED with what this command wants, and both sides
	// agree as long as the same paths are named. Whether the directory those paths
	// resolve to is still the one setup provisioned is a different question, and
	// nothing here was asking it: cleanupSandboxRuntimeRoots evicts inactive roots
	// and the next run recreates the pathname with ordinary permissions, so the
	// hashes still matched while the capability ACE was gone and a
	// WRITE_RESTRICTED token could not write anything under it.
	if err := validateWindowsSandboxRuntimeStamp(config.PermissionProfile, expected.ACLPlanHash); err != nil {
		return err
	}
	if actual.NetworkFilters != expected.NetworkFilters {
		return errors.New("windows sandbox setup is out of date: network enforcement plan changed")
	}
	return nil
}

func WindowsACLPlanHash(plan WindowsACLPlan) (string, error) {
	entries := canonicalWindowsACLEntries(plan.Entries)
	bytes, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("marshal windows ACL plan hash input: %w", err)
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalWindowsACLEntries(entries []WindowsACLEntry) []WindowsACLEntry {
	out := make([]WindowsACLEntry, 0, len(entries))
	for _, entry := range dedupeWindowsACLEntries(entries) {
		entry.Path = windowsCapabilityPathKey(entry.Path)
		entry.Capability = strings.ToLower(strings.TrimSpace(entry.Capability))
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i]
		right := out[j]
		if left.Action != right.Action {
			return left.Action < right.Action
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Capability != right.Capability {
			return left.Capability < right.Capability
		}
		return !left.Materialize && right.Materialize
	})
	return out
}

// windowsSandboxRuntimeRoots returns the runtime root the plan must name.
//
// PINNED to the profile's own runtime when it has one, rather than derived a
// second time. A command's profile has already been through
// permissionProfileWithRuntime, so its runtime tree is the one this process
// chose, created and took a lease on. Asking a separate function to work out
// which tree that "should" be is how the plan comes to name one directory while
// the command writes to another, which is the whole of issue #881.
//
// Deriving BOTH candidates was the previous answer to that problem. It bought
// agreement at the price of putting os.TempDir() into a machine-wide
// fingerprint: setup run under one TEMP recorded a plan that a later parent
// process with a different TEMP could not reproduce, so every command failed the
// equality check even though the cache runtime was untouched and healthy.
// Pinning removes the second derivation instead of trying to keep two in step.
//
// The derive branch below serves callers that have no runtime yet (elevated
// setup, doctor) and goes through sandboxRuntimeRootFor, THE selector
// prepareSandboxRuntime uses, so the two cannot drift.
//
// The FIRST root only, and that is deliberate rather than an oversight. The
// marker compares plan hashes for EQUALITY, and a command presents exactly one
// workspace root, so setup has to derive its candidates from the same single root
// the command will. Deriving them for every root instead would put candidates in
// the marker that no single command reproduces, and every command would fail with
// the same "permission roots or deny lists changed" this pairing exists to fix.
// Nothing passes more than one root today: every construction site is a
// one-element slice. Whoever adds multi-root support has to change the marker to a
// per-root or subset comparison FIRST; widening this function on its own would
// reintroduce the outage.
func windowsSandboxRuntimeRoots(profile PermissionProfile, workspaceRoots []string) []string {
	// The pin. Nothing is derived when the profile already carries the answer,
	// including after prepareSandboxRuntime relocated on a lease failure: the plan
	// names whatever tree the command actually holds.
	if profile.Runtime != nil {
		if root := strings.TrimSpace(profile.Runtime.Root); root != "" {
			return []string{root}
		}
	}
	workspaceRoot := ""
	for _, candidate := range workspaceRoots {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			workspaceRoot = canonicalSandboxWorkspaceRoot(trimmed)
			break
		}
	}
	if workspaceRoot == "" || workspaceRoot == "." {
		return nil
	}
	cacheRoot, err := sandboxUserCacheDir()
	if err != nil {
		return nil
	}
	// Canonicalized exactly as prepareSandboxRuntime canonicalizes it, because
	// sandboxRuntimeRootFor compares this against the workspace root to decide
	// whether the cache-derived tree lands inside it.
	if cacheRoot = canonicalSandboxWorkspaceRoot(cacheRoot); cacheRoot == "" || cacheRoot == "." {
		return nil
	}
	root, err := sandboxRuntimeRootFor(workspaceRoot, cacheRoot)
	if err != nil {
		return nil
	}
	return []string{root}
}

// windowsSandboxProfileWithRuntime adds the runtime candidates as write roots.
//
// Applied on BOTH sides of the setup protocol, which is the whole point. The
// marker fingerprints the capability ACL plan built from this profile, while
// every command reaches the Windows runner having already had
// permissionProfileWithRuntime append the root it selected. Setup fingerprinted
// the bare profile and the command presented an augmented one, so a marker
// written seconds earlier was rejected with "permission roots or deny lists
// changed" and no command could run at all.
//
// Naming the SAME root on both sides makes the two hashes agree, and it puts the
// runtime root into the CAPABILITY plan as well. That second part matters since
// the principal command runs on a WRITE_RESTRICTED token restricted to the
// capability SIDs: a runtime root carrying only the principal ACE satisfies the
// normal token and fails the restricted check, so cache and temp writes were
// denied even once the marker agreed.
func windowsSandboxProfileWithRuntime(profile PermissionProfile, workspaceRoots []string) PermissionProfile {
	candidates := windowsSandboxRuntimeRoots(profile, workspaceRoots)
	if len(candidates) == 0 {
		return profile
	}
	existing := make(map[string]struct{}, len(profile.FileSystem.WriteRoots))
	for _, root := range profile.FileSystem.WriteRoots {
		existing[windowsCapabilityPathKey(root.Root)] = struct{}{}
	}
	writeRoots := append([]WritableRoot{}, profile.FileSystem.WriteRoots...)
	for _, candidate := range candidates {
		if _, ok := existing[windowsCapabilityPathKey(candidate)]; ok {
			continue
		}
		existing[windowsCapabilityPathKey(candidate)] = struct{}{}
		writeRoots = append(writeRoots, WritableRoot{Root: candidate})
	}
	profile.FileSystem.WriteRoots = writeRoots
	return profile
}

// ensureWindowsSandboxRuntimeRoots creates every runtime root the plan grants.
//
// Paired with windowsSandboxProfileWithRuntime: that function puts the root into
// the ACL plan, and this one makes it exist. Splitting the two is what broke
// elevated setup once already, because the capability plan refuses to
// materialize a write root and fails the whole run on a path that is merely
// absent. Both now go through windowsSandboxRuntimeRoots, so they cannot name
// different trees.
//
// Called by WHOEVER APPLIES THE PLAN, which is both tiers rather than only the
// elevated one. Setup applies it under Administrator; the unelevated tier applies
// its own workspace ACLs per command by design, since capability grants on trees
// the user already owns need no privilege. A command creating a runtime root under
// its own cache or temp grants itself nothing it could not create anyway, and the
// tier that skips this is the tier that dies on "windows ACL target does not
// exist".
// windowsRuntimeRootRollback removes the runtime directories one provisioning
// call actually created, and only those.
//
// Setup materializes runtime roots before the network plan, the ACL apply, the
// network apply and the marker write. Every one of those can fail, and the
// existing rollback only restored ACL snapshots, so a failed `zero sandbox
// setup` reported failure and left new persistent state behind. It could not
// clean up even in principle, because provisioning returned nothing about what
// it had made.
type windowsRuntimeRootRollback struct {
	// created is in creation order, outermost first, so undo walks it backwards.
	created []string
}

// run removes what was created, innermost first.
//
// os.Remove rather than os.RemoveAll, deliberately. A directory that is not
// empty by the time we get here holds something this call did not create, and
// removing it would turn a failed setup into data loss. Refusing is the right
// answer: the error is reported and the residue stays findable.
func (rollback windowsRuntimeRootRollback) run() error {
	var errs []error
	for index := len(rollback.created) - 1; index >= 0; index-- {
		path := rollback.created[index]
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove sandbox runtime root %s created by this run: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

// createRuntimeDirRecording is MkdirAll that reports which components it made.
//
// The distinction between "created" and "already there" is the whole contract:
// a pre-existing cache or temp ancestor belongs to the user and must survive a
// failed setup, while the components this run added must not.
// windowsSandboxRuntimeOwnedDepth is how many trailing components of a runtime
// root Zero creates and therefore owns: "zero", "runtime", "v1", "<hash>". See
// deterministicSandboxRuntimeRoot, which joins exactly these under the cache
// root.
const windowsSandboxRuntimeOwnedDepth = 4

// refuseReparsedRuntimeAncestors rejects a reparse point at any component Zero
// creates, so an elevated ACL is never written through one.
//
// ONLY THE COMPONENTS WE OWN. The cache root above them is the user's, and on a
// machine with a redirected LOCALAPPDATA it is legitimately a reparse point, so
// refusing there would break ordinary setups. Everything below it is ours, was
// created by us, and has no business being a link.
//
// The check has to cover EVERY owned component, not just the deepest one that
// exists. A junction planted at "zero" with the components below it created by
// the attacker leaves the deepest existing component an ordinary directory, so a
// check that looks only there passes while creation follows the junction and the
// leaf lands in the attacker's tree. openWindowsACLTarget then opens that leaf,
// sees no reparse point on it, and the capability ACL is written outside the
// runtime hierarchy entirely.
//
// os.Lstat reports a junction as ModeIrregular rather than ModeSymlink, which is
// why both are tested: a Windows junction needs no privilege to create, so this
// is reachable by any local user.
func refuseReparsedRuntimeAncestors(root string) error {
	cleaned := filepath.Clean(root)
	owned := make([]string, 0, windowsSandboxRuntimeOwnedDepth)
	current := cleaned
	for range windowsSandboxRuntimeOwnedDepth {
		owned = append(owned, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	for _, component := range owned {
		info, err := os.Lstat(component)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect sandbox runtime component %s: %w", component, err)
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("refusing to provision the sandbox runtime through a link at %s: a reparse point here would redirect the directory the sandbox is granted write access to", component)
		}
	}
	return nil
}

func createRuntimeDirRecording(root string) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	// Checked BEFORE anything is created, and again by the caller after, because
	// this alone is a check-then-use: an ancestor swapped between the two would
	// still redirect the leaf. Pairing it with the post-check narrows the window
	// to the creation itself rather than to the whole of setup.
	if err := refuseReparsedRuntimeAncestors(root); err != nil {
		return nil, err
	}
	// Find the deepest ancestor that already exists, then create downwards from
	// there, so the record contains exactly the new components.
	var missing []string
	current := filepath.Clean(root)
	for {
		// os.Stat, which FOLLOWS links, deliberately. os.Lstat reports a junction
		// as ModeIrregular rather than as a directory, so using it here refused a
		// redirected LOCALAPPDATA -- an ordinary Windows configuration -- with
		// "exists and is not a directory". Whether a link is acceptable is a
		// different question, answered by refuseReparsedRuntimeAncestors for the
		// components Zero actually owns.
		if info, err := os.Stat(current); err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("sandbox runtime path %s exists and is not a directory", current)
			}
			break
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect sandbox runtime path %s: %w", current, err)
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	var created []string
	for index := len(missing) - 1; index >= 0; index-- {
		if err := os.Mkdir(missing[index], 0o700); err != nil {
			if os.IsExist(err) {
				// Raced with something else creating it; not ours to remove.
				continue
			}
			return created, fmt.Errorf("create sandbox runtime root %s: %w", missing[index], err)
		}
		created = append(created, missing[index])
	}
	// Re-checked after creation. If an ancestor was swapped for a junction while
	// we were creating, the leaf we just made is in the wrong tree, and granting
	// it the capability ACL would put it on someone elses directory. Reported as
	// a failure so the caller rolls back rather than proceeding.
	if err := refuseReparsedRuntimeAncestors(root); err != nil {
		return created, err
	}
	return created, nil
}

func ensureWindowsSandboxRuntimeRoots(profile PermissionProfile, workspaceRoots []string) (windowsRuntimeRootRollback, error) {
	var rollback windowsRuntimeRootRollback
	for _, root := range windowsSandboxRuntimeRoots(profile, workspaceRoots) {
		created, err := createRuntimeDirRecording(root)
		// Appended before the error check: a partial creation still has to be
		// undone, and returning the record with the error is what lets the caller
		// do that.
		rollback.created = append(rollback.created, created...)
		if err != nil {
			return rollback, err
		}
	}
	return rollback, nil
}

// buildWindowsSandboxSetupACLPlan provisions the runtime roots and then builds the
// plan that grants them, in that order.
//
// One function rather than two statements at the call site because the ordering is
// the contract: BuildWindowsACLPlan emits AllowWrite entries for the runtime
// candidates, applyWindowsACLPlan materializes only DenyRead targets, and an
// AllowWrite target that does not exist fails the entire run. The elevated setup
// path had the provisioning omitted once already, which turned a clean `zero
// sandbox setup` into "windows ACL target does not exist". Keeping the two joined
// here means a caller cannot get the plan without the trees it names.
func buildWindowsSandboxSetupACLPlan(config WindowsSandboxSetupConfig) (WindowsACLPlan, windowsRuntimeRootRollback, error) {
	rollback, err := ensureWindowsSandboxRuntimeRoots(config.PermissionProfile, config.WorkspaceRoots)
	if err != nil {
		return WindowsACLPlan{}, rollback, err
	}
	plan, err := BuildWindowsACLPlan(config.commandConfig())
	if err != nil {
		return WindowsACLPlan{}, rollback, err
	}
	return plan, rollback, nil
}

// windowsSandboxProfileWithProvisionedRuntime is the command-side counterpart:
// it creates the runtime candidates and returns the profile that grants them.
//
// Joined for the same reason as the setup helper, and called from the PARENT for
// one more. The unelevated tier applies its own ACL plan inside the re-exec'd
// runner, where TEMP points into the runtime tree; deriving the candidates there
// yields a temp-side root under the redirected temp rather than the one the plan
// names, so the runner can neither derive nor provision them. The parent still has
// the operator's environment, so it does both and the runner only applies.
func windowsSandboxProfileWithProvisionedRuntime(profile PermissionProfile, workspaceRoots []string) (PermissionProfile, error) {
	// The command side does not roll back: it is not transactional, and a runtime
	// root it created is the tree the command is about to use.
	if _, err := ensureWindowsSandboxRuntimeRoots(profile, workspaceRoots); err != nil {
		return PermissionProfile{}, err
	}
	return windowsSandboxProfileWithRuntime(profile, workspaceRoots), nil
}

// shortWindowsACLPlanHash trims a plan hash for a human-facing error. Twelve hex
// characters is plenty to tell two plans apart by eye, and the full 64 buries the
// rest of the message.
func shortWindowsACLPlanHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return "(none)"
	}
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// WindowsSandboxProfileWithRuntimeRoots folds the sandbox runtime roots into a
// permission profile, for callers that build a setup config outside this package.
//
// Call it ONLY from a process whose TEMP and TMP are the operator's. The
// temp-derived candidate reads os.TempDir(), and the sandbox points those
// variables at its own runtime temp for anything it launches, so a process on the
// far side of that redirection derives a path no other process agrees on. The
// command runner is exactly such a process: it takes the profile it is handed.
func WindowsSandboxProfileWithRuntimeRoots(profile PermissionProfile, workspaceRoots []string) PermissionProfile {
	return windowsSandboxProfileWithRuntime(profile, workspaceRoots)
}

// windowsSandboxRuntimeStampName marks a runtime root that ELEVATED SETUP
// actually provisioned and applied the capability ACL to.
const windowsSandboxRuntimeStampName = ".zero-sandbox-setup"

func windowsSandboxRuntimeStampPath(root string) string {
	return filepath.Join(root, windowsSandboxRuntimeStampName)
}

// writeWindowsSandboxRuntimeStamp records, INSIDE the runtime root, that this
// exact tree carries the capability ACL for this exact plan.
//
// The marker alone cannot tell. It hashes ACL-plan ENTRIES, which are pathnames,
// not the objects those pathnames resolve to. cleanupSandboxRuntimeRoots removes
// inactive roots with os.RemoveAll on an age and count policy, and the next run
// for that workspace recreates the same deterministic pathname through
// os.MkdirAll with ordinary inherited permissions. The plan hash is unchanged,
// so both the elevated and the unelevated marker checks reported setup as
// current while the recreated directory carried NO capability ACE, and a
// WRITE_RESTRICTED token could not write TMP, GOCACHE or anything else under it.
//
// A file inside the tree survives exactly as long as the tree does. Eviction
// takes it, ordinary recreation does not restore it, so its absence is precisely
// the condition "this pathname exists but is not the object setup provisioned".
func writeWindowsSandboxRuntimeStamp(root string, planHash string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create sandbox runtime root for the setup stamp: %w", err)
	}
	if err := os.WriteFile(windowsSandboxRuntimeStampPath(root), []byte(planHash), 0o600); err != nil {
		return fmt.Errorf("write sandbox runtime setup stamp: %w", err)
	}
	return nil
}

// validateWindowsSandboxRuntimeStamp reports whether the runtime root this
// command will use is the one setup provisioned.
//
// Absent when the profile carries no runtime root, which is the setup side
// itself and every non-restricted profile, so this adds no requirement where
// there is nothing to check.
func validateWindowsSandboxRuntimeStamp(profile PermissionProfile, planHash string) error {
	if profile.Runtime == nil {
		return nil
	}
	root := strings.TrimSpace(profile.Runtime.Root)
	if root == "" {
		return nil
	}
	recorded, err := os.ReadFile(windowsSandboxRuntimeStampPath(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("the sandbox runtime directory for this workspace was removed since setup ran, so it no longer carries the permissions the sandbox needs — run `zero sandbox setup` from an elevated (Administrator) terminal (%s)", root)
		}
		return fmt.Errorf("read sandbox runtime setup stamp: %w", err)
	}
	if strings.TrimSpace(string(recorded)) != strings.TrimSpace(planHash) {
		return fmt.Errorf("the sandbox runtime directory for this workspace was provisioned for a different configuration — run `zero sandbox setup` from an elevated (Administrator) terminal (%s)", root)
	}
	return nil
}

// windowsSandboxSelectedRuntimeRoot returns the concrete runtime root a profile
// carries, or empty when it carries none.
func windowsSandboxSelectedRuntimeRoot(profile PermissionProfile) string {
	if profile.Runtime == nil {
		return ""
	}
	return strings.TrimSpace(profile.Runtime.Root)
}
