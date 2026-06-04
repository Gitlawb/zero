package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			"provider": "anthropic",
			"apiKey": "sk-bad",
			"model": "claude"
		}]
	}`)

	_, err := Resolve(ResolveOptions{ProjectConfigPath: path, Env: map[string]string{}})
	if err == nil {
		t.Fatal("Resolve() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), `unknown provider kind "anthropic"`) {
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
