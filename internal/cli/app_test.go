package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/tui"
	"github.com/Gitlawb/zero/internal/update"
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
		{"sessions"},
		{"session"},
		{"plugins"},
		{"plugin"},
		{"hooks"},
		{"mcp"},
		{"sandbox"},
		{"update"},
		{"worktrees"},
		{"worktree"},
		{"verify"},
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

func TestRunUpdateCheckTextAndJSON(t *testing.T) {
	result := update.Result{
		CurrentVersion: "dev",
		LatestVersion:  "0.2.0",
		ReleaseURL:     "https://github.com/Gitlawb/zero/releases/tag/v0.2.0",
		TagName:        "v0.2.0",
		ReleaseAsset: update.AssetCheck{
			Platform:      "linux",
			Arch:          "x64",
			ArchiveName:   "zero-v0.2.0-linux-x64.tar.gz",
			ArchiveURL:    "https://example.test/zero-v0.2.0-linux-x64.tar.gz",
			ChecksumName:  "zero-v0.2.0-linux-x64.tar.gz.sha256",
			ChecksumURL:   "https://example.test/zero-v0.2.0-linux-x64.tar.gz.sha256",
			ArchiveFound:  true,
			ChecksumFound: true,
			Verified:      true,
		},
		UpdateAvailable: true,
	}
	deps := appDeps{
		checkUpdate: func(ctx context.Context, options update.Options) (update.Result, error) {
			result.CurrentVersion = options.CurrentVersion
			return result, nil
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"update", "--check"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Update available: dev -> 0.2.0") {
		t.Fatalf("unexpected update text: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Release asset: zero-v0.2.0-linux-x64.tar.gz") || !strings.Contains(stdout.String(), "Checksum asset: zero-v0.2.0-linux-x64.tar.gz.sha256") {
		t.Fatalf("unexpected update asset text: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"update", "--check", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var payload struct {
		CurrentVersion string `json:"currentVersion"`
		LatestVersion  string `json:"latestVersion"`
		ReleaseURL     string `json:"releaseUrl"`
		TagName        string `json:"tagName"`
		ReleaseAsset   struct {
			Platform      string `json:"platform"`
			Arch          string `json:"arch"`
			ArchiveName   string `json:"archiveName"`
			ChecksumName  string `json:"checksumName"`
			ArchiveFound  bool   `json:"archiveFound"`
			ChecksumFound bool   `json:"checksumFound"`
			Verified      bool   `json:"verified"`
		} `json:"releaseAsset"`
		UpdateAvailable bool `json:"updateAvailable"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("update JSON did not decode: %v\n%s", err, stdout.String())
	}
	if payload.CurrentVersion != "dev" ||
		payload.LatestVersion != result.LatestVersion ||
		payload.ReleaseURL != result.ReleaseURL ||
		payload.TagName != result.TagName ||
		payload.ReleaseAsset.ArchiveName != result.ReleaseAsset.ArchiveName ||
		payload.ReleaseAsset.ChecksumName != result.ReleaseAsset.ChecksumName ||
		!payload.ReleaseAsset.Verified ||
		payload.UpdateAvailable != result.UpdateAvailable {
		t.Fatalf("unexpected update JSON: %#v", payload)
	}
}

func TestRunUpdatePassesCheckOptions(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var got update.Options
	exitCode := runWithDeps([]string{"update", "--check", "--repo=Gitlawb/fork", "--endpoint", "https://example.test/releases/latest", "--timeout", "750ms", "--target", "windows-x64", "--json"}, &stdout, &stderr, appDeps{
		checkUpdate: func(_ context.Context, options update.Options) (update.Result, error) {
			got = options
			return update.Result{
				CurrentVersion:  options.CurrentVersion,
				LatestVersion:   "0.2.0",
				ReleaseURL:      "https://example.test/releases/tag/v0.2.0",
				TagName:         "v0.2.0",
				UpdateAvailable: true,
			}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if got.CurrentVersion != "dev" || got.Repository != "Gitlawb/fork" || got.Endpoint != "https://example.test/releases/latest" || got.Timeout != 750*time.Millisecond || got.GOOS != "windows" || got.GOARCH != "amd64" {
		t.Fatalf("unexpected update options: %#v", got)
	}
	if stdout.Len() == 0 {
		t.Fatalf("expected JSON output")
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunUpdateRejectsInvalidTarget(t *testing.T) {
	tests := []struct {
		args      []string
		errorText string
	}{
		{args: []string{"update", "--check", "--target", "solaris-sparc"}, errorText: "unsupported update target"},
		{args: []string{"update", "--check", "--target="}, errorText: "--target requires a non-empty value"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithDeps(tt.args, &stdout, &stderr, appDeps{
				checkUpdate: func(context.Context, update.Options) (update.Result, error) {
					t.Fatal("checkUpdate should not run for invalid target")
					return update.Result{}, nil
				},
			})

			if exitCode != exitUsage {
				t.Fatalf("expected usage exit code, got %d", exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if got := stderr.String(); !strings.Contains(got, tt.errorText) {
				t.Fatalf("expected target usage error %q, got %q", tt.errorText, got)
			}
		})
	}
}

func TestRunUpdateRequiresCheckFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"update"}, &stdout, &stderr, appDeps{})

	if exitCode == exitSuccess {
		t.Fatalf("expected non-success exit code, got %d", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "--check") {
		t.Fatalf("expected --check usage error, got %q", got)
	}
}

func TestRunUpdateRejectsInvalidTimeout(t *testing.T) {
	for _, args := range [][]string{
		{"update", "--check", "--timeout", "fast"},
		{"update", "--check", "--timeout", "0s"},
		{"update", "--check", "--timeout=-1s"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
				checkUpdate: func(context.Context, update.Options) (update.Result, error) {
					t.Fatal("checkUpdate should not run for invalid timeout")
					return update.Result{}, nil
				},
			})

			if exitCode != exitUsage {
				t.Fatalf("expected usage exit code, got %d", exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if got := stderr.String(); !strings.Contains(got, "invalid update timeout") {
				t.Fatalf("expected timeout usage error, got %q", got)
			}
		})
	}
}

func TestRunUpdateReportsCheckError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"update", "--check"}, &stdout, &stderr, appDeps{
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			return update.Result{}, errors.New("network failure")
		},
	})

	if exitCode == exitSuccess {
		t.Fatalf("expected non-success exit code, got %d", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "network failure") {
		t.Fatalf("expected update error, got %q", got)
	}
}

func TestRunUpdateHelpDocumentsCheckFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"update", "--help"}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	for _, want := range []string{"--check", "--repo", "--endpoint", "--timeout", "--target"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected update help to document %s, got %q", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunUpdateReportsUpToDate(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"update", "--check"}, &stdout, &stderr, appDeps{
		checkUpdate: func(context.Context, update.Options) (update.Result, error) {
			return update.Result{
				CurrentVersion:  "dev",
				LatestVersion:   "dev",
				ReleaseURL:      "https://github.com/Gitlawb/zero/releases/tag/dev",
				TagName:         "dev",
				UpdateAvailable: false,
			}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Fatalf("expected up-to-date output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
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
		"sandbox",
		"update",
		"worktrees",
		"verify",
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
		"Task",
		"read_file",
		"list_directory",
		"glob",
		"grep",
		"write_file",
		"edit_file",
		"apply_patch",
		"update_plan",
		"bash",
		"web_fetch",
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
	if options.AgentOptions.Autonomy != string(sandbox.AutonomyLow) {
		t.Fatalf("AgentOptions.Autonomy = %q, want %q", options.AgentOptions.Autonomy, sandbox.AutonomyLow)
	}
	if options.AgentOptions.Sandbox == nil {
		t.Fatal("AgentOptions.Sandbox = nil, want sandbox engine")
	}
	if options.SandboxStore == nil {
		t.Fatal("SandboxStore = nil, want sandbox grant store")
	}
	if options.PermissionMode != permissionMode {
		t.Fatalf("PermissionMode = %q, want %q", options.PermissionMode, permissionMode)
	}
}
