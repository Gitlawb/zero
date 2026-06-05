package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestRunConfigPrintsRedactedSummary(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"config"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Config", "active provider: work", "max turns: 7", "work [openai]", "api key: set"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected config output to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "sk-test") {
		t.Fatalf("config output leaked API key: %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunConfigPrintsJSONSummary(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"config", "--json"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{`"activeProvider": "work"`, `"apiKeySet": true`, `"maxTurns": 7`} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected config JSON to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "sk-test") {
		t.Fatalf("config JSON leaked API key: %q", output)
	}
}

func TestRunConfigAndProvidersRedactBaseURLSecrets(t *testing.T) {
	deps := commandCenterSecretBaseURLDeps(t)
	commands := [][]string{
		{"config", "--json"},
		{"providers", "current"},
		{"providers", "current", "--json"},
	}

	for _, command := range commands {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := runWithDeps(command, &stdout, &stderr, deps)

		if exitCode != exitSuccess {
			t.Fatalf("%v: expected exit code %d, got %d: %s", command, exitSuccess, exitCode, stderr.String())
		}
		output := stdout.String()
		errorOutput := stderr.String()
		if errorOutput != "" {
			t.Fatalf("%v: expected empty stderr, got %q", command, errorOutput)
		}
		for _, leaked := range []string{"user:", "super-secret", "query-secret", "sk-test"} {
			if strings.Contains(output, leaked) {
				t.Fatalf("%v: output leaked %q: %q", command, leaked, output)
			}
			if strings.Contains(errorOutput, leaked) {
				t.Fatalf("%v: stderr leaked %q: %q", command, leaked, errorOutput)
			}
		}
		if !strings.Contains(output, "https://proxy.example/v1") {
			t.Fatalf("%v: expected sanitized provider base URL host/path, got %q", command, output)
		}
		if !strings.Contains(output, "api_key=[REDACTED]") {
			t.Fatalf("%v: expected redacted query secret, got %q", command, output)
		}
	}
}

func TestRunConfigRejectsModelOnlyFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"config", "--include-deprecated"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitUsage {
		t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown flag "--include-deprecated"`) {
		t.Fatalf("expected unknown flag error, got %q", stderr.String())
	}
}

func TestRunModelsListsRegistryModels(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"models", "list", "--provider", "anthropic"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Models") || !strings.Contains(output, "claude-sonnet-4.5") {
		t.Fatalf("expected anthropic models in output, got %q", output)
	}
	if strings.Contains(output, "gpt-4.1") {
		t.Fatalf("expected provider filter to hide OpenAI models, got %q", output)
	}
}

func TestRunModelsRejectsUnknownProvider(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"models", "--provider", "missing"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitUsage {
		t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown model provider") {
		t.Fatalf("expected unknown provider error, got %q", stderr.String())
	}
}

func TestRunProvidersShowsCurrentProvider(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"providers", "current"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Provider", "name: work", "kind: openai", "model: gpt-4.1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected provider output to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "sk-test") {
		t.Fatalf("provider output leaked API key: %q", output)
	}
}

func TestRunProvidersCurrentJSONIncludesRuntimeMetadata(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"providers", "current", "--json"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{`"name": "work"`, `"providerKind": "openai"`, `"apiModel": "gpt-4.1"`, `"apiKeySet": true`} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected provider JSON to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "sk-test") {
		t.Fatalf("provider JSON leaked API key: %q", output)
	}
}

func TestRunProvidersPositionalHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"providers", "help"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Usage:", "zero providers", "list", "current"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected providers help to contain %q, got %q", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunProvidersRejectsModelOnlyFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"providers", "list", "--provider", "openai"}, &stdout, &stderr, commandCenterDeps(t))

	if exitCode != exitUsage {
		t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown flag "--provider"`) {
		t.Fatalf("expected unknown flag error, got %q", stderr.String())
	}
}

func commandCenterDeps(t *testing.T) appDeps {
	t.Helper()

	cwd := t.TempDir()
	return appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			if workspaceRoot != cwd {
				t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
			}
			profile := config.ProviderProfile{
				Name:         "work",
				ProviderKind: config.ProviderKindOpenAI,
				BaseURL:      config.OpenAIBaseURL,
				APIKey:       "sk-test",
				Model:        "gpt-4.1",
			}
			return config.ResolvedConfig{
				ActiveProvider: "work",
				Providers:      []config.ProviderProfile{profile},
				Provider:       profile,
				MaxTurns:       7,
			}, nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return commandCenterProvider{}, nil
		},
	}
}

func commandCenterSecretBaseURLDeps(t *testing.T) appDeps {
	t.Helper()

	deps := commandCenterDeps(t)
	cwd, err := deps.getwd()
	if err != nil {
		t.Fatalf("getwd error: %v", err)
	}
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		if workspaceRoot != cwd {
			t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
		}
		profile := config.ProviderProfile{
			Name:         "gateway",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://user:super-secret@proxy.example/v1?api_key=query-secret&mode=test",
			APIKey:       "sk-test",
			Model:        "gateway-model",
		}
		return config.ResolvedConfig{
			ActiveProvider: "gateway",
			Providers:      []config.ProviderProfile{profile},
			Provider:       profile,
			MaxTurns:       7,
		}, nil
	}
	return deps
}

type commandCenterProvider struct{}

func (commandCenterProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent)
	close(ch)
	return ch, nil
}
