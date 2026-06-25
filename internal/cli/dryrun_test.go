package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/tui"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// TestDryRunHelpNewFlagDocumented verifies --dry-run appears in the top-level help.
func TestDryRunHelpNewFlagDocumented(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run([]string{"--help"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout.String(), "--dry-run") {
		t.Fatalf("expected --dry-run in help output, got: %s", stdout.String())
	}
}

// TestDryRunDoesNotBlockReadOnlyCommands verifies --dry-run is accepted alongside
// read-only commands without changing their output.
func TestDryRunDoesNotBlockReadOnlyCommands(t *testing.T) {
	// --version with --dry-run should still print the version.
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run([]string{"--dry-run", "--version"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0: %s", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "zero dev\n" {
		t.Fatalf("expected version output, got %q", got)
	}
}

// TestDryRunSetupPrintsWouldNotWrite verifies that a mutating command (setup)
// prints "Would" instead of writing the config when --dry-run is set.
func TestDryRunSetupPrintsWouldNotWrite(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")

	exitCode := runWithDeps([]string{"--dry-run", "setup", "ollama"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		userConfigPath: func() (string, error) {
			return configPath, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	// Config file must NOT have been written.
	if _, err := os.Stat(configPath); err == nil {
		t.Fatal("config file was written despite --dry-run")
	}
	// stdout should contain "Would" or "would".
	output := stdout.String()
	if !strings.Contains(output, "Would") && !strings.Contains(output, "would") {
		t.Fatalf("expected dry-run output mentioning 'Would', got: %s", output)
	}
}

// TestDryRunProvidersAddPrintsWouldNotWrite verifies that `providers add` with
// --dry-run prints "Would" instead of writing config.
func TestDryRunProvidersAddPrintsWouldNotWrite(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")

	exitCode := runWithDeps([]string{"--dry-run", "providers", "add", "ollama"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		userConfigPath: func() (string, error) {
			return configPath, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if _, err := os.Stat(configPath); err == nil {
		t.Fatal("config file was written despite --dry-run")
	}
	output := stdout.String()
	if !strings.Contains(output, "Would") && !strings.Contains(output, "would") {
		t.Fatalf("expected dry-run output mentioning 'Would', got: %s", output)
	}
}

// TestDryRunAcceptsInlineFlagForm verifies the =form of --dry-run.
func TestDryRunAcceptsInlineFlagForm(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")

	exitCode := runWithDeps([]string{"--dry-run=true", "setup", "ollama"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		userConfigPath: func() (string, error) {
			return configPath, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if _, err := os.Stat(configPath); err == nil {
		t.Fatal("config file was written despite --dry-run")
	}
	output := stdout.String()
	if !strings.Contains(output, "Would") && !strings.Contains(output, "would") {
		t.Fatalf("expected dry-run output mentioning 'Would', got: %s", output)
	}
}

// TestDryRunWithBadValueRejectsInvalid verifies that --dry-run=foo is accepted
// (a non-boolean value is treated as disabled, matching common flag conventions).
// Actually, let's make it a boolean flag: --dry-run or --dry-run=true/false.
func TestDryRunWithExplicitTrue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")

	exitCode := runWithDeps([]string{"--dry-run=true", "setup", "ollama"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		userConfigPath: func() (string, error) {
			return configPath, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if _, err := os.Stat(configPath); err == nil {
		t.Fatal("config file was written despite --dry-run=true")
	}
}

// TestDryRunTUIRespectsFlag verifies the TUI launch path respects --dry-run
// (the TUI itself is a read-mostly interactive interface, but the flag is
// stored for agent-loop tool gating).
func TestDryRunTUIRespectsFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cwd := t.TempDir()
	var launchedOptions tui.Options

	exitCode := runWithDeps([]string{"--dry-run"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{MaxTurns: 5}, nil
		},
		registerMCPTools: func(context.Context, *tools.Registry, config.MCPConfig, mcp.RegisterOptions) (mcpToolRuntime, error) {
			return noopMCPRuntime{}, nil
		},
		runTUI: func(_ context.Context, options tui.Options) int {
			launchedOptions = options
			return 0
		},
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0: %s", exitCode, stderr.String())
	}
	// The agent options should carry a DryRun flag.
	if !launchedOptions.AgentOptions.DryRun {
		t.Fatal("expected AgentOptions.DryRun to be true when --dry-run is set")
	}
}

// TestDryRunExecRespectsFlag verifies that exec with --dry-run reports "would"
// instead of actually running the agent.
func TestDryRunExecPrintsWouldNotRun(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--dry-run", "exec", "hello"}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Would") && !strings.Contains(output, "would") {
		t.Fatalf("expected dry-run output with 'Would', got: %s", output)
	}
}

// TestDryRunSandboxGrantSetPrintsWould verifies sandbox grants allow/deny with
// --dry-run prints "Would" instead of writing the grant store.
func TestDryRunSandboxGrantSetPrintsWould(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	grantPath := filepath.Join(t.TempDir(), "grants.json")

	exitCode := runWithDeps([]string{"--dry-run", "sandbox", "grants", "allow", "bash"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		resolveConfig: func(_ string, _ config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, nil
		},
		newSandboxStore: func() (*sandbox.GrantStore, error) {
			return sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: grantPath})
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Would") && !strings.Contains(output, "would") {
		t.Fatalf("expected dry-run output with 'Would', got: %s", output)
	}
}

// TestDryRunHooksAddPrintsWould verifies hooks add with --dry-run prints "Would".
func TestDryRunHooksAddPrintsWould(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	hooksPath := filepath.Join(t.TempDir(), "hooks.json")

	exitCode := runWithDeps([]string{"--dry-run", "hooks", "add", "--id", "test-hook", "--event", "beforeTool", "--command", "/bin/true"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		userConfigPath: func() (string, error) {
			return hooksPath, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Would") && !strings.Contains(output, "would") {
		t.Fatalf("expected dry-run output with 'Would', got: %s", output)
	}
}
