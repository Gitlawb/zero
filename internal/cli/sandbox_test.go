package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

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

func TestRunSandboxPolicyInspectTextAndJSON(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{
		getwd:           func() (string, error) { return t.TempDir(), nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		selectSandboxBackend: func(options sandbox.BackendOptions) sandbox.Backend {
			return sandbox.Backend{
				Name:     sandbox.BackendPolicyOnly,
				Platform: "windows",
				Fallback: true,
				Message:  "policy-only fallback: Windows native sandbox adapter is not implemented",
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
						SupportLevel string                      `json:"supportLevel"`
						Capabilities []sandbox.BackendCapability `json:"capabilities"`
						Restrictions []string                    `json:"restrictions"`
						Warnings     []string                    `json:"warnings"`
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
				if sandboxPolicyCapabilityStatus(payload.Plan.Capabilities, "native_process_isolation") != sandbox.CapabilityUnavailable {
					t.Fatalf("expected native isolation unavailable, got %#v", payload.Plan.Capabilities)
				}
				if !sandboxPolicyRestrictionContains(payload.Plan.Restrictions, "native process isolation unavailable on windows") {
					t.Fatalf("expected JSON plan to document Windows fallback, got %#v", payload.Plan.Restrictions)
				}
				if !sandboxPolicyRestrictionContains(payload.Plan.Warnings, "Windows native sandbox adapter is not implemented") {
					t.Fatalf("expected JSON warnings to document Windows fallback, got %#v", payload.Plan.Warnings)
				}
			} else {
				output := stdout.String()
				for _, want := range []string{
					"Zero sandbox policy",
					"backend: policy-only",
					"support_level: policy-only",
					"backend_fallback: true",
					"backend_command_wrapping: false",
					"backend_native_isolation: false",
					"backend_platform: windows",
					"Windows native sandbox adapter is not implemented",
				} {
					if !strings.Contains(output, want) {
						t.Fatalf("expected policy text to contain %q, got %q", want, output)
					}
				}
			}
		})
	}
}

func TestRunSandboxPolicyEffectiveTextAndJSON(t *testing.T) {
	store := newSandboxTestStore(t)
	deps := appDeps{
		getwd:           func() (string, error) { return t.TempDir(), nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
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
