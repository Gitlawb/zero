package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/tui"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

var errWriteFailed = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}

func TestRunPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run([]string{"--version"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if got := stdout.String(); got != "zero dev\n" {
		t.Fatalf("expected version output, got %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunPrintsHelp(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			assertHelpOutput(t, args)
		})
	}
}

func TestRunNoArgsLaunchesTUIWithNilProviderWhenNoProviderConfigured(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cwd := t.TempDir()
	var launchedOptions tui.Options

	exitCode := runWithDeps([]string{}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			if workspaceRoot != cwd {
				t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
			}
			return config.ResolvedConfig{MaxTurns: 12}, nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			t.Fatal("newProvider should not be called without a resolved provider")
			return nil, nil
		},
		runTUI: func(ctx context.Context, options tui.Options) int {
			launchedOptions = options
			return 7
		},
	})

	if exitCode != 7 {
		t.Fatalf("expected TUI exit code 7, got %d", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if launchedOptions.Cwd != cwd {
		t.Fatalf("Cwd = %q, want %q", launchedOptions.Cwd, cwd)
	}
	if launchedOptions.Provider != nil {
		t.Fatalf("Provider = %#v, want nil", launchedOptions.Provider)
	}
	if launchedOptions.ProviderName != "" || launchedOptions.ModelName != "" {
		t.Fatalf("provider metadata = %q/%q, want empty", launchedOptions.ProviderName, launchedOptions.ModelName)
	}
	assertCoreRegistry(t, launchedOptions.Registry)
	assertAgentOptions(t, launchedOptions, 12, agent.PermissionModeAuto)
}

func TestRunNoArgsLaunchesTUIWithResolvedProviderMetadata(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cwd := t.TempDir()
	fake := &cliFakeProvider{}
	var launchedOptions tui.Options
	var providerProfile config.ProviderProfile

	exitCode := runWithDeps([]string{}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			if workspaceRoot != cwd {
				t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
			}
			return config.ResolvedConfig{
				ActiveProvider: "work",
				Provider: config.ProviderProfile{
					Name:         "work",
					ProviderKind: config.ProviderKindOpenAI,
					BaseURL:      config.OpenAIBaseURL,
					APIKey:       "sk-test",
					Model:        "gpt-test",
				},
				MaxTurns: 5,
			}, nil
		},
		newProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			providerProfile = profile
			return fake, nil
		},
		runTUI: func(ctx context.Context, options tui.Options) int {
			launchedOptions = options
			return 0
		},
	})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if providerProfile.Name != "work" || providerProfile.Model != "gpt-test" {
		t.Fatalf("providerProfile = %#v, want resolved provider", providerProfile)
	}
	if launchedOptions.Provider != fake {
		t.Fatalf("Provider = %#v, want fake provider", launchedOptions.Provider)
	}
	if launchedOptions.ProviderName != "work" {
		t.Fatalf("ProviderName = %q, want work", launchedOptions.ProviderName)
	}
	if launchedOptions.ModelName != "gpt-test" {
		t.Fatalf("ModelName = %q, want gpt-test", launchedOptions.ModelName)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	assertCoreRegistry(t, launchedOptions.Registry)
	assertAgentOptions(t, launchedOptions, 5, agent.PermissionModeAuto)
}

func TestRunNoArgsReportsConfigErrorsWithoutLaunchingTUI(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	launchCalled := false

	exitCode := runWithDeps([]string{}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, errors.New("bad config")
		},
		runTUI: func(ctx context.Context, options tui.Options) int {
			launchCalled = true
			return 0
		},
	})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if launchCalled {
		t.Fatal("TUI launcher should not be called when config fails")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "bad config") {
		t.Fatalf("expected config error on stderr, got %q", got)
	}
}

func TestRunCommandsDoNotLaunchTUI(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"help"},
		{"--version"},
		{"version"},
		{"wat"},
		{"exec"},
		{"config"},
		{"models"},
		{"providers"},
		{"doctor"},
		{"search"},
		{"find"},
		{"plugins"},
		{"plugin"},
		{"hooks"},
		{"mcp"},
		{"serve"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			launchCalled := false

			_ = runWithDeps(args, &stdout, &stderr, appDeps{
				runTUI: func(ctx context.Context, options tui.Options) int {
					launchCalled = true
					return 0
				},
			})

			if launchCalled {
				t.Fatalf("TUI launcher should not be called for args %#v", args)
			}
		})
	}
}

func assertHelpOutput(t *testing.T, args []string) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(args, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	output := stdout.String()
	for _, want := range []string{
		"ZERO terminal coding agent",
		"Usage:",
		"zero [command]",
		"exec",
		"config",
		"models",
		"providers",
		"doctor",
		"search",
		"plugins",
		"hooks",
		"mcp",
		"serve",
		"--version",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected help output to contain %q, got %q", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunExecRequiresPrompt(t *testing.T) {
	for _, args := range [][]string{
		{"exec"},
		{"exec", ""},
		{"exec", "   "},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := Run(args, &stdout, &stderr)

			if exitCode != 2 {
				t.Fatalf("expected exit code 2, got %d", exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), "Prompt required") {
				t.Fatalf("expected prompt error, got %q", stderr.String())
			}
		})
	}
}

func TestRunReturnsFailureWhenStdoutWriteFails(t *testing.T) {
	exitCode := Run([]string{"--version"}, failingWriter{}, io.Discard)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
}

func TestRunReturnsFailureWhenStderrWriteFails(t *testing.T) {
	exitCode := Run([]string{"wat"}, io.Discard, failingWriter{})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run([]string{"wat"}, &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `unknown command "wat"`) {
		t.Fatalf("expected unknown command error, got %q", got)
	}
}

type cliFakeProvider struct{}

func (cliFakeProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent)
	close(ch)
	return ch, nil
}

func assertCoreRegistry(t *testing.T, registry *tools.Registry) {
	t.Helper()

	if registry == nil {
		t.Fatal("Registry = nil, want core tool registry")
	}

	for _, name := range []string{
		"read_file",
		"list_directory",
		"glob",
		"grep",
		"write_file",
		"edit_file",
		"apply_patch",
		"update_plan",
		"bash",
	} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("expected registry to include core tool %q", name)
		}
	}
}

func assertAgentOptions(t *testing.T, options tui.Options, maxTurns int, permissionMode agent.PermissionMode) {
	t.Helper()

	if options.AgentOptions.MaxTurns != maxTurns {
		t.Fatalf("AgentOptions.MaxTurns = %d, want %d", options.AgentOptions.MaxTurns, maxTurns)
	}
	if options.AgentOptions.PermissionMode != permissionMode {
		t.Fatalf("AgentOptions.PermissionMode = %q, want %q", options.AgentOptions.PermissionMode, permissionMode)
	}
	if options.PermissionMode != permissionMode {
		t.Fatalf("PermissionMode = %q, want %q", options.PermissionMode, permissionMode)
	}
}
