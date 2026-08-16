package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// The session's provider spelling can differ from the persisted row's
// ("openai" vs a saved "OpenAI"). Activation matches credential identity while
// the model write matches the row exactly, so the model write has to use the
// resolved spelling or it silently persists nothing.
func TestModelPersistenceUsesResolvedPersistedSpelling(t *testing.T) {
	newConfig := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(`{"activeProvider":"OpenAI","providers":[{"name":"OpenAI","catalogID":"openai","model":"gpt-5.1"},{"name":"ollama","catalogID":"ollama","provider_kind":"openai-compatible","baseURL":"http://localhost:11434/v1","model":"m1"}]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("persistSelectedModel", func(t *testing.T) {
		configPath := newConfig(t)
		m := newModel(context.Background(), Options{UserConfigPath: configPath})
		// The session spelling "openai" addresses the persisted "OpenAI" row.
		persisted, err := m.persistSelectedModel(config.ProviderProfile{Name: "openai", Model: "gpt-5.5"})
		if err != nil {
			t.Fatal(err)
		}
		if !persisted {
			t.Fatal("persistSelectedModel reported no write for a persisted case variant")
		}
		cfg := readTUIConfigFixture(t, configPath)
		if cfg.Providers[0].Model != "gpt-5.5" {
			t.Fatalf("model = %q, want gpt-5.5 written to the OpenAI row", cfg.Providers[0].Model)
		}
	})

	t.Run("switchProviderModel", func(t *testing.T) {
		configPath := newConfig(t)
		saved := []config.ProviderProfile{
			{Name: "OpenAI", CatalogID: "openai", Model: "gpt-5.1", APIKey: "sk-test"},
			{Name: "ollama", CatalogID: "ollama", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "http://localhost:11434/v1", Model: "m1"},
		}
		m := newModel(context.Background(), Options{
			UserConfigPath:  configPath,
			ProviderName:    "ollama",
			ModelName:       "m1",
			Provider:        &fakeProvider{},
			ProviderProfile: saved[1],
			SavedProviders:  saved,
			NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
				return &fakeProvider{}, nil
			},
		})

		// "openai" is the picker row's owner spelling, not the persisted one.
		if _, status, ok, _ := m.switchProviderModel("openai", "gpt-5.5"); !ok {
			t.Fatalf("switch to a case-variant provider spelling failed: %s", status)
		}
		cfg := readTUIConfigFixture(t, configPath)
		if cfg.ActiveProvider != "OpenAI" {
			t.Fatalf("activeProvider = %q, want the row's spelling OpenAI", cfg.ActiveProvider)
		}
		if cfg.Providers[0].Model != "gpt-5.5" {
			t.Fatalf("model = %q, want gpt-5.5 persisted onto the OpenAI row", cfg.Providers[0].Model)
		}
	})
}
