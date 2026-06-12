package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providerhealth"
	"github.com/Gitlawb/zero/internal/tui"
)

func TestFormatSetupCompleteIncludesTryThisExample(t *testing.T) {
	out := formatSetupComplete(tui.SetupResult{
		ConfigPath: "/home/u/.config/zero/config.json",
		Provider: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			APIKey:       "sk-x",
			Model:        "gpt-4.1",
		},
	})
	if !strings.Contains(out, "Zero setup complete") {
		t.Fatalf("missing completion header: %q", out)
	}
	// The working-state confirmation must include a concrete one-line "try this"
	// example so the user can immediately verify a real run.
	if !strings.Contains(out, `zero exec "`) || !strings.Contains(out, "--model gpt-4.1") {
		t.Fatalf("missing try-this exec example: %q", out)
	}
}

func TestRunSetupVerifyReportsClassifiedProbeFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	probed := false
	deps := appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		probeProviderHealth: func(_ context.Context, options providerhealth.Options) providerhealth.Result {
			probed = true
			if !options.Connectivity {
				t.Fatalf("setup --verify must request a connectivity probe")
			}
			return providerhealth.Result{
				Status: providerhealth.StatusFail,
				Checks: []providerhealth.Check{
					{ID: "provider.connectivity", Status: providerhealth.StatusFail, Category: providerhealth.CategoryAuth, Message: "Provider endpoint returned 401: invalid api key"},
				},
			}
		},
	}

	exitCode := runWithDeps([]string{"setup", "openai", "--api-key-env", "OPENAI_API_KEY", "--verify"}, &stdout, &stderr, deps)

	if !probed {
		t.Fatalf("expected setup --verify to run a probe")
	}
	if exitCode != exitProvider {
		t.Fatalf("exit code = %d, want %d (a failed probe is a provider error)", exitCode, exitProvider)
	}
	output := stderr.String()
	// The failure must be specific and fixable, not a stack trace.
	if !strings.Contains(strings.ToLower(output), "api key") {
		t.Fatalf("expected a fixable auth remedy on stderr, got %q", output)
	}
}

func TestRunSetupVerifySucceedsAndConfirmsWorking(t *testing.T) {
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	deps := appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		probeProviderHealth: func(context.Context, providerhealth.Options) providerhealth.Result {
			return providerhealth.Result{
				Status: providerhealth.StatusPass,
				Checks: []providerhealth.Check{
					{ID: "provider.connectivity", Status: providerhealth.StatusPass, Message: "reachable"},
				},
			}
		},
	}

	exitCode := runWithDeps([]string{"setup", "openai", "--api-key-env", "OPENAI_API_KEY", "--verify"}, &stdout, &stderr, deps)

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Zero setup complete") {
		t.Fatalf("missing completion header: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "verified") && !strings.Contains(strings.ToLower(out), "reachable") {
		t.Fatalf("a passing probe should confirm the provider is verified/reachable, got %q", out)
	}
}

func TestRunSetupWithoutVerifyDoesNotProbe(t *testing.T) {
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "config.json")
	deps := appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		probeProviderHealth: func(context.Context, providerhealth.Options) providerhealth.Result {
			t.Fatal("setup without --verify must not probe")
			return providerhealth.Result{}
		},
	}

	exitCode := runWithDeps([]string{"setup", "ollama"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
}
