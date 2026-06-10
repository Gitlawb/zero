package cli

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/tui"
)

func TestSetupMissingCredentialEnv(t *testing.T) {
	tests := []struct {
		name    string
		profile config.ProviderProfile
		wantEnv string
		want    bool
	}{
		{
			name: "catalog provider",
			profile: config.ProviderProfile{
				Name:      "groq",
				CatalogID: "groq",
			},
			wantEnv: "GROQ_API_KEY",
			want:    true,
		},
		{
			name: "openai compatible without catalog",
			profile: config.ProviderProfile{
				Name:         "custom",
				ProviderKind: config.ProviderKindOpenAICompatible,
			},
			wantEnv: "OPENAI_API_KEY",
			want:    true,
		},
		{
			name: "local provider",
			profile: config.ProviderProfile{
				Name:      "local",
				CatalogID: "ollama",
			},
			want: false,
		},
		{
			name: "ollama cloud provider",
			profile: config.ProviderProfile{
				Name:      "ollama-cloud",
				CatalogID: "ollama-cloud",
			},
			wantEnv: "OLLAMA_API_KEY",
			want:    true,
		},
		{
			name: "credential resolved",
			profile: config.ProviderProfile{
				Name:         "openai",
				ProviderKind: config.ProviderKindOpenAI,
				APIKey:       "sk-test",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEnv, got := setupMissingCredentialEnv(tt.profile)
			if got != tt.want || gotEnv != tt.wantEnv {
				t.Fatalf("setupMissingCredentialEnv() = (%q, %v), want (%q, %v)", gotEnv, got, tt.wantEnv, tt.want)
			}
		})
	}
}

func TestSetupProviderOptionsUseRuntimeSupportedCatalog(t *testing.T) {
	options := setupProviderOptions()
	got := make([]string, 0, len(options))
	for _, option := range options {
		got = append(got, option.ID)
	}

	want := make([]string, 0)
	for _, descriptor := range providercatalog.All() {
		if providercatalog.RuntimeSupported(descriptor) {
			want = append(want, descriptor.ID)
		}
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("setupProviderOptions IDs = %#v, want runtime-supported catalog IDs %#v", got, want)
	}
	for _, excluded := range []string{"bedrock", "vertex"} {
		for _, id := range got {
			if id == excluded {
				t.Fatalf("setupProviderOptions included unsupported provider %q in %#v", excluded, got)
			}
		}
	}
}

func TestSaveSetupProviderStoresPastedAPIKey(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")

	result, err := saveSetupProvider(appDeps{
		userConfigPath: func() (string, error) {
			return configPath, nil
		},
	}, tui.SetupSelection{
		CatalogID: "ollama-cloud",
		Model:     "qwen3-coder:480b",
		APIKey:    "sk-pasted-secret",
	}, setupSaveOptions{})
	if err != nil {
		t.Fatalf("saveSetupProvider() error = %v", err)
	}

	if result.Provider.APIKey != "sk-pasted-secret" {
		t.Fatalf("Provider.APIKey = %q, want pasted key", result.Provider.APIKey)
	}
	if result.Provider.APIKeyEnv != "" {
		t.Fatalf("Provider.APIKeyEnv = %q, want empty when API key is pasted", result.Provider.APIKeyEnv)
	}

	cfg := readFileConfig(t, configPath)
	if cfg.ActiveProvider != "ollama-cloud" {
		t.Fatalf("ActiveProvider = %q, want ollama-cloud", cfg.ActiveProvider)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("Providers = %#v, want one provider", cfg.Providers)
	}
	if cfg.Providers[0].APIKey != "sk-pasted-secret" || cfg.Providers[0].APIKeyEnv != "" {
		t.Fatalf("stored provider credentials = APIKey %q APIKeyEnv %q, want pasted key only", cfg.Providers[0].APIKey, cfg.Providers[0].APIKeyEnv)
	}
}
