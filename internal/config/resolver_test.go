package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/modelregistry"
)

func TestResolveAppliesLayerPrecedence(t *testing.T) {
	userPath := writeConfig(t, `{
		"activeProvider": "user",
		"maxTurns": 3,
		"providers": [{
			"name": "user",
			"provider": "openai",
			"api_key": "sk-user",
			"model_id": "gpt-user"
		}]
	}`)
	projectPath := writeConfig(t, `{
		"activeProvider": "project",
		"maxTurns": 4,
		"providers": [{
			"name": "project",
			"provider_kind": "openai-compatible",
			"base_url": "https://project.example/v1",
			"apiKey": "sk-project",
			"model": "project-model"
		}]
	}`)

	resolved, err := Resolve(ResolveOptions{
		UserConfigPath:    userPath,
		ProjectConfigPath: projectPath,
		Env: map[string]string{
			"ZERO_PROVIDER": "env",
			"OPENAI_MODEL":  "env-model",
		},
		Overrides: Overrides{
			ActiveProvider: "cli",
			MaxTurns:       9,
			Provider: ProviderProfile{
				Name:         "cli",
				ProviderKind: "openai-compatible",
				BaseURL:      "https://cli.example/v1",
				APIKey:       "sk-cli",
				Model:        "cli-model",
			},
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.ActiveProvider != "cli" {
		t.Fatalf("ActiveProvider = %q, want cli", resolved.ActiveProvider)
	}
	if resolved.MaxTurns != 9 {
		t.Fatalf("MaxTurns = %d, want 9", resolved.MaxTurns)
	}
	if resolved.Provider.Name != "cli" || resolved.Provider.Model != "cli-model" {
		t.Fatalf("Provider = %#v, want CLI provider", resolved.Provider)
	}
	if resolved.Provider.BaseURL != "https://cli.example/v1" {
		t.Fatalf("BaseURL = %q, want CLI custom URL", resolved.Provider.BaseURL)
	}
}

func TestResolveSelectsActiveProviderProfile(t *testing.T) {
	path := writeConfig(t, `{
		"activeProvider": "beta",
		"providers": [
			{"name": "alpha", "provider": "openai", "apiKey": "sk-alpha", "model": "gpt-alpha"},
			{"name": "beta", "provider": "openai", "apiKey": "sk-beta", "model": "gpt-beta"}
		]
	}`)

	resolved, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Provider.Name != "beta" {
		t.Fatalf("Provider.Name = %q, want beta", resolved.Provider.Name)
	}
	if resolved.Provider.APIKey != "sk-beta" {
		t.Fatalf("Provider.APIKey = %q, want sk-beta", resolved.Provider.APIKey)
	}
}

func TestResolveIgnoresWhitespaceOnlyActiveProviderLayers(t *testing.T) {
	userPath := writeConfig(t, `{
		"activeProvider": "alpha",
		"providers": [
			{"name": "alpha", "provider": "openai", "apiKey": "sk-alpha", "model": "gpt-alpha"},
			{"name": "beta", "provider": "openai", "apiKey": "sk-beta", "model": "gpt-beta"}
		]
	}`)
	projectPath := writeConfig(t, `{"activeProvider": "   "}`)

	resolved, err := Resolve(ResolveOptions{
		UserConfigPath:    userPath,
		ProjectConfigPath: projectPath,
		Env:               map[string]string{},
		Overrides:         Overrides{ActiveProvider: "   "},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ActiveProvider != "alpha" {
		t.Fatalf("ActiveProvider = %q, want alpha", resolved.ActiveProvider)
	}
}

func TestResolveUsesOpenAIEnvFallback(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{
		Env: map[string]string{
			"OPENAI_API_KEY":  "sk-env",
			"OPENAI_BASE_URL": "https://env.example/v1",
			"OPENAI_MODEL":    "env-model",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.ActiveProvider != "openai" {
		t.Fatalf("ActiveProvider = %q, want openai", resolved.ActiveProvider)
	}
	if resolved.Provider.ProviderKind != ProviderKindOpenAICompatible {
		t.Fatalf("ProviderKind = %q, want openai-compatible", resolved.Provider.ProviderKind)
	}
	if resolved.Provider.APIKey != "sk-env" || resolved.Provider.Model != "env-model" {
		t.Fatalf("Provider = %#v, want env credentials/model", resolved.Provider)
	}
	if resolved.Provider.BaseURL != "https://env.example/v1" {
		t.Fatalf("BaseURL = %q, want env URL", resolved.Provider.BaseURL)
	}
}

func TestResolveUsesOpenAIAPIKeyOnlyWithDefaultModel(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{
		Env: map[string]string{
			"OPENAI_API_KEY": "sk-env",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.ActiveProvider != "openai" {
		t.Fatalf("ActiveProvider = %q, want openai", resolved.ActiveProvider)
	}
	if resolved.Provider.ProviderKind != ProviderKindOpenAI {
		t.Fatalf("ProviderKind = %q, want openai", resolved.Provider.ProviderKind)
	}
	if resolved.Provider.Model != modelregistry.DefaultModelID {
		t.Fatalf("Model = %q, want registry default model", resolved.Provider.Model)
	}
}

func TestResolveDoesNotDefaultOpenAICustomBaseURLModel(t *testing.T) {
	_, err := Resolve(ResolveOptions{
		Env: map[string]string{
			"OPENAI_API_KEY":  "sk-env",
			"OPENAI_BASE_URL": "https://gateway.example/v1",
		},
	})
	if err == nil {
		t.Fatal("expected Resolve() error for custom OpenAI-compatible env without model")
	}
	message := err.Error()
	if !strings.Contains(message, "provider openai requires model") {
		t.Fatalf("expected missing model error, got %q", message)
	}
	if strings.Contains(message, "sk-env") {
		t.Fatalf("error leaked API key: %q", message)
	}
}

func TestResolveUsesAnthropicEnvFallback(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{
		Env: map[string]string{
			"ZERO_PROVIDER":      "anthropic",
			"ANTHROPIC_API_KEY":  "sk-ant-env",
			"ANTHROPIC_BASE_URL": "https://anthropic.example",
			"ANTHROPIC_MODEL":    "claude-sonnet-4.5",
			"OPENAI_API_KEY":     "sk-openai-env",
			"OPENAI_MODEL":       "gpt-4.1",
			"GEMINI_API_KEY":     "sk-google-env",
			"GEMINI_MODEL":       "gemini-2.5-flash",
			"GEMINI_BASE_URL":    "https://google.example",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.ActiveProvider != "anthropic" {
		t.Fatalf("ActiveProvider = %q, want anthropic", resolved.ActiveProvider)
	}
	if resolved.Provider.ProviderKind != ProviderKindAnthropic {
		t.Fatalf("ProviderKind = %q, want anthropic", resolved.Provider.ProviderKind)
	}
	if resolved.Provider.APIKey != "sk-ant-env" || resolved.Provider.Model != "claude-sonnet-4.5" {
		t.Fatalf("Provider = %#v, want Anthropic env credentials/model", resolved.Provider)
	}
	if resolved.Provider.BaseURL != "https://anthropic.example" {
		t.Fatalf("BaseURL = %q, want Anthropic env URL", resolved.Provider.BaseURL)
	}
	if len(resolved.Providers) != 3 {
		t.Fatalf("Providers = %#v, want openai, anthropic, and google env profiles", resolved.Providers)
	}
}

func TestResolveUsesAnthropicEnvFallbackWithCustomProfile(t *testing.T) {
	path := writeConfig(t, `{
		"activeProvider": "claude-prod",
		"providers": [{
			"name": "claude-prod",
			"provider_kind": "anthropic",
			"description": "production Claude"
		}]
	}`)

	resolved, err := Resolve(ResolveOptions{
		ProjectConfigPath: path,
		Env: map[string]string{
			"ZERO_PROVIDER":      "claude-prod",
			"ANTHROPIC_API_KEY":  "sk-ant-env",
			"ANTHROPIC_BASE_URL": "https://anthropic.example",
			"ANTHROPIC_MODEL":    "claude-sonnet-4.5",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.ActiveProvider != "claude-prod" {
		t.Fatalf("ActiveProvider = %q, want claude-prod", resolved.ActiveProvider)
	}
	if resolved.Provider.ProviderKind != ProviderKindAnthropic {
		t.Fatalf("ProviderKind = %q, want anthropic", resolved.Provider.ProviderKind)
	}
	if resolved.Provider.APIKey != "sk-ant-env" || resolved.Provider.Model != "claude-sonnet-4.5" {
		t.Fatalf("Provider = %#v, want Anthropic env credentials/model on custom profile", resolved.Provider)
	}
	if resolved.Provider.BaseURL != "https://anthropic.example" {
		t.Fatalf("BaseURL = %q, want Anthropic env URL", resolved.Provider.BaseURL)
	}
	if resolved.Provider.Description != "production Claude" {
		t.Fatalf("Description = %q, want existing custom profile metadata preserved", resolved.Provider.Description)
	}
	if len(resolved.Providers) != 1 {
		t.Fatalf("Providers = %#v, want only custom Anthropic profile", resolved.Providers)
	}
}

func TestResolveUsesGoogleEnvFallbackAliases(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{
		Env: map[string]string{
			"ZERO_PROVIDER":   "google",
			"GOOGLE_API_KEY":  "sk-google-env",
			"GOOGLE_BASE_URL": "https://google.example",
			"GOOGLE_MODEL":    "gemini-2.5-pro",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.ActiveProvider != "google" {
		t.Fatalf("ActiveProvider = %q, want google", resolved.ActiveProvider)
	}
	if resolved.Provider.ProviderKind != ProviderKindGoogle {
		t.Fatalf("ProviderKind = %q, want google", resolved.Provider.ProviderKind)
	}
	if resolved.Provider.APIKey != "sk-google-env" || resolved.Provider.Model != "gemini-2.5-pro" {
		t.Fatalf("Provider = %#v, want Google env credentials/model", resolved.Provider)
	}
	if resolved.Provider.BaseURL != "https://google.example" {
		t.Fatalf("BaseURL = %q, want Google env URL", resolved.Provider.BaseURL)
	}
}

func TestResolveProviderCommandOverridesEnvProviderFields(t *testing.T) {
	command := writeCommand(t, commandScript{
		Stdout: `{"name":"cmd","provider":"openai","apiKey":"sk-command","model":"gpt-command"}`,
	})

	resolved, err := Resolve(ResolveOptions{
		ProviderCommand: command,
		Env: map[string]string{
			"OPENAI_API_KEY": "sk-env",
			"OPENAI_MODEL":   "gpt-env",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.ActiveProvider != "cmd" {
		t.Fatalf("ActiveProvider = %q, want cmd", resolved.ActiveProvider)
	}
	if resolved.Provider.APIKey != "sk-command" {
		t.Fatalf("APIKey = %q, want provider command key", resolved.Provider.APIKey)
	}
	if resolved.Provider.Model != "gpt-command" {
		t.Fatalf("Model = %q, want provider command model", resolved.Provider.Model)
	}
}

func TestResolveAllowsNoConfiguredProviders(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.ActiveProvider != "" {
		t.Fatalf("ActiveProvider = %q, want empty", resolved.ActiveProvider)
	}
	if len(resolved.Providers) != 0 {
		t.Fatalf("Providers = %#v, want empty", resolved.Providers)
	}
	if resolved.Provider != (ProviderProfile{}) {
		t.Fatalf("Provider = %#v, want zero value", resolved.Provider)
	}
	if resolved.MaxTurns != defaultMaxTurns {
		t.Fatalf("MaxTurns = %d, want %d", resolved.MaxTurns, defaultMaxTurns)
	}
}

func TestResolveRejectsActiveProviderWithoutConfiguredProfiles(t *testing.T) {
	path := writeConfig(t, `{"activeProvider":"ghost","providers":[]}`)

	resolved, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want missing active provider error")
	}
	if !strings.Contains(err.Error(), `active provider "ghost" not found`) {
		t.Fatalf("error = %q, want active provider missing message", err.Error())
	}
	if resolved.ActiveProvider != "" {
		t.Fatalf("ActiveProvider = %q, want empty", resolved.ActiveProvider)
	}
	if len(resolved.Providers) != 0 {
		t.Fatalf("Providers = %#v, want empty", resolved.Providers)
	}
	if resolved.Provider != (ProviderProfile{}) {
		t.Fatalf("Provider = %#v, want zero value", resolved.Provider)
	}
	if resolved.MaxTurns != 0 {
		t.Fatalf("MaxTurns = %d, want zero on failed resolve", resolved.MaxTurns)
	}
}

func TestResolveTrimsProviderProfileAliasesBeforeFallback(t *testing.T) {
	path := writeConfig(t, `{
		"activeProvider": "custom",
		"providers": [{
			"name": "custom",
			"provider_kind": "openai-compatible",
			"baseURL": "   ",
			"base_url": "https://custom.example/v1",
			"apiKey": "   ",
			"api_key": "sk-custom",
			"model": "   ",
			"model_id": "custom-model"
		}]
	}`)

	resolved, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Provider.BaseURL != "https://custom.example/v1" {
		t.Fatalf("BaseURL = %q, want alias fallback", resolved.Provider.BaseURL)
	}
	if resolved.Provider.APIKey != "sk-custom" {
		t.Fatalf("APIKey = %q, want alias fallback", resolved.Provider.APIKey)
	}
	if resolved.Provider.Model != "custom-model" {
		t.Fatalf("Model = %q, want alias fallback", resolved.Provider.Model)
	}
}

func TestResolveNormalizesOfficialOpenAIBaseURL(t *testing.T) {
	path := writeConfig(t, `{
		"activeProvider": "openai",
		"providers": [{
			"name": "openai",
			"provider": "openai",
			"baseURL": "openai",
			"apiKey": "sk-official",
			"model": "gpt-4.1"
		}]
	}`)

	resolved, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Provider.ProviderKind != ProviderKindOpenAI {
		t.Fatalf("ProviderKind = %q, want openai", resolved.Provider.ProviderKind)
	}
	if resolved.Provider.BaseURL != OpenAIBaseURL {
		t.Fatalf("BaseURL = %q, want %q", resolved.Provider.BaseURL, OpenAIBaseURL)
	}
}

func TestResolveAcceptsOfficialAnthropicAndGoogleProfiles(t *testing.T) {
	path := writeConfig(t, `{
		"activeProvider": "claude",
		"providers": [
			{"name": "claude", "provider": "anthropic", "apiKey": "sk-ant", "model": "claude-sonnet-4.5"},
			{"name": "gemini", "provider": "google", "apiKey": "sk-google", "model": "gemini-2.5-flash"}
		]
	}`)

	resolved, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Provider.ProviderKind != ProviderKindAnthropic {
		t.Fatalf("ProviderKind = %q, want anthropic", resolved.Provider.ProviderKind)
	}
	if resolved.Provider.BaseURL != AnthropicBaseURL {
		t.Fatalf("BaseURL = %q, want default Anthropic URL", resolved.Provider.BaseURL)
	}
	if resolved.Providers[1].ProviderKind != ProviderKindGoogle {
		t.Fatalf("Google ProviderKind = %q, want google", resolved.Providers[1].ProviderKind)
	}
	if resolved.Providers[1].BaseURL != GoogleBaseURL {
		t.Fatalf("Google BaseURL = %q, want default Google URL", resolved.Providers[1].BaseURL)
	}
}

func TestResolveRejectsOpenAICompatibleWithoutBaseURL(t *testing.T) {
	path := writeConfig(t, `{
		"activeProvider": "custom",
		"providers": [{
			"name": "custom",
			"provider_kind": "openai-compatible",
			"apiKey": "sk-custom",
			"model": "custom-model"
		}]
	}`)

	_, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "openai-compatible provider custom requires baseURL") {
		t.Fatalf("error = %q, want missing baseURL message", err.Error())
	}
}

func TestResolveRejectsUnknownProviderKind(t *testing.T) {
	path := writeConfig(t, `{
		"activeProvider": "bad",
		"providers": [{
			"name": "bad",
			"provider": "bedrock",
			"apiKey": "sk-bad",
			"model": "model"
		}]
	}`)

	_, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), `unknown provider kind "bedrock"`) {
		t.Fatalf("error = %q, want unknown provider kind", err.Error())
	}
}

func TestResolveRedactsSecretsFromErrors(t *testing.T) {
	path := writeConfig(t, `{
		"activeProvider": "custom",
		"providers": [{
			"name": "custom",
			"provider_kind": "openai-compatible",
			"apiKey": "sk-secret-value",
			"model": "custom-model"
		}]
	}`)

	_, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want validation error")
	}
	if strings.Contains(err.Error(), "sk-secret-value") {
		t.Fatalf("error leaked secret: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error = %q, want redaction marker", err.Error())
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "zero.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
