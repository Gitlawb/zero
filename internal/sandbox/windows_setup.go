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

const windowsSandboxSetupMarkerSchemaVersion = 5

// windowsSandboxIdentityEnv opts a machine into the principal backend while it
// is still experimental. Provisioning is inert without it, so an existing
// install keeps the restricted-token behaviour until someone turns this on.
//
// Lives here, beside the setup protocol rather than beside the Windows-only
// runtime, because the opt-in is part of that protocol: it has to be readable on
// every platform so the setup args and the marker can carry it.
const windowsSandboxIdentityEnv = "ZERO_WINDOWS_SANDBOX_IDENTITY"

// windowsSandboxIdentityEnabled reports whether the principal backend is opted
// into. An explicit entry in env is authoritative; otherwise the process
// environment decides.
func windowsSandboxIdentityEnabled(env map[string]string) bool {
	if value, ok := env[windowsSandboxIdentityEnv]; ok {
		return strings.TrimSpace(value) == "1"
	}
	return strings.TrimSpace(os.Getenv(windowsSandboxIdentityEnv)) == "1"
}

// WindowsSandboxPrincipalOptIn resolves the principal opt-in for callers outside
// this package (the `zero sandbox setup` CLI and `zero doctor`), so both sides
// of the setup protocol read the opt-in the same way. Pass nil to consult the
// current process environment.
func WindowsSandboxPrincipalOptIn(env map[string]string) bool {
	return windowsSandboxIdentityEnabled(env)
}

func windowsSandboxPrincipalOptInValue(optIn bool) string {
	if optIn {
		return "1"
	}
	return "0"
}

type WindowsSandboxSetupArgsOptions struct {
	SandboxHome       string
	CommandCWD        string
	WorkspaceRoots    []string
	PermissionProfile PermissionProfile
	// PrincipalOptIn is the caller's principal opt-in, serialized into the setup
	// args. Elevated setup runs in its own process — a UAC-elevated one whose
	// environment is not the caller's — so it must be told the value rather than
	// left to sample an environment nobody set.
	//
	// Tri-state on purpose. nil means "this caller did not resolve the opt-in",
	// and BuildWindowsSandboxSetupArgs then resolves it from the environment of
	// the process building the args — which is the caller's own process, the one
	// place where the ambient value is the value the operator typed. A plain bool
	// could not say that: its zero value asserts "opted out", so every caller that
	// simply did not know about this field would serialize `--sandbox-principal 0`
	// while the command half still resolved the opt-in from its environment. Under
	// a machine-wide opt-in the two halves would then disagree and marker
	// validation would refuse every command — the same silent-disagreement bug
	// this flag exists to remove, re-created one layer up.
	//
	// Set it only to override the environment (a caller holding a command's Env
	// map, or a test pinning a value); leave it nil to mean "whatever this shell
	// says", which is what `zero sandbox setup` and `zero doctor` want.
	PrincipalOptIn *bool
}

// principalOptIn resolves the tri-state. It runs inside
// BuildWindowsSandboxSetupArgs, i.e. in the caller's process, before the args
// cross the UAC boundary — so an unset caller still ships an explicit 0|1 that
// the elevated helper can trust.
func (options WindowsSandboxSetupArgsOptions) principalOptIn() bool {
	if options.PrincipalOptIn != nil {
		return *options.PrincipalOptIn
	}
	return windowsSandboxIdentityEnabled(nil)
}

type WindowsSandboxSetupConfig struct {
	SandboxHome       string
	CommandCWD        string
	WorkspaceRoots    []string
	PermissionProfile PermissionProfile
	PrincipalOptIn    bool
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
	// PrincipalOptIn records whether the run that wrote this marker provisioned a
	// sandbox principal. Without it the two halves each sampled their own
	// environment and could disagree silently — see
	// ValidateWindowsSandboxSetupMarker.
	PrincipalOptIn bool `json:"principalOptIn"`
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
	profileJSON, err := json.Marshal(options.PermissionProfile)
	if err != nil {
		return nil, fmt.Errorf("marshal windows sandbox setup permission profile: %w", err)
	}
	args := []string{
		"--sandbox-home", sandboxHome,
		"--command-cwd", commandCWD,
		"--permission-profile", string(profileJSON),
		// Always explicit, never omitted-means-false: the elevated helper must be
		// able to tell "the caller wants no principal" from "an older caller said
		// nothing", and only the first of those is safe to run silently.
		"--sandbox-principal", windowsSandboxPrincipalOptInValue(options.principalOptIn()),
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
		case "--sandbox-principal":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxSetupConfig{}, err
			}
			switch strings.TrimSpace(value) {
			case "1":
				config.PrincipalOptIn = true
			case "0":
				config.PrincipalOptIn = false
			default:
				// Refused rather than treated as off: a value this helper cannot read
				// is a caller it does not understand, and guessing "no principal"
				// there would provision a weaker sandbox than the caller asked for
				// while reporting success.
				return WindowsSandboxSetupConfig{}, fmt.Errorf("invalid --sandbox-principal %q, want 0 or 1", value)
			}
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

// commandConfig is the command-shaped view the setup half plans against. Its Env
// carries the opt-in the caller serialized into the setup args, so every
// downstream windowsSandboxIdentityEnabled call — the gate that decides whether
// elevated setup provisions a principal at all — reads the caller's intent
// rather than sampling the elevated helper's own environment, which UAC does not
// inherit from the shell the user typed in.
func (config WindowsSandboxSetupConfig) commandConfig() WindowsSandboxCommandConfig {
	return WindowsSandboxCommandConfig{
		SandboxHome:       config.SandboxHome,
		CommandCWD:        config.CommandCWD,
		WorkspaceRoots:    cloneStrings(config.WorkspaceRoots),
		PermissionProfile: config.PermissionProfile,
		Env:               map[string]string{windowsSandboxIdentityEnv: windowsSandboxPrincipalOptInValue(config.PrincipalOptIn)},
		SandboxLevel:      WindowsSandboxLevelRestrictedToken,
	}
}

// WindowsSandboxSetupConfigFromCommand is how a command asks "was setup run for
// what I need?". It carries the command's own opt-in so marker validation can
// compare it against what setup actually provisioned.
func WindowsSandboxSetupConfigFromCommand(config WindowsSandboxCommandConfig) WindowsSandboxSetupConfig {
	return WindowsSandboxSetupConfig{
		SandboxHome:       config.SandboxHome,
		CommandCWD:        config.CommandCWD,
		WorkspaceRoots:    cloneStrings(config.WorkspaceRoots),
		PermissionProfile: config.PermissionProfile,
		PrincipalOptIn:    windowsSandboxIdentityEnabled(config.Env),
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
		PrincipalOptIn:   config.PrincipalOptIn,
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
	// The two halves of the protocol run in different processes, so they can
	// disagree about the principal opt-in. Refuse the command rather than pick a
	// winner.
	//
	// The direction that matters is the first one: the opt-in is on, setup never
	// provisioned an account, and the runtime's lookup declines with a nil error —
	// so without this the command runs on the restricted token, which does not
	// confine reads, while the operator believes a principal is isolating them.
	// A sandbox that is weaker than advertised has to be loud.
	//
	// The reverse is refused too. It is not the dangerous direction — the command
	// gets the well-worn restricted token it asked for — but setup did create a
	// local account and grant it ACEs on the workspace, and letting commands run
	// as if that had not happened leaves nothing to reconcile it. Both directions
	// clear the same way: run `zero sandbox setup` again with the environment you
	// actually want.
	if actual.PrincipalOptIn != expected.PrincipalOptIn {
		if expected.PrincipalOptIn {
			return fmt.Errorf("windows sandbox setup is out of date: %s=1 asks for a sandbox principal, but setup provisioned none — "+
				"re-run `zero sandbox setup` from an elevated (Administrator) terminal with %s=1, or unset it to use the restricted-token sandbox",
				windowsSandboxIdentityEnv, windowsSandboxIdentityEnv)
		}
		return fmt.Errorf("windows sandbox setup is out of date: setup provisioned a sandbox principal, but %s is not set for this command — "+
			"set %s=1, or re-run `zero sandbox setup` from an elevated (Administrator) terminal without it to retire the principal",
			windowsSandboxIdentityEnv, windowsSandboxIdentityEnv)
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
