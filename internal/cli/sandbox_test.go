package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sandbox"
)

func TestRunSandboxGrantsAllowListDenyRevokeAndClear(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "grants", "allow", "write_file", "--auto", "medium", "--reason", "workspace edits", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("allow exit = %d, stderr %q", exitCode, stderr.String())
	}
	var allowPayload struct {
		Grant sandbox.Grant `json:"grant"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &allowPayload); err != nil {
		t.Fatalf("decode allow JSON: %v\n%s", err, stdout.String())
	}
	if allowPayload.Grant.ToolName != "write_file" || allowPayload.Grant.Decision != sandbox.GrantAllow || allowPayload.Grant.MaxAutonomy != sandbox.AutonomyMedium {
		t.Fatalf("unexpected allow payload: %#v", allowPayload)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"sandbox", "grants", "deny", "bash", "--auto=high", "--reason=network blocked"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("deny exit = %d, stderr %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "bash") || !strings.Contains(stdout.String(), "deny") {
		t.Fatalf("unexpected deny text: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"sandbox", "grants", "list", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("list exit = %d, stderr %q", exitCode, stderr.String())
	}
	var listPayload struct {
		Grants []sandbox.Grant `json:"grants"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list JSON: %v\n%s", err, stdout.String())
	}
	if len(listPayload.Grants) != 2 || listPayload.Grants[0].ToolName != "bash" || listPayload.Grants[1].ToolName != "write_file" {
		t.Fatalf("unexpected sorted grants: %#v", listPayload.Grants)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"sandbox", "grants", "revoke", "bash", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("revoke exit = %d, stderr %q", exitCode, stderr.String())
	}
	var revokePayload struct {
		Revoked int `json:"revoked"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &revokePayload); err != nil {
		t.Fatalf("decode revoke JSON: %v\n%s", err, stdout.String())
	}
	if revokePayload.Revoked != 1 {
		t.Fatalf("revoked = %d, want 1", revokePayload.Revoked)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"sandbox", "grants", "clear", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitUsage {
		t.Fatalf("clear without confirm exit = %d, want usage", exitCode)
	}
	if !strings.Contains(stderr.String(), "--confirm") {
		t.Fatalf("expected confirm error, got %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"sandbox", "grants", "clear", "--confirm", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("clear exit = %d, stderr %q", exitCode, stderr.String())
	}
	var clearPayload struct {
		Cleared int `json:"cleared"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &clearPayload); err != nil {
		t.Fatalf("decode clear JSON: %v\n%s", err, stdout.String())
	}
	if clearPayload.Cleared != 1 {
		t.Fatalf("cleared = %d, want 1", clearPayload.Cleared)
	}
}

func TestRunSandboxGrantsCreateAndRevokeByPath(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil }}
	path := filepath.Join(t.TempDir(), "secret.txt")

	var stdout, stderr bytes.Buffer
	run := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return runWithDeps(args, &stdout, &stderr, deps)
	}

	// An exact-path grant and a tool-wide grant for the same tool.
	if exit := run("sandbox", "grants", "allow", "write_file", "--path", path); exit != exitSuccess {
		t.Fatalf("allow --path exit = %d, stderr %q", exit, stderr.String())
	}
	if exit := run("sandbox", "grants", "allow", "write_file"); exit != exitSuccess {
		t.Fatalf("allow tool-wide exit = %d, stderr %q", exit, stderr.String())
	}

	// Revoking by path removes only the path-scoped grant.
	if exit := run("sandbox", "grants", "revoke", "write_file", "--path", path, "--json"); exit != exitSuccess {
		t.Fatalf("revoke --path exit = %d, stderr %q", exit, stderr.String())
	}
	var payload struct {
		Revoked int `json:"revoked"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode revoke JSON: %v\n%s", err, stdout.String())
	}
	if payload.Revoked != 1 {
		t.Fatalf("revoked = %d, want 1", payload.Revoked)
	}

	grants, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(grants) != 1 || grants[0].ScopeKind != sandbox.ScopeToolWide {
		t.Fatalf("expected only the tool-wide grant to remain, got %#v", grants)
	}
}

func TestRunSandboxGrantsRejectsEmptyPath(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil }}

	var stdout, stderr bytes.Buffer
	run := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return runWithDeps(args, &stdout, &stderr, deps)
	}

	// Seed a tool-wide grant first so a buggy "revoke all for tool" or "allow
	// tool-wide" from a rejected call would actually change the store and be caught
	// (a revoke-all on an empty store is a silent no-op).
	if _, err := store.Grant(sandbox.GrantInput{ToolName: "write_file", Decision: sandbox.GrantAllow, MaxAutonomy: sandbox.AutonomyHigh}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	// An explicit but empty --path must fail closed rather than silently widening
	// an allow to tool-wide or a revoke to all-grants-for-tool. Check immutability
	// after EACH rejected call so a mutation in one isn't masked by a later call.
	for _, args := range [][]string{
		{"sandbox", "grants", "allow", "write_file", "--path", ""},
		{"sandbox", "grants", "allow", "write_file", "--path="},
		{"sandbox", "grants", "revoke", "write_file", "--path", ""},
		{"sandbox", "grants", "revoke", "write_file", "--path="},
	} {
		before, err := store.List()
		if err != nil {
			t.Fatalf("%v: List before: %v", args, err)
		}
		if exit := run(args...); exit == exitSuccess {
			t.Fatalf("%v: expected a usage error for an empty --path, got success", args)
		}
		// Either rejection path is acceptable (the `--path=` form hits the
		// non-empty check; the `--path ""` form is rejected as a missing value) —
		// what matters is that an empty --path never silently widens scope.
		if !strings.Contains(stderr.String(), "path") {
			t.Fatalf("%v: stderr should explain the empty --path, got %q", args, stderr.String())
		}
		after, err := store.List()
		if err != nil {
			t.Fatalf("%v: List after: %v", args, err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("%v: rejected call mutated grants; before=%#v after=%#v", args, before, after)
		}
	}

	// Only the seeded grant remains — nothing added or removed by the rejected calls.
	grants, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(grants) != 1 || grants[0].ScopeKind != sandbox.ScopeToolWide {
		t.Fatalf("expected only the seeded tool-wide grant to remain, got %#v", grants)
	}
}

func TestRunSandboxPolicyInspectTextAndJSON(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{
		getwd:           func() (string, error) { return t.TempDir(), nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, nil
		},
		selectSandboxBackend: func(options sandbox.BackendOptions) sandbox.Backend {
			return sandbox.Backend{
				Name:     sandbox.BackendPolicyOnly,
				Platform: "windows",
				Fallback: true,
				Message:  "policy-only fallback: Windows sandbox command runner is not available",
			}
		},
	}

	for _, args := range [][]string{
		{"sandbox", "policy"},
		{"sandbox", "policy", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runWithDeps(args, &stdout, &stderr, deps)
			if exitCode != exitSuccess {
				t.Fatalf("policy exit = %d, stderr %q", exitCode, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
			if strings.Contains(strings.Join(args, " "), "--json") {
				var payload struct {
					Policy  sandbox.Policy  `json:"policy"`
					Backend sandbox.Backend `json:"backend"`
					Plan    struct {
						TargetBackend    sandbox.BackendName         `json:"targetBackend"`
						CommandWrapped   bool                        `json:"commandWrapped"`
						EnforcementLevel sandbox.EnforcementLevel    `json:"enforcementLevel"`
						DowngradeReason  string                      `json:"downgradeReason"`
						SupportLevel     string                      `json:"supportLevel"`
						Capabilities     []sandbox.BackendCapability `json:"capabilities"`
						Restrictions     []string                    `json:"restrictions"`
						Warnings         []string                    `json:"warnings"`
					} `json:"plan"`
					Grants string `json:"grantsPath"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
					t.Fatalf("decode policy JSON: %v\n%s", err, stdout.String())
				}
				if payload.Policy.Mode != sandbox.ModeEnforce || payload.Backend.Name != sandbox.BackendPolicyOnly || payload.Grants == "" {
					t.Fatalf("unexpected policy JSON: %#v", payload)
				}
				if payload.Backend.Platform != "windows" || !payload.Backend.Fallback || payload.Backend.NativeIsolation || payload.Backend.CommandWrapping {
					t.Fatalf("unexpected backend capability JSON: %#v", payload.Backend)
				}
				if payload.Plan.SupportLevel != string(sandbox.BackendSupportPolicyOnly) {
					t.Fatalf("support level = %q, want policy-only", payload.Plan.SupportLevel)
				}
				if payload.Plan.TargetBackend != sandbox.BackendWindowsRestrictedToken || payload.Plan.CommandWrapped || payload.Plan.EnforcementLevel != sandbox.EnforcementDegraded || payload.Plan.DowngradeReason == "" {
					t.Fatalf("unexpected manager baseline fields: %#v", payload.Plan)
				}
				if sandboxPolicyCapabilityStatus(payload.Plan.Capabilities, "native_process_isolation") != sandbox.CapabilityUnavailable {
					t.Fatalf("expected native isolation unavailable, got %#v", payload.Plan.Capabilities)
				}
				if !sandboxPolicyRestrictionContains(payload.Plan.Restrictions, "native process isolation unavailable on windows") {
					t.Fatalf("expected JSON plan to document Windows fallback, got %#v", payload.Plan.Restrictions)
				}
				if !sandboxPolicyRestrictionContains(payload.Plan.Warnings, "Windows sandbox command runner is not available") {
					t.Fatalf("expected JSON warnings to document Windows fallback, got %#v", payload.Plan.Warnings)
				}
			} else {
				output := stdout.String()
				for _, want := range []string{
					"Zero sandbox policy",
					"backend: policy-only",
					"target_backend: windows-restricted-token",
					"support_level: policy-only",
					"command_wrapped: false",
					"enforcement_level: degraded",
					"downgrade_reason: policy-only fallback: Windows sandbox command runner is not available",
					"backend_fallback: true",
					"backend_command_wrapping: false",
					"backend_native_isolation: false",
					"backend_platform: windows",
					"Windows sandbox command runner is not available",
				} {
					if !strings.Contains(output, want) {
						t.Fatalf("expected policy text to contain %q, got %q", want, output)
					}
				}
			}
		})
	}
}

func TestRunSandboxSetupRunsWindowsHelper(t *testing.T) {
	workspace := t.TempDir()
	runnerDir := t.TempDir()
	runnerPath := filepath.Join(runnerDir, sandbox.WindowsSandboxCommandRunnerName)
	var gotPath string
	var gotArgs []string
	deps := appDeps{
		getwd: func() (string, error) { return workspace, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{
				Sandbox: config.SandboxConfig{Network: "allow"},
			}, nil
		},
		selectSandboxBackend: func(options sandbox.BackendOptions) sandbox.Backend {
			return sandbox.Backend{
				Name:            sandbox.BackendWindowsRestrictedToken,
				Available:       true,
				Platform:        "windows",
				Executable:      runnerPath,
				CommandWrapping: true,
				NativeIsolation: true,
			}
		},
		runSandboxSetupHelper: func(path string, args []string, stdout io.Writer, stderr io.Writer) error {
			gotPath = path
			gotArgs = append([]string{}, args...)
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "setup", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("setup exit = %d, stderr %q", exitCode, stderr.String())
	}
	wantPath := filepath.Join(runnerDir, sandbox.WindowsSandboxSetupName)
	if gotPath != wantPath {
		t.Fatalf("setup helper path = %q, want %q", gotPath, wantPath)
	}
	config, err := sandbox.ParseWindowsSandboxSetupArgs(gotArgs)
	if err != nil {
		t.Fatalf("ParseWindowsSandboxSetupArgs: %v", err)
	}
	if config.CommandCWD != workspace || len(config.WorkspaceRoots) != 1 || config.WorkspaceRoots[0] != workspace {
		t.Fatalf("setup args config = %#v, want workspace cwd/root", config)
	}
	if config.PermissionProfile.Network.Mode != sandbox.NetworkAllow {
		t.Fatalf("setup network profile = %#v, want allow", config.PermissionProfile.Network)
	}
	var payload sandboxSetupResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode setup JSON: %v\n%s", err, stdout.String())
	}
	if !payload.Ran || payload.Helper != wantPath || payload.Backend != sandbox.BackendWindowsRestrictedToken {
		t.Fatalf("setup payload = %#v, want ran Windows helper", payload)
	}
}

func TestRunSandboxSetupNoopsForNonWindowsBackend(t *testing.T) {
	deps := appDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, nil
		},
		selectSandboxBackend: func(options sandbox.BackendOptions) sandbox.Backend {
			return sandbox.Backend{Name: sandbox.BackendLinuxBwrap, Platform: "linux", Available: true}
		},
		runSandboxSetupHelper: func(string, []string, io.Writer, io.Writer) error {
			t.Fatal("setup helper should not run for non-Windows backend")
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "setup"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("setup exit = %d, stderr %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No native sandbox setup is required for linux") {
		t.Fatalf("setup output = %q, want non-Windows no-op message", stdout.String())
	}
}

func TestTUISandboxSetupCommandGatedToWindowsNativeBackend(t *testing.T) {
	if got := tuiSandboxSetupCommand(sandbox.Backend{Name: sandbox.BackendLinuxBwrap, Platform: "linux", Available: true}, appDeps{}); got != nil {
		t.Fatal("linux backend should not enable the TUI sandbox setup command")
	}
	if got := tuiSandboxSetupCommand(sandbox.Backend{Name: sandbox.BackendPolicyOnly, Platform: "windows", Available: false}, appDeps{}); got != nil {
		t.Fatal("policy-only Windows backend should not enable the TUI sandbox setup command")
	}
	got := tuiSandboxSetupCommand(sandbox.Backend{
		Name:            sandbox.BackendWindowsRestrictedToken,
		Platform:        "windows",
		Available:       true,
		Executable:      filepath.Join(t.TempDir(), sandbox.WindowsSandboxCommandRunnerName),
		CommandWrapping: true,
		NativeIsolation: true,
	}, appDeps{})
	if got == nil {
		t.Fatal("native Windows backend should enable the TUI sandbox setup command")
	}
}

func TestRunSandboxPolicyJSONGoldenIncludesManagerBaselineFields(t *testing.T) {
	store := newSandboxTestStore(t)
	workspace := t.TempDir()
	deps := appDeps{
		getwd:           func() (string, error) { return workspace, nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, nil
		},
		selectSandboxBackend: func(options sandbox.BackendOptions) sandbox.Backend {
			return sandbox.Backend{
				Name:     sandbox.BackendPolicyOnly,
				Platform: "windows",
				Fallback: true,
				Message:  "policy-only fallback: Windows sandbox command runner is not available",
			}
		},
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "policy", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("policy exit = %d, stderr %q", exitCode, stderr.String())
	}

	got := stdout.String()
	got = replacePathToken(got, workspace, "$WORKSPACE")
	got = replacePathToken(got, store.FilePath(), "$GRANTS")
	wantBytes, err := os.ReadFile(filepath.Join("testdata", "sandbox_policy_windows_policy_only.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !jsonValuesEqual(t, wantBytes, []byte(got)) {
		t.Fatalf("policy JSON golden mismatch\nwant:\n%s\ngot:\n%s", string(wantBytes), got)
	}
}

func jsonValuesEqual(t *testing.T, wantBytes []byte, gotBytes []byte) bool {
	t.Helper()
	var wantValue any
	if err := json.Unmarshal(wantBytes, &wantValue); err != nil {
		t.Fatalf("decode wanted JSON: %v", err)
	}
	var gotValue any
	if err := json.Unmarshal(gotBytes, &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v\n%s", err, string(gotBytes))
	}
	return reflect.DeepEqual(wantValue, gotValue)
}

func replacePathToken(value string, path string, token string) string {
	replace := func(value string, path string) string {
		value = strings.ReplaceAll(value, path, token)
		if encoded, err := json.Marshal(path); err == nil && len(encoded) >= 2 {
			value = strings.ReplaceAll(value, string(encoded[1:len(encoded)-1]), token)
		}
		return value
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		value = replace(value, resolved)
	}
	return replace(value, path)
}

func TestRunSandboxPolicyEffectiveTextAndJSON(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{
		getwd:           func() (string, error) { return t.TempDir(), nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, nil
		},
		selectSandboxBackend: func(options sandbox.BackendOptions) sandbox.Backend {
			return sandbox.Backend{
				Name:     sandbox.BackendPolicyOnly,
				Platform: "darwin",
				Fallback: true,
				Message:  "policy-only fallback",
			}
		},
	}

	t.Run("text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exitCode := runWithDeps([]string{"sandbox", "policy", "--effective"}, &stdout, &stderr, deps)
		if exitCode != exitSuccess {
			t.Fatalf("effective exit = %d, stderr %q", exitCode, stderr.String())
		}
		output := stdout.String()
		for _, want := range []string{
			"Zero effective sandbox policy",
			"mode: enforce",
			"network: deny",
			"enforce_workspace: true",
			"deny_destructive_shell: true",
			"interactive_command_guard: enabled",
			"support_level: policy-only",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("effective text missing %q, got %q", want, output)
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exitCode := runWithDeps([]string{"sandbox", "policy", "--effective", "--json"}, &stdout, &stderr, deps)
		if exitCode != exitSuccess {
			t.Fatalf("effective json exit = %d, stderr %q", exitCode, stderr.String())
		}
		var payload struct {
			Policy struct {
				Mode                 string `json:"mode"`
				Network              string `json:"network"`
				EnforceWorkspace     bool   `json:"enforceWorkspace"`
				DenyDestructiveShell bool   `json:"denyDestructiveShell"`
			} `json:"policy"`
			Backend struct {
				Name string `json:"name"`
			} `json:"backend"`
			Plan struct {
				SupportLevel string `json:"supportLevel"`
			} `json:"plan"`
			Guards struct {
				InteractiveCommand bool `json:"interactiveCommand"`
				DestructiveShell   bool `json:"destructiveShell"`
				Network            bool `json:"network"`
				Workspace          bool `json:"workspace"`
			} `json:"guards"`
			GrantsPath string `json:"grantsPath"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
			t.Fatalf("decode effective JSON: %v\n%s", err, stdout.String())
		}
		if payload.Policy.Mode != "enforce" || payload.Policy.Network != "deny" {
			t.Fatalf("unexpected effective policy: %#v", payload.Policy)
		}
		if !payload.Policy.EnforceWorkspace || !payload.Policy.DenyDestructiveShell {
			t.Fatalf("expected workspace + destructive guards enabled: %#v", payload.Policy)
		}
		if !payload.Guards.InteractiveCommand || !payload.Guards.DestructiveShell {
			t.Fatalf("expected guards reported: %#v", payload.Guards)
		}
		if payload.Plan.SupportLevel != string(sandbox.BackendSupportPolicyOnly) || payload.GrantsPath == "" {
			t.Fatalf("unexpected effective plan/grants: %#v %q", payload.Plan, payload.GrantsPath)
		}
	})
}

func TestEffectiveSandboxPolicyListsWriteRoots(t *testing.T) {
	output := formatEffectiveSandboxPolicy("/ws", sandbox.DefaultPolicy(), sandbox.Backend{}, sandbox.BackendPlan{}, resolveSandboxGuards(sandbox.DefaultPolicy()), "/grants", []string{"/ws", "/extra"}, nil)
	if !strings.Contains(output, "write_roots: /ws, /extra") {
		t.Fatalf("expected write_roots line, got:\n%s", output)
	}
	if !strings.Contains(output, "enforce_workspace: true\nwrite_roots: /ws, /extra") {
		t.Fatalf("write_roots should directly follow enforce_workspace, got:\n%s", output)
	}
	if strings.Contains(output, "write_roots_error") {
		t.Fatalf("unexpected write_roots_error line without an error:\n%s", output)
	}
}

func TestEffectiveSandboxPolicyShowsWriteRootsError(t *testing.T) {
	scopeErr := errors.New(`write root "/gone": write root must exist`)
	output := formatEffectiveSandboxPolicy("/ws", sandbox.DefaultPolicy(), sandbox.Backend{}, sandbox.BackendPlan{}, resolveSandboxGuards(sandbox.DefaultPolicy()), "/grants", []string{"/ws"}, scopeErr)
	if !strings.Contains(output, "write_roots: /ws") {
		t.Fatalf("expected fallback write_roots line, got:\n%s", output)
	}
	if !strings.Contains(output, `write_roots_error: write root "/gone": write root must exist`) {
		t.Fatalf("expected write_roots_error line, got:\n%s", output)
	}
}

func TestRunSandboxPolicyEffectiveListsConfiguredWriteRoots(t *testing.T) {
	store := newSandboxTestStore(t)
	extra := t.TempDir()
	resolvedExtra, err := filepath.EvalSymlinks(extra)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) returned error: %v", extra, err)
	}
	deps := appDeps{
		getwd:           func() (string, error) { return t.TempDir(), nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{Sandbox: config.SandboxConfig{AdditionalWriteRoots: []string{extra}}}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"sandbox", "policy", "--effective"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("effective exit = %d, stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "write_roots: ") {
		t.Fatalf("effective text missing write_roots line:\n%s", output)
	}
	if !strings.Contains(output, resolvedExtra) {
		t.Fatalf("write_roots should include the configured extra root %q:\n%s", resolvedExtra, output)
	}
	if strings.Contains(output, "write_roots_error") {
		t.Fatalf("unexpected write_roots_error for valid roots:\n%s", output)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"sandbox", "policy", "--effective", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("effective json exit = %d, stderr %q", code, stderr.String())
	}
	var payload struct {
		WriteRoots []string `json:"writeRoots"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode effective JSON: %v\n%s", err, stdout.String())
	}
	if len(payload.WriteRoots) != 2 {
		t.Fatalf("writeRoots = %#v, want workspace root + extra root", payload.WriteRoots)
	}
	if payload.WriteRoots[1] != resolvedExtra {
		t.Fatalf("writeRoots[1] = %q, want %q", payload.WriteRoots[1], resolvedExtra)
	}
	if strings.Contains(stdout.String(), "writeRootsError") {
		t.Fatalf("unexpected writeRootsError key for valid roots:\n%s", stdout.String())
	}
}

func TestRunSandboxPolicyEffectiveWriteRootsFailSoft(t *testing.T) {
	store := newSandboxTestStore(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	deps := appDeps{
		getwd:           func() (string, error) { return t.TempDir(), nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{Sandbox: config.SandboxConfig{AdditionalWriteRoots: []string{missing}}}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"sandbox", "policy", "--effective"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("effective exit = %d, want success (stale config must fail soft), stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "write_roots_error: ") {
		t.Fatalf("expected visible write_roots_error line for stale config entry:\n%s", output)
	}
	if !strings.Contains(output, "write_roots: ") {
		t.Fatalf("expected workspace-only write_roots fallback line:\n%s", output)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"sandbox", "policy", "--effective", "--json"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("effective json exit = %d, want success (stale config must fail soft), stderr %q", code, stderr.String())
	}
	var payload struct {
		WriteRoots      []string `json:"writeRoots"`
		WriteRootsError string   `json:"writeRootsError"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode effective JSON: %v\n%s", err, stdout.String())
	}
	if payload.WriteRootsError == "" {
		t.Fatalf("expected writeRootsError in JSON for stale config entry:\n%s", stdout.String())
	}
	if !strings.Contains(payload.WriteRootsError, missing) {
		t.Fatalf("writeRootsError = %q, want it to name the stale root %q", payload.WriteRootsError, missing)
	}
	if len(payload.WriteRoots) != 1 {
		t.Fatalf("writeRoots = %#v, want workspace-only fallback", payload.WriteRoots)
	}
}

func TestRunSandboxPolicyEffectiveHelpListed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "policy", "--help"}, &stdout, &stderr, appDeps{})
	if exitCode != exitSuccess {
		t.Fatalf("help exit = %d, stderr %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "--effective") {
		t.Fatalf("policy help should document --effective, got %q", stdout.String())
	}
}

func TestRunSandboxHelpDoesNotOpenStore(t *testing.T) {
	deps := appDeps{newSandboxStore: func() (*sandbox.GrantStore, error) {
		t.Fatal("newSandboxStore should not be called for help")
		return nil, nil
	}}
	for _, args := range [][]string{
		{"sandbox", "--help"},
		{"sandbox", "grants", "--help"},
		{"sandbox", "grants", "allow", "--help"},
		{"sandbox", "policy", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runWithDeps(args, &stdout, &stderr, deps)
			if exitCode != exitSuccess {
				t.Fatalf("help exit = %d, stderr %q", exitCode, stderr.String())
			}
			if stdout.Len() == 0 {
				t.Fatalf("expected help output")
			}
		})
	}
}

func TestRunSandboxPolicyAppliesConfiguredCeiling(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{Sandbox: config.SandboxConfig{MaxAutonomy: "medium"}}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "policy", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("policy exit = %d, stderr %q", exitCode, stderr.String())
	}
	var payload struct {
		Policy sandbox.Policy `json:"policy"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode policy JSON: %v\n%s", err, stdout.String())
	}
	if payload.Policy.MaxAutonomy != sandbox.AutonomyMedium {
		t.Fatalf("policy.MaxAutonomy = %q, want medium", payload.Policy.MaxAutonomy)
	}
}

func TestRunSandboxPolicyTextShowsMaxAutonomy(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{Sandbox: config.SandboxConfig{MaxAutonomy: "medium"}}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"sandbox", "policy"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("policy exit = %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "max_autonomy: medium") {
		t.Fatalf("policy text missing max_autonomy line:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"sandbox", "policy", "--effective"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("effective policy exit = %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "max_autonomy: medium") {
		t.Fatalf("effective policy text missing max_autonomy line:\n%s", stdout.String())
	}
}

func TestRunSandboxPolicySurfacesResolveConfigError(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{
		getwd:           func() (string, error) { return t.TempDir(), nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, fmt.Errorf("invalid sandbox.maxAutonomy %q", "moderate")
		},
	}

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "policy"}, &stdout, &stderr, deps)
	if exitCode != exitProvider {
		t.Fatalf("policy exit = %d, want provider exit %d (resolve error surfaced, not silent DefaultPolicy fallback)", exitCode, exitProvider)
	}
	if !strings.Contains(stderr.String(), "invalid sandbox.maxAutonomy") {
		t.Fatalf("expected surfaced resolve error in stderr, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout on resolve error, got %q", stdout.String())
	}
}

func TestApplyConfiguredAutonomyCeiling(t *testing.T) {
	cases := []struct {
		name        string
		maxAutonomy string
		want        sandbox.Autonomy
	}{
		// Empty is a no-op: the default High ceiling is preserved.
		{name: "empty keeps default high", maxAutonomy: "", want: sandbox.AutonomyHigh},
		{name: "whitespace keeps default high", maxAutonomy: "   ", want: sandbox.AutonomyHigh},
		{name: "valid low", maxAutonomy: "low", want: sandbox.AutonomyLow},
		{name: "valid medium", maxAutonomy: "medium", want: sandbox.AutonomyMedium},
		{name: "valid high", maxAutonomy: "high", want: sandbox.AutonomyHigh},
		{name: "case-insensitive medium", maxAutonomy: "MEDIUM", want: sandbox.AutonomyMedium},
		// Fail-closed: an invalid non-empty value clamps to the most restrictive
		// ceiling instead of leaving the High default in place.
		{name: "invalid banana clamps to low", maxAutonomy: "banana", want: sandbox.AutonomyLow},
		{name: "invalid moderate clamps to low", maxAutonomy: "moderate", want: sandbox.AutonomyLow},
		{name: "invalid med clamps to low", maxAutonomy: "med", want: sandbox.AutonomyLow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if base := sandbox.DefaultPolicy(); base.MaxAutonomy != sandbox.AutonomyHigh {
				t.Fatalf("precondition: DefaultPolicy().MaxAutonomy = %q, want high", base.MaxAutonomy)
			}
			policy := applyConfiguredAutonomyCeiling(sandbox.DefaultPolicy(), tc.maxAutonomy)
			if policy.MaxAutonomy != tc.want {
				t.Fatalf("applyConfiguredAutonomyCeiling(_, %q).MaxAutonomy = %q, want %q", tc.maxAutonomy, policy.MaxAutonomy, tc.want)
			}
		})
	}
}

func TestApplyConfiguredSandboxPolicyDiagnosticsFlags(t *testing.T) {
	base := sandbox.DefaultPolicy()
	if base.BlockUnixSockets || base.MonitorDenials {
		t.Fatalf("precondition: diagnostic flags must default off")
	}

	// Omitted keys leave the (off) defaults untouched.
	if got := applyConfiguredSandboxPolicy(sandbox.DefaultPolicy(), config.SandboxConfig{}); got.BlockUnixSockets || got.MonitorDenials {
		t.Fatalf("empty config must not enable diagnostic flags: %#v", got)
	}

	// The flags opt in.
	got := applyConfiguredSandboxPolicy(sandbox.DefaultPolicy(), config.SandboxConfig{
		BlockUnixSockets: true,
		MonitorDenials:   true,
	})
	if !got.BlockUnixSockets || !got.MonitorDenials {
		t.Fatalf("diagnostic config not applied to policy: %#v", got)
	}
}

func TestApplyConfiguredSandboxPolicyHonorsAutoAllowBashEnv(t *testing.T) {
	// Unset/blank: the opt-in stays off so a sandboxed bash command still prompts.
	t.Setenv(sandbox.EnvAutoAllowBash, "")
	if got := applyConfiguredSandboxPolicy(sandbox.DefaultPolicy(), config.SandboxConfig{}); got.AutoAllowBashWhenSandboxed {
		t.Fatal("auto-allow-bash must be off when ZERO_SANDBOX_AUTO_ALLOW_BASH is unset")
	}
	// Truthy: the env opt-in is wired through the central policy builder.
	t.Setenv(sandbox.EnvAutoAllowBash, "true")
	if got := applyConfiguredSandboxPolicy(sandbox.DefaultPolicy(), config.SandboxConfig{}); !got.AutoAllowBashWhenSandboxed {
		t.Fatal("ZERO_SANDBOX_AUTO_ALLOW_BASH=true must enable AutoAllowBashWhenSandboxed")
	}
}

func newSandboxTestStore(t *testing.T) *sandbox.GrantStore {
	t.Helper()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json"),
		Now:      fixedCLITime("2026-06-05T14:45:00Z"),
	})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	return store
}

func sandboxPolicyRestrictionContains(restrictions []string, value string) bool {
	for _, restriction := range restrictions {
		if strings.Contains(restriction, value) {
			return true
		}
	}
	return false
}

func sandboxPolicyCapabilityStatus(capabilities []sandbox.BackendCapability, key string) sandbox.CapabilityStatus {
	for _, capability := range capabilities {
		if capability.Key == key {
			return capability.Status
		}
	}
	return ""
}
