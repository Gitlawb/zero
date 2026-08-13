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

const windowsSandboxSetupMarkerSchemaVersion = 4

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
	// Augmented here, in the caller's shell, before the args cross into the
	// elevated helper. The temp-derived candidate reads os.TempDir(), so it has
	// to be resolved where the environment is still the operator's.
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
		return errors.New("windows sandbox setup is out of date: permission roots or deny lists changed")
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

// windowsSandboxRuntimeCandidates returns every runtime root setup provisions.
//
// BOTH candidates, not the one this process would select. sandboxRuntimeRootFor
// prefers the cache-derived root and falls back to the temp-derived one when the
// first would land inside the workspace or its lease cannot be taken, and that
// choice is made per process. Setup that granted only its own choice left the
// other unprovisioned, so a command that fell back wrote to a tree with no ACE
// on it. Both are deterministic now, so setup can cover both and command
// selection lands on a provisioned root either way.
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
func windowsSandboxRuntimeCandidates(workspaceRoots []string) []string {
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
	var roots []string
	if cacheRoot, err := sandboxUserCacheDir(); err == nil {
		if cacheRoot = canonicalSandboxWorkspaceRoot(cacheRoot); cacheRoot != "" && cacheRoot != "." {
			if root, ok := deterministicSandboxRuntimeRoot(workspaceRoot, cacheRoot); ok {
				roots = append(roots, root)
			}
		}
	}
	if root, err := fallbackSandboxRuntimeRoot(workspaceRoot); err == nil {
		roots = append(roots, root)
	}
	return roots
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
// Adding the full candidate set on both sides makes the two hashes agree without
// the command having to know which root setup happened to pick, and it puts the
// runtime roots into the CAPABILITY plan as well. That second part matters since
// the principal command runs on a WRITE_RESTRICTED token restricted to the
// capability SIDs: a runtime root carrying only the principal ACE satisfies the
// normal token and fails the restricted check, so cache and temp writes were
// denied even once the marker agreed.
func windowsSandboxProfileWithRuntime(profile PermissionProfile, workspaceRoots []string) PermissionProfile {
	candidates := windowsSandboxRuntimeCandidates(workspaceRoots)
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

// ensureWindowsSandboxRuntimeCandidates creates every runtime root setup grants.
//
// Paired with windowsSandboxProfileWithRuntime: that function puts the candidates
// into the ACL plan, and this one makes them exist. Splitting the two is what
// broke elevated setup once already, because the capability plan refuses to
// materialize a write root and fails the whole run on a path that is merely
// absent. Whenever one of these grows a candidate, so must the other.
//
// Called by WHOEVER APPLIES THE PLAN, which is both tiers rather than only the
// elevated one. Setup applies it under Administrator; the unelevated tier applies
// its own workspace ACLs per command by design, since capability grants on trees
// the user already owns need no privilege. A command creating a runtime root under
// its own cache or temp grants itself nothing it could not create anyway, and the
// tier that skips this is the tier that dies on "windows ACL target does not
// exist".
func ensureWindowsSandboxRuntimeCandidates(workspaceRoots []string) error {
	for _, root := range windowsSandboxRuntimeCandidates(workspaceRoots) {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return fmt.Errorf("create sandbox runtime root %s: %w", root, err)
		}
	}
	return nil
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
func buildWindowsSandboxSetupACLPlan(config WindowsSandboxSetupConfig) (WindowsACLPlan, error) {
	if err := ensureWindowsSandboxRuntimeCandidates(config.WorkspaceRoots); err != nil {
		return WindowsACLPlan{}, err
	}
	return BuildWindowsACLPlan(config.commandConfig())
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
	if err := ensureWindowsSandboxRuntimeCandidates(workspaceRoots); err != nil {
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
