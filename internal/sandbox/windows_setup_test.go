package sandbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAndParseWindowsSandboxSetupArgs(t *testing.T) {
	home := t.TempDir()
	args, err := BuildWindowsSandboxSetupArgs(WindowsSandboxSetupArgsOptions{
		SandboxHome:    home,
		CommandCWD:     `C:\workspace\src`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind: FileSystemRestricted,
				WriteRoots: []WritableRoot{
					{Root: `C:\workspace`, ProtectedMetadataNames: []string{".git"}},
				},
				DenyRead: []string{`C:\workspace\secret`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsSandboxSetupArgs: %v", err)
	}
	config, err := ParseWindowsSandboxSetupArgs(args)
	if err != nil {
		t.Fatalf("ParseWindowsSandboxSetupArgs: %v", err)
	}
	if config.SandboxHome != home || config.CommandCWD != `C:\workspace\src` || len(config.WorkspaceRoots) != 1 || config.WorkspaceRoots[0] != `C:\workspace` {
		t.Fatalf("setup config = %#v, want sandbox home, command cwd, and workspace root", config)
	}
	if config.PermissionProfile.FileSystem.Kind != FileSystemRestricted || len(config.PermissionProfile.FileSystem.DenyRead) != 1 {
		t.Fatalf("permission profile = %#v, want restricted deny-read profile", config.PermissionProfile)
	}
}

func TestWindowsSandboxSetupPathForRunner(t *testing.T) {
	got := WindowsSandboxSetupPathForRunner(filepath.Join("C:", "zero", WindowsSandboxCommandRunnerName))
	want := filepath.Join("C:", "zero", WindowsSandboxSetupName)
	if got != want {
		t.Fatalf("WindowsSandboxSetupPathForRunner = %q, want %q", got, want)
	}
	if got := WindowsSandboxSetupPathForRunner(""); got != "" {
		t.Fatalf("empty runner setup path = %q, want empty", got)
	}
}

func TestRunWindowsSandboxSetupRejectsInvalidArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := RunWindowsSandboxSetup([]string{"--command-cwd", `C:\workspace`}, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want usage error", code)
	}
	if !strings.Contains(stderr.String(), WindowsSandboxSetupName) {
		t.Fatalf("stderr = %q, want setup helper name", stderr.String())
	}
}

func TestWindowsSandboxSetupMarkerRefreshesWhenProfileChanges(t *testing.T) {
	config := WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     `C:\workspace`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
				DenyRead:   []string{`C:\workspace\secret-read`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	}
	if _, err := WriteWindowsSandboxSetupMarker(config); err != nil {
		t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
	}
	if err := ValidateWindowsSandboxSetupMarker(config); err != nil {
		t.Fatalf("ValidateWindowsSandboxSetupMarker unchanged: %v", err)
	}

	changed := config
	changed.PermissionProfile.FileSystem.DenyRead = []string{`C:\workspace\other-secret`}
	err := ValidateWindowsSandboxSetupMarker(changed)
	if err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("ValidateWindowsSandboxSetupMarker changed error = %v, want out of date", err)
	}
}

// One setup marker must validly serve BOTH network modes: an approved (allow)
// network command and an ordinary (deny) command both validate against the same
// setup. This is the fix for the "network policy changed" brick that hit every
// approved network command (curl, git push, …). The per-command mode is enforced
// at runtime by the token's SID set, not by which marker exists.
func TestWindowsSandboxSetupMarkerValidatesBothNetworkModes(t *testing.T) {
	deny := WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     `C:\workspace`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	}
	// Set up once (as a deny command would).
	if _, err := WriteWindowsSandboxSetupMarker(deny); err != nil {
		t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
	}
	if err := ValidateWindowsSandboxSetupMarker(deny); err != nil {
		t.Fatalf("deny command must validate against its own setup: %v", err)
	}
	// An APPROVED (allow) command must ALSO validate against the SAME setup —
	// previously this bricked with "network policy changed".
	allow := deny
	allow.PermissionProfile.Network = NetworkPolicy{Mode: NetworkAllow}
	if err := ValidateWindowsSandboxSetupMarker(allow); err != nil {
		t.Fatalf("approved (allow) command must validate against the deny setup, got: %v", err)
	}
}

// A pre-v4 marker on disk must be rejected as out of date so the schema bump
// forces a clean re-setup (old markers scoped the filter to write SIDs).
func TestWindowsSandboxSetupMarkerRejectsOldSchema(t *testing.T) {
	config := WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     `C:\workspace`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, WriteRoots: []WritableRoot{{Root: `C:\workspace`}}},
			Network:    NetworkPolicy{Mode: NetworkDeny},
		},
	}
	marker, err := BuildWindowsSandboxSetupMarker(config)
	if err != nil {
		t.Fatalf("BuildWindowsSandboxSetupMarker: %v", err)
	}
	marker.SchemaVersion = 3
	bytes, err := json.Marshal(marker)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(WindowsSandboxSetupMarkerPath(config.SandboxHome), bytes, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	err = ValidateWindowsSandboxSetupMarker(config)
	if err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("schema-3 marker must be out of date, got: %v", err)
	}
}

// The principal opt-in has to travel in the setup args, because the elevated
// half runs in its own process: a UAC-elevated helper does not inherit the
// environment of the shell that asked for setup. Sampling the ambient
// environment there let the two halves disagree, so the serialized value must
// win over the environment in BOTH directions.
func TestWindowsSandboxSetupPrincipalOptInSurvivesElevatedEnvironment(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, WriteRoots: []WritableRoot{{Root: `C:\workspace`}}},
		Network:    NetworkPolicy{Mode: NetworkAllow},
	}
	testCases := []struct {
		name       string
		optIn      bool
		ambientEnv string
	}{
		// The reported case: the caller's shell opted in, the elevated helper's
		// environment has nothing. Without the serialized value setup provisions no
		// principal and every later command silently falls back.
		{name: "opted in, elevated environment empty", optIn: true, ambientEnv: ""},
		// The mirror: the elevated helper happens to have a machine-wide opt-in the
		// caller did not ask for. Setup must not create an account on its own say-so.
		{name: "opted out, elevated environment opted in", optIn: false, ambientEnv: "1"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(windowsSandboxIdentityEnv, testCase.ambientEnv)
			optIn := testCase.optIn
			args, err := BuildWindowsSandboxSetupArgs(WindowsSandboxSetupArgsOptions{
				SandboxHome:       t.TempDir(),
				CommandCWD:        `C:\workspace`,
				WorkspaceRoots:    []string{`C:\workspace`},
				PermissionProfile: profile,
				PrincipalOptIn:    &optIn,
			})
			if err != nil {
				t.Fatalf("BuildWindowsSandboxSetupArgs: %v", err)
			}
			config, err := ParseWindowsSandboxSetupArgs(args)
			if err != nil {
				t.Fatalf("ParseWindowsSandboxSetupArgs: %v", err)
			}
			if config.PrincipalOptIn != testCase.optIn {
				t.Fatalf("parsed PrincipalOptIn = %v, want %v", config.PrincipalOptIn, testCase.optIn)
			}
			// This is the value the elevated setup gate actually reads before it
			// decides to provision an account.
			if got := windowsSandboxIdentityEnabled(config.commandConfig().Env); got != testCase.optIn {
				t.Fatalf("elevated setup opt-in = %v, want %v (ambient %s=%q must not decide)",
					got, testCase.optIn, windowsSandboxIdentityEnv, testCase.ambientEnv)
			}
		})
	}
}

// A setup helper that cannot read the opt-in must refuse rather than default to
// "no principal": provisioning less than the caller asked for and reporting
// success is the silent downgrade this protocol exists to prevent.
func TestParseWindowsSandboxSetupArgsRejectsUnreadablePrincipalOptIn(t *testing.T) {
	optIn := true
	args, err := BuildWindowsSandboxSetupArgs(WindowsSandboxSetupArgsOptions{
		SandboxHome:       t.TempDir(),
		CommandCWD:        `C:\workspace`,
		WorkspaceRoots:    []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{FileSystem: FileSystemPolicy{Kind: FileSystemRestricted}, Network: NetworkPolicy{Mode: NetworkDeny}},
		PrincipalOptIn:    &optIn,
	})
	if err != nil {
		t.Fatalf("BuildWindowsSandboxSetupArgs: %v", err)
	}
	for index, arg := range args {
		if arg == "--sandbox-principal" {
			args[index+1] = "yes"
		}
	}
	if _, err := ParseWindowsSandboxSetupArgs(args); err == nil || !strings.Contains(err.Error(), "--sandbox-principal") {
		t.Fatalf("ParseWindowsSandboxSetupArgs error = %v, want rejection of the unreadable opt-in", err)
	}
}

// Setup and the commands that follow it run in separate processes, so they can
// disagree about the opt-in. The marker records what setup provisioned and the
// command refuses on a mismatch — most of all when the command opted in and
// setup did not, because the runtime's principal lookup declines with a nil
// error and the command would otherwise run on the read-unconfined
// restricted-token backend while the operator believes a principal is isolating
// it.
func TestWindowsSandboxSetupMarkerRejectsPrincipalOptInMismatch(t *testing.T) {
	// Neutral ambient environment: the disagreement under test is between the two
	// recorded intents, not between either of them and this process.
	t.Setenv(windowsSandboxIdentityEnv, "")
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, WriteRoots: []WritableRoot{{Root: `C:\workspace`}}},
		Network:    NetworkPolicy{Mode: NetworkAllow},
	}
	command := func(home string, env map[string]string) WindowsSandboxCommandConfig {
		return WindowsSandboxCommandConfig{
			SandboxHome:       home,
			CommandCWD:        `C:\workspace`,
			WorkspaceRoots:    []string{`C:\workspace`},
			PermissionProfile: profile,
			Env:               env,
			SandboxLevel:      WindowsSandboxLevelRestrictedToken,
			Command:           []string{"cmd.exe", "/c", "echo"},
		}
	}
	testCases := []struct {
		name       string
		setupOptIn bool
		commandEnv map[string]string
		wantError  string
	}{
		{
			name:       "command opts in, setup provisioned no principal",
			setupOptIn: false,
			commandEnv: map[string]string{windowsSandboxIdentityEnv: "1"},
			wantError:  "asks for a sandbox principal, but setup provisioned none",
		},
		{
			name:       "setup provisioned a principal, command opts out",
			setupOptIn: true,
			commandEnv: map[string]string{windowsSandboxIdentityEnv: "0"},
			wantError:  "setup provisioned a sandbox principal",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			setupConfig := WindowsSandboxSetupConfig{
				SandboxHome:       home,
				CommandCWD:        `C:\workspace`,
				WorkspaceRoots:    []string{`C:\workspace`},
				PermissionProfile: profile,
				PrincipalOptIn:    testCase.setupOptIn,
			}
			marker, err := WriteWindowsSandboxSetupMarker(setupConfig)
			if err != nil {
				t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
			}
			// Assert the setup half recorded what it was told before trusting what
			// the command half makes of it.
			if marker.PrincipalOptIn != testCase.setupOptIn {
				t.Fatalf("marker PrincipalOptIn = %v, want %v", marker.PrincipalOptIn, testCase.setupOptIn)
			}
			// An agreeing command still validates, so the refusal below is about the
			// disagreement and not about the marker being unusable.
			agreeing := command(home, map[string]string{windowsSandboxIdentityEnv: windowsSandboxPrincipalOptInValue(testCase.setupOptIn)})
			if err := ValidateWindowsSandboxSetupMarker(WindowsSandboxSetupConfigFromCommand(agreeing)); err != nil {
				t.Fatalf("agreeing command must validate against its own setup: %v", err)
			}

			err = ValidateWindowsSandboxSetupMarker(WindowsSandboxSetupConfigFromCommand(command(home, testCase.commandEnv)))
			if err == nil {
				t.Fatalf("disagreeing command validated the marker, want refusal")
			}
			if !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("validate error = %v, want it to contain %q", err, testCase.wantError)
			}
			if !strings.Contains(err.Error(), "zero sandbox setup") {
				t.Fatalf("validate error = %v, want the remedy to name `zero sandbox setup`", err)
			}
		})
	}
}

func TestWindowsSandboxSetupConfigFromCommandPreservesProfileInputs(t *testing.T) {
	command := WindowsSandboxCommandConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     `C:\workspace\src`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
				DenyRead:   []string{`C:\workspace\secret`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
		Env:     map[string]string{"ZERO_SANDBOXED": "1"},
		Command: []string{"cmd.exe", "/c", "dir"},
	}
	setup := WindowsSandboxSetupConfigFromCommand(command)
	if setup.SandboxHome != command.SandboxHome || setup.CommandCWD != command.CommandCWD || len(setup.WorkspaceRoots) != 1 || setup.WorkspaceRoots[0] != `C:\workspace` {
		t.Fatalf("setup config = %#v, want command roots", setup)
	}
	if setup.PermissionProfile.FileSystem.Kind != FileSystemRestricted || len(setup.PermissionProfile.FileSystem.DenyRead) != 1 {
		t.Fatalf("setup profile = %#v, want command permission profile", setup.PermissionProfile)
	}
}

// Serializing the opt-in makes it a field every caller of
// BuildWindowsSandboxSetupArgs could get wrong, so the field is a tri-state and
// its unset meaning is load-bearing: "consult the environment", never "opted
// out". The command half still resolves the opt-in from the process environment
// when its own Env carries no explicit entry, so if an unset setup caller
// asserted false instead, the two halves would disagree under a machine-wide
// opt-in and marker validation would refuse every command — safe, but it bricks
// the caller, and it re-creates the very disagreement this flag removes. That is
// exactly what the existing smoke callers
// (runner_windows_integration_test.go:43 and :52) do: they never set the field.
//
// This test runs on every GOOS and pins the unset default in both ambient
// states, so getting it backwards is a test failure here rather than a surprise
// on a real elevated machine.
func TestWindowsSandboxSetupArgsUnsetPrincipalOptInConsultsEnvironment(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, WriteRoots: []WritableRoot{{Root: `C:\workspace`}}},
		Network:    NetworkPolicy{Mode: NetworkAllow},
	}
	// The command half as an ambient caller declares it: no explicit entry in Env,
	// so it resolves the opt-in from the environment.
	command := func(home string) WindowsSandboxCommandConfig {
		return WindowsSandboxCommandConfig{
			SandboxHome:       home,
			CommandCWD:        `C:\workspace`,
			WorkspaceRoots:    []string{`C:\workspace`},
			PermissionProfile: profile,
			SandboxLevel:      WindowsSandboxLevelRestrictedToken,
			Command:           []string{"cmd.exe", "/c", "echo"},
		}
	}
	// setupMarkerFor runs the full caller path — build args, cross the (simulated)
	// UAC boundary by re-parsing them, write the marker — so what is asserted is
	// what an elevated helper would actually have provisioned.
	setupMarkerFor := func(t *testing.T, home string, optIn *bool) WindowsSandboxSetupMarker {
		t.Helper()
		args, err := BuildWindowsSandboxSetupArgs(WindowsSandboxSetupArgsOptions{
			SandboxHome:       home,
			CommandCWD:        `C:\workspace`,
			WorkspaceRoots:    []string{`C:\workspace`},
			PermissionProfile: profile,
			PrincipalOptIn:    optIn,
		})
		if err != nil {
			t.Fatalf("BuildWindowsSandboxSetupArgs: %v", err)
		}
		config, err := ParseWindowsSandboxSetupArgs(args)
		if err != nil {
			t.Fatalf("ParseWindowsSandboxSetupArgs: %v", err)
		}
		marker, err := WriteWindowsSandboxSetupMarker(config)
		if err != nil {
			t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
		}
		return marker
	}

	for _, ambient := range []string{"1", ""} {
		name := "machine-wide opt-in"
		if ambient == "" {
			name = "no opt-in"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(windowsSandboxIdentityEnv, ambient)
			want := ambient == "1"

			// An unset caller must provision what the environment says. Assert the
			// setup half recorded that before trusting the agreement below: a marker
			// that recorded the wrong thing could still "agree" if the command half
			// were broken in the same direction.
			home := t.TempDir()
			marker := setupMarkerFor(t, home, nil)
			if marker.PrincipalOptIn != want {
				t.Fatalf("unset caller recorded PrincipalOptIn = %v, want %v (ambient %s=%q decides)",
					marker.PrincipalOptIn, want, windowsSandboxIdentityEnv, ambient)
			}
			if err := ValidateWindowsSandboxSetupMarker(WindowsSandboxSetupConfigFromCommand(command(home))); err != nil {
				t.Fatalf("an unset caller must agree with the ambient command half: %v", err)
			}

			// And an explicit value still overrides the environment in both
			// directions, or the tri-state would have no third state.
			override := !want
			overrideHome := t.TempDir()
			overrideMarker := setupMarkerFor(t, overrideHome, &override)
			if overrideMarker.PrincipalOptIn != override {
				t.Fatalf("explicit caller recorded PrincipalOptIn = %v, want %v", overrideMarker.PrincipalOptIn, override)
			}
			err := ValidateWindowsSandboxSetupMarker(WindowsSandboxSetupConfigFromCommand(command(overrideHome)))
			if err == nil {
				t.Fatalf("an explicit opt-in of %v validated against an ambient command half of %v, want refusal", override, want)
			}
			if !strings.Contains(err.Error(), windowsSandboxIdentityEnv) {
				t.Fatalf("validate error = %v, want it to name %s", err, windowsSandboxIdentityEnv)
			}
		})
	}
}

func TestWindowsACLPlanHashIsStableAcrossEntryOrder(t *testing.T) {
	left, err := WindowsACLPlanHash(WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLDenyRead, Path: `C:\workspace\secret`, Capability: "S-1-5-21-3", Materialize: true},
		{Action: WindowsACLAllowWrite, Path: `C:\workspace`, Capability: "S-1-5-21-1"},
		{Action: WindowsACLDenyWrite, Path: `C:\workspace\.git`, Capability: "S-1-5-21-1"},
	}})
	if err != nil {
		t.Fatalf("WindowsACLPlanHash left: %v", err)
	}
	right, err := WindowsACLPlanHash(WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLAllowWrite, Path: `c:/workspace`, Capability: "s-1-5-21-1"},
		{Action: WindowsACLDenyWrite, Path: `c:/workspace/.git`, Capability: "S-1-5-21-1"},
		{Action: WindowsACLDenyRead, Path: `c:/workspace/secret`, Capability: "S-1-5-21-3", Materialize: true},
	}})
	if err != nil {
		t.Fatalf("WindowsACLPlanHash right: %v", err)
	}
	if left != right {
		t.Fatalf("ACL plan hashes differ: %q vs %q", left, right)
	}
}
