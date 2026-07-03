package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agentcli"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
)

func TestRunProvidersModelsListsDiscoveredModels(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	deps := commandCenterDeps(t)
	var probed config.ProviderProfile
	deps.discoverProviderModels = func(_ context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
		probed = profile
		return []providermodeldiscovery.Model{
			{ID: "team/model-a", Description: "first"},
			{ID: "team/model-b"},
		}, nil
	}

	exitCode := runWithDeps([]string{"providers", "models"}, &stdout, &stderr, deps)

	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	// No name given → the active provider is probed, with its resolved credential.
	if probed.Name != "work" {
		t.Fatalf("probed provider = %q, want active provider work", probed.Name)
	}
	out := stdout.String()
	for _, want := range []string{"Provider models (work)", "team/model-a — first", "team/model-b", "2 models discovered", "--model team/model-a"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunProvidersModelsJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	deps := commandCenterDeps(t)
	deps.discoverProviderModels = func(_ context.Context, _ config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
		return []providermodeldiscovery.Model{{ID: "m1", Description: "d1"}, {ID: "m2"}}, nil
	}

	exitCode := runWithDeps([]string{"providers", "models", "--json"}, &stdout, &stderr, deps)

	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	var payload struct {
		Provider string `json:"provider"`
		Count    int    `json:"count"`
		Models   []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
		} `json:"models"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode json: %v\n%s", err, stdout.String())
	}
	if payload.Provider != "work" || payload.Count != 2 || len(payload.Models) != 2 {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Models[0].ID != "m1" || payload.Models[0].Description != "d1" {
		t.Fatalf("model[0] = %#v", payload.Models[0])
	}
	// A model with no description omits the field rather than emitting an empty string.
	if payload.Models[1].ID != "m2" || payload.Models[1].Description != "" {
		t.Fatalf("model[1] = %#v", payload.Models[1])
	}
}

func TestRunProvidersModelsSelectsNamedProvider(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	deps := commandCenterDeps(t)
	custom := config.ProviderProfile{
		Name:         "self-hosted",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      "https://models.internal/v1",
		APIKey:       "sk-internal",
		Model:        "house-model",
	}
	deps.resolveConfig = func(string, config.Overrides) (config.ResolvedConfig, error) {
		work := config.ProviderProfile{Name: "work", ProviderKind: config.ProviderKindOpenAI, BaseURL: config.OpenAIBaseURL, APIKey: "sk", Model: "gpt-4.1"}
		return config.ResolvedConfig{ActiveProvider: "work", Provider: work, Providers: []config.ProviderProfile{work, custom}}, nil
	}
	var probed config.ProviderProfile
	deps.discoverProviderModels = func(_ context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
		probed = profile
		return []providermodeldiscovery.Model{{ID: "house-model"}}, nil
	}

	exitCode := runWithDeps([]string{"providers", "models", "self-hosted"}, &stdout, &stderr, deps)

	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if probed.Name != "self-hosted" || probed.BaseURL != "https://models.internal/v1" {
		t.Fatalf("probed = %#v, want the named custom provider", probed)
	}
}

func TestRunProvidersModelsUnknownProviderFails(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	deps := commandCenterDeps(t)
	called := false
	deps.discoverProviderModels = func(_ context.Context, _ config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
		called = true
		return nil, nil
	}

	exitCode := runWithDeps([]string{"providers", "models", "ghost"}, &stdout, &stderr, deps)

	if exitCode == exitSuccess {
		t.Fatalf("exit = %d, want non-zero for unknown provider", exitCode)
	}
	if called {
		t.Fatal("discovery must not run for an unknown provider")
	}
	if !strings.Contains(stderr.String(), `provider "ghost" not found`) {
		t.Fatalf("stderr = %q, want provider-not-found", stderr.String())
	}
}

func TestRunProvidersModelsSurfacesDiscoveryError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	deps := commandCenterDeps(t)
	deps.discoverProviderModels = func(_ context.Context, _ config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
		return nil, errors.New("models endpoint returned 404")
	}

	exitCode := runWithDeps([]string{"providers", "models"}, &stdout, &stderr, deps)

	if exitCode != exitProvider {
		t.Fatalf("exit = %d, want exitProvider %d", exitCode, exitProvider)
	}
	if !strings.Contains(stderr.String(), "models endpoint returned 404") {
		t.Fatalf("stderr = %q, want the discovery error", stderr.String())
	}
}

func TestApplyAuthCLIDiscoveryCredential(t *testing.T) {
	t.Run("anthropic access token becomes bearer header with oauth beta", func(t *testing.T) {
		profile := config.ProviderProfile{Name: "claude-code", AuthCLI: "claude"}
		creds := agentcli.Credentials{AccessToken: "tok"}

		got := applyAuthCLIDiscoveryCredential(profile, "anthropic", creds)

		if got.AuthHeader != "Authorization" {
			t.Fatalf("AuthHeader = %q, want Authorization", got.AuthHeader)
		}
		if got.AuthHeaderValue != "Bearer tok" {
			t.Fatalf("AuthHeaderValue = %q, want %q", got.AuthHeaderValue, "Bearer tok")
		}
		if got.CustomHeaders["anthropic-beta"] != "oauth-2025-04-20" {
			t.Fatalf("CustomHeaders[anthropic-beta] = %q, want oauth-2025-04-20", got.CustomHeaders["anthropic-beta"])
		}
		if got.APIKey != "" {
			t.Fatalf("APIKey = %q, want empty when only an access token is present", got.APIKey)
		}
	})

	t.Run("chatgpt api key wins as profile APIKey with no header", func(t *testing.T) {
		profile := config.ProviderProfile{Name: "codex", AuthCLI: "codex"}
		creds := agentcli.Credentials{APIKey: "sk-openai"}

		got := applyAuthCLIDiscoveryCredential(profile, "chatgpt", creds)

		if got.APIKey != "sk-openai" {
			t.Fatalf("APIKey = %q, want sk-openai", got.APIKey)
		}
		if got.AuthHeaderValue != "" {
			t.Fatalf("AuthHeaderValue = %q, want empty when a stored API key is used", got.AuthHeaderValue)
		}
		if got.CustomHeaders != nil {
			t.Fatalf("CustomHeaders = %#v, want nil (no beta header for a plain API key)", got.CustomHeaders)
		}
	})

	t.Run("chatgpt access token becomes bearer header without anthropic beta", func(t *testing.T) {
		profile := config.ProviderProfile{Name: "codex", AuthCLI: "codex"}
		creds := agentcli.Credentials{AccessToken: "tok"}

		got := applyAuthCLIDiscoveryCredential(profile, "chatgpt", creds)

		if got.AuthHeader != "Authorization" {
			t.Fatalf("AuthHeader = %q, want Authorization", got.AuthHeader)
		}
		if got.AuthHeaderValue != "Bearer tok" {
			t.Fatalf("AuthHeaderValue = %q, want %q", got.AuthHeaderValue, "Bearer tok")
		}
		if _, ok := got.CustomHeaders["anthropic-beta"]; ok {
			t.Fatalf("CustomHeaders = %#v, want no anthropic-beta for the chatgpt catalog", got.CustomHeaders)
		}
	})
}

// TestRunProvidersModelsAuthCLIDispatchUsesInjectedCredentialExtraction covers
// the full discoveryCredentialProfile AuthCLI branch — harness lookup PLUS
// credential extraction — through deps.extractAgentCLICredentials, not just
// the pure applyAuthCLIDiscoveryCredential helper above. Before this seam
// existed, this branch called agentcli.ExtractCredentials directly and could
// only be exercised by touching a real PATH/filesystem/keychain.
func TestRunProvidersModelsAuthCLIDispatchUsesInjectedCredentialExtraction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := commandCenterDeps(t)
	deps.resolveConfig = func(string, config.Overrides) (config.ResolvedConfig, error) {
		profile := config.ProviderProfile{Name: "claude-code", ProviderKind: config.ProviderKindAnthropic, AuthCLI: "claude", Model: "claude-sonnet-4.5"}
		return config.ResolvedConfig{ActiveProvider: "claude-code", Providers: []config.ProviderProfile{profile}, Provider: profile, MaxTurns: 7}, nil
	}
	var extractCalledWith agentcli.Harness
	deps.extractAgentCLICredentials = func(h agentcli.Harness, _ agentcli.Deps) (agentcli.Credentials, bool, error) {
		extractCalledWith = h
		return agentcli.Credentials{AccessToken: "tok-from-fake"}, true, nil
	}
	var probed config.ProviderProfile
	deps.discoverProviderModels = func(_ context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
		probed = profile
		return nil, nil
	}

	exitCode := runWithDeps([]string{"providers", "models"}, &stdout, &stderr, deps)

	if exitCode != exitSuccess {
		t.Fatalf("exit = %d, want %d: %s", exitCode, exitSuccess, stderr.String())
	}
	if extractCalledWith.ID != "claude" {
		t.Fatalf("extractAgentCLICredentials called with harness %#v, want claude", extractCalledWith)
	}
	if probed.AuthHeaderValue != "Bearer tok-from-fake" {
		t.Fatalf("probed.AuthHeaderValue = %q, want the fake-extracted token, proving the injected seam (not a real ExtractCredentials call) fed the discovery request", probed.AuthHeaderValue)
	}
}
