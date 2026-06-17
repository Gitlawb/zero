package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sandbox"
)

func sandboxCheckDeps(t *testing.T) (appDeps, string) {
	t.Helper()
	store := newSandboxTestStore(t)
	root := t.TempDir()
	return appDeps{
		getwd:           func() (string, error) { return root, nil },
		newSandboxStore: func() (*sandbox.GrantStore, error) { return store, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, nil
		},
		selectSandboxBackend: func(sandbox.BackendOptions) sandbox.Backend {
			return sandbox.Backend{Name: sandbox.BackendPolicyOnly, Platform: "windows", Fallback: true}
		},
	}, root
}

func TestRunSandboxCheckJSONDeniesOutOfWorkspaceWrite(t *testing.T) {
	deps, _ := sandboxCheckDeps(t)
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "check", "write_file", "--side-effect", "write", "--path", "/etc/passwd", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("check exit=%d stderr=%s", exitCode, stderr.String())
	}
	var payload struct {
		Tool string `json:"tool"`
		Plan struct {
			Policy struct {
				EffectiveMode string `json:"effectiveMode"`
			} `json:"policy"`
			Backend struct {
				Name string `json:"name"`
			} `json:"backend"`
		} `json:"plan"`
		Decision struct {
			Action string `json:"action"`
			Risk   struct {
				Level string `json:"level"`
			} `json:"risk"`
			Violation *struct {
				Code string `json:"code"`
			} `json:"violation"`
		} `json:"decision"`
		Grant struct {
			ToolName string `json:"toolName"`
			Matched  bool   `json:"matched"`
		} `json:"grant"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode check JSON: %v\n%s", err, stdout.String())
	}
	if payload.Tool != "write_file" {
		t.Fatalf("tool = %q", payload.Tool)
	}
	if payload.Plan.Policy.EffectiveMode != string(sandbox.ModeEnforce) {
		t.Fatalf("effectiveMode = %q, want enforce", payload.Plan.Policy.EffectiveMode)
	}
	if payload.Plan.Backend.Name != string(sandbox.BackendPolicyOnly) {
		t.Fatalf("backend = %q", payload.Plan.Backend.Name)
	}
	if payload.Decision.Action != string(sandbox.ActionDeny) {
		t.Fatalf("expected deny for out-of-workspace write, got %q\n%s", payload.Decision.Action, stdout.String())
	}
	if payload.Decision.Violation == nil {
		t.Fatalf("expected a violation for the out-of-workspace write")
	}
	if payload.Grant.ToolName != "write_file" || payload.Grant.Matched {
		t.Fatalf("expected an unmatched grant for write_file, got %#v", payload.Grant)
	}
}

func TestRunSandboxCheckTextRendersDecisionAndPlan(t *testing.T) {
	deps, _ := sandboxCheckDeps(t)
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "check", "read_file", "--side-effect", "read", "--path", "notes.txt"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("check exit=%d stderr=%s", exitCode, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Sandbox check: read_file",
		"decision: ",
		"risk: ",
		"policy: mode=enforce",
		"backend: policy-only",
		"grant: none recorded for this tool",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("check text missing %q:\n%s", want, out)
		}
	}
}

func TestRunSandboxCheckRequiresTool(t *testing.T) {
	deps, _ := sandboxCheckDeps(t)
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"sandbox", "check"}, &stdout, &stderr, deps)
	if exitCode == exitSuccess {
		t.Fatalf("expected a usage error when no tool is given, got success: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "tool name is required") {
		t.Fatalf("expected 'tool name is required', got %q", stderr.String())
	}
}
