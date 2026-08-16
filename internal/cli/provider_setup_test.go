package cli

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
)

// Regression for issue #555's follow-up: `zero providers check` must not
// error that a no-auth custom endpoint requires an API key, matching what
// /model and /providers already treat as usable.
func TestValidateProviderRuntimeReadyCustomEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		profile config.ProviderProfile
		wantErr bool
	}{
		{
			name: "custom openai compatible with no credential configured",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				Model:     "custom-model",
			},
			wantErr: false,
		},
		{
			name: "custom openai compatible with stale legacy default env",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				APIKeyEnv: "OPENAI_API_KEY",
				Model:     "custom-model",
			},
			wantErr: false,
		},
		{
			name: "custom openai compatible with explicit non-default env still requires it",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				APIKeyEnv: "LLAMA_CPP_API_KEY",
				Model:     "custom-model",
			},
			wantErr: true,
		},
		{
			name: "catalog provider missing key still errors",
			profile: config.ProviderProfile{
				Name:      "groq",
				CatalogID: "groq",
				Model:     "llama-3.3-70b-versatile",
			},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateProviderRuntimeReady(c.profile)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateProviderRuntimeReady() error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func isolateKimiDeviceIDStorage(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("HOME", root)
}

func TestProvidersAddKimiCodeDoesNotPersistRuntimeHeaders(t *testing.T) {
	isolateKimiDeviceIDStorage(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}

	var stdout, stderr bytes.Buffer
	code := runProviders([]string{"add", "kimi-code"}, &stdout, &stderr, deps)
	if code != exitSuccess {
		t.Fatalf("providers add kimi-code failed: %d, stderr: %s", code, stderr.String())
	}

	cfg := readCLIConfigFixture(t, configPath)
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	for k := range cfg.Providers[0].CustomHeaders {
		if providercatalog.IsRuntimeIdentityHeader(k) {
			t.Fatalf("config.json contains persisted runtime identity header %q", k)
		}
	}

	resolved, err := config.Resolve(config.ResolveOptions{UserConfigPath: configPath})
	if err != nil {
		t.Fatalf("config.Resolve: %v", err)
	}
	active := resolved.Provider
	if active.CustomHeaders["X-Msh-Platform"] != "kimi_code_cli" {
		t.Fatalf("resolved profile missing X-Msh-Platform: %#v", active.CustomHeaders)
	}
	if active.CustomHeaders["X-Msh-Device-Id"] == "" {
		t.Fatalf("resolved profile missing X-Msh-Device-Id: %#v", active.CustomHeaders)
	}
}

func TestSetupKimiCodeDoesNotPersistRuntimeHeaders(t *testing.T) {
	isolateKimiDeviceIDStorage(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}

	var stdout, stderr bytes.Buffer
	code := runSetup([]string{"kimi-code"}, &stdout, &stderr, deps)
	if code != exitSuccess {
		t.Fatalf("setup kimi-code failed: %d, stderr: %s", code, stderr.String())
	}

	cfg := readCLIConfigFixture(t, configPath)
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	for k := range cfg.Providers[0].CustomHeaders {
		if providercatalog.IsRuntimeIdentityHeader(k) {
			t.Fatalf("config.json contains persisted runtime identity header %q", k)
		}
	}
}
