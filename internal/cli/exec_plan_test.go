package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
)

func TestParseExecArgsRecognizesPlanFlag(t *testing.T) {
	options, _, err := parseExecArgs([]string{"--plan", "draft a plan"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if !options.plan {
		t.Fatal("expected --plan to set options.plan")
	}
}

func TestParseExecArgsRejectsPlanWithUseSpec(t *testing.T) {
	_, _, err := parseExecArgs([]string{"--plan", "--use-spec", "draft a plan"})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected --plan/--use-spec validation, got %v", err)
	}
}

func TestParseExecArgsRejectsPlanWithSkipPermissionsUnsafe(t *testing.T) {
	_, _, err := parseExecArgs([]string{"--plan", "--skip-permissions-unsafe", "draft a plan"})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected --plan/--skip-permissions-unsafe validation, got %v", err)
	}
}

// TestParseExecArgsRejectsPlanWithWorktree guards against worktree
// preparation (a filesystem mutation) running ahead of the plan mode gate:
// options.worktree is processed in runExec before the plan permission mode is
// assigned, so the combination must be rejected during option validation,
// before any worktree prep can occur.
func TestParseExecArgsRejectsPlanWithWorktree(t *testing.T) {
	_, _, err := parseExecArgs([]string{"--plan", "--worktree", "draft a plan"})
	if err == nil || !strings.Contains(err.Error(), "--worktree") {
		t.Fatalf("expected --plan/--worktree validation, got %v", err)
	}
}

func TestParseExecArgsRejectsPlanWithNonPlanPermissionMode(t *testing.T) {
	_, _, err := parseExecArgs([]string{"--plan", "--permission-mode=ask", "draft a plan"})
	if err == nil || !strings.Contains(err.Error(), "--permission-mode") {
		t.Fatalf("expected --plan/--permission-mode validation, got %v", err)
	}

	options, _, err := parseExecArgs([]string{"--plan", "--permission-mode=plan", "draft a plan"})
	if err != nil {
		t.Fatalf("expected --plan with --permission-mode=plan to succeed, got %v", err)
	}
	if !options.plan || options.permissionMode != "plan" {
		t.Fatalf("expected options.plan=true and permissionMode=plan, got plan=%v mode=%q", options.plan, options.permissionMode)
	}
}

// TestRunExecPlanHidesWriteAndShellToolsFromListing drives the real --plan
// flag through runExec (via --list-tools, so no provider is needed) and
// confirms write_file and bash — advertised under every other mode covered by
// TestRunExecListToolsAppliesModeBeforeListing-style tests — are hidden,
// mirroring the TUI /plan on gating end to end from the CLI entry point.
func TestRunExecPlanHidesWriteAndShellToolsFromListing(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"exec", "--plan", "--list-tools"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	listing := stdout.String()
	if !strings.Contains(listing, "Tools visible to model") {
		t.Fatalf("expected --plan --list-tools to list tools, got %q", listing)
	}
	if !strings.Contains(listing, "read_file") {
		t.Fatalf("expected plan mode to still list read_file, got %q", listing)
	}
	for _, hidden := range []string{"write_file", "edit_file", "apply_patch", "bash"} {
		if strings.Contains(listing, hidden) {
			t.Fatalf("expected --plan to hide %q from the tool listing, got %q", hidden, listing)
		}
	}
}

func TestResolveExecPermissionModePlanOverride(t *testing.T) {
	options := execOptions{autonomy: "low", plan: true}
	mode, err := resolveExecPermissionMode(options)
	if err != nil {
		t.Fatalf("resolveExecPermissionMode: %v", err)
	}
	// resolveExecPermissionMode itself only resolves --auto; the --plan override
	// is applied by the caller (runExec) afterward, same as --use-spec. This
	// pins the precondition: --plan must not interfere with autonomy resolution.
	if mode != agent.PermissionModeAuto {
		t.Fatalf("resolveExecPermissionMode with --plan = %q, want auto (override applied by the caller)", mode)
	}
}
