package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSetActiveProviderSwitchesConfiguredProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "OpenAI",
		Providers: []ProviderProfile{
			{
				Name:         "OpenAI",
				ProviderKind: ProviderKindOpenAI,
				Model:        "gpt-4.1",
			},
			{
				Name:         "Anthropic",
				ProviderKind: ProviderKindAnthropic,
				Model:        "claude-3-5-sonnet-latest",
			},
		},
	}, 0o600)

	cfg, err := SetActiveProvider(path, "  anthropic  ")
	if err != nil {
		t.Fatalf("SetActiveProvider() error = %v", err)
	}

	if cfg.ActiveProvider != "Anthropic" {
		t.Fatalf("ActiveProvider = %q, want Anthropic", cfg.ActiveProvider)
	}

	persisted := readConfigFixture(t, path)
	if persisted.ActiveProvider != "Anthropic" {
		t.Fatalf("persisted ActiveProvider = %q, want Anthropic", persisted.ActiveProvider)
	}
}

func TestSetActiveProviderRejectsUnknownProviderWithoutRewriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
			{Name: "anthropic", ProviderKind: ProviderKindAnthropic, Model: "claude-3-5-sonnet-latest"},
		},
	}, 0o600)

	_, err := SetActiveProvider(path, "google")
	if err == nil {
		t.Fatal("SetActiveProvider() error = nil, want error")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config was rewritten for unknown provider\nbefore: %s\nafter: %s", before, after)
	}

	persisted := readConfigFixture(t, path)
	if persisted.ActiveProvider != "openai" {
		t.Fatalf("persisted ActiveProvider = %q, want openai", persisted.ActiveProvider)
	}
}

func TestSetActiveProviderRejectsEmptyProviderName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
		},
	}, 0o600)

	_, err := SetActiveProvider(path, " \t\n ")
	if err == nil {
		t.Fatal("SetActiveProvider() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "provider name is required") {
		t.Fatalf("SetActiveProvider() error = %q, want provider name required", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config was rewritten for empty provider name\nbefore: %s\nafter: %s", before, after)
	}
}

func TestSetActiveProviderRejectsEmptyConfigPath(t *testing.T) {
	_, err := SetActiveProvider(" \t\n ", "openai")
	if err == nil {
		t.Fatal("SetActiveProvider() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "config path is required") {
		t.Fatalf("SetActiveProvider() error = %q, want config path required", err)
	}
}

func TestSetActiveProviderRejectsMissingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")

	_, err := SetActiveProvider(path, "openai")
	if err == nil {
		t.Fatal("SetActiveProvider() error = nil, want error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("SetActiveProvider() error = %v, want not-exist error", err)
	}
}

func TestSetActiveProviderTightensExistingConfigFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX mode bits reliably")
	}

	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
			{Name: "anthropic", ProviderKind: ProviderKindAnthropic, Model: "claude-3-5-sonnet-latest"},
		},
	}, 0o644)

	_, err := SetActiveProvider(path, "anthropic")
	if err != nil {
		t.Fatalf("SetActiveProvider() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 0600", got)
	}
}

func TestSetProviderModelUpdatesConfiguredProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{
				Name:         "openai",
				ProviderKind: ProviderKindOpenAI,
				APIKey:       "sk-test",
				Model:        "gpt-4.1",
			},
			{
				Name:         "anthropic",
				ProviderKind: ProviderKindAnthropic,
				Model:        "claude-sonnet-4.5",
			},
		},
	}, 0o600)

	cfg, err := SetProviderModel(path, " OpenAI ", " gpt-4.1-mini ")
	if err != nil {
		t.Fatalf("SetProviderModel() error = %v", err)
	}

	if cfg.Providers[0].Model != "gpt-4.1-mini" {
		t.Fatalf("updated provider model = %q, want gpt-4.1-mini", cfg.Providers[0].Model)
	}
	if cfg.Providers[0].APIKey != "sk-test" {
		t.Fatalf("provider credential was not preserved: %#v", cfg.Providers[0])
	}
	if cfg.Providers[1].Model != "claude-sonnet-4.5" {
		t.Fatalf("unrelated provider changed: %#v", cfg.Providers[1])
	}

	persisted := readConfigFixture(t, path)
	if persisted.Providers[0].Model != "gpt-4.1-mini" {
		t.Fatalf("persisted provider model = %q, want gpt-4.1-mini", persisted.Providers[0].Model)
	}
	if persisted.ActiveProvider != "openai" {
		t.Fatalf("active provider changed to %q", persisted.ActiveProvider)
	}
}

func TestSetProviderModelRejectsUnknownProviderWithoutRewriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
		},
	}, 0o600)

	_, err := SetProviderModel(path, "anthropic", "claude-sonnet-4.5")
	if err == nil {
		t.Fatal("SetProviderModel() error = nil, want error")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config was rewritten for unknown provider\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpsertProviderTightensExistingConfigFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX mode bits reliably")
	}

	path := filepath.Join(t.TempDir(), "zero.json")
	if err := os.WriteFile(path, []byte(`{"providers":[]}`), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	_, err := UpsertProvider(path, ProviderProfile{
		Name:         "openai",
		ProviderKind: ProviderKindOpenAI,
		APIKey:       "sk-test",
		Model:        "gpt-4.1",
	}, true)
	if err != nil {
		t.Fatalf("UpsertProvider() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 0600", got)
	}
}

func TestSetFavoriteModelsPersistsUserPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
		},
	}, 0o600)

	cfg, err := SetFavoriteModels(path, []string{" qwen3-coder:480b ", "", "rnj-1:8b", "qwen3-coder:480b"})
	if err != nil {
		t.Fatalf("SetFavoriteModels() error = %v", err)
	}

	want := []string{"qwen3-coder:480b", "rnj-1:8b"}
	if !reflect.DeepEqual(cfg.Preferences.FavoriteModels, want) {
		t.Fatalf("FavoriteModels = %#v, want %#v", cfg.Preferences.FavoriteModels, want)
	}
	persisted := readConfigFixture(t, path)
	if !reflect.DeepEqual(persisted.Preferences.FavoriteModels, want) {
		t.Fatalf("persisted FavoriteModels = %#v, want %#v", persisted.Preferences.FavoriteModels, want)
	}
	if persisted.ActiveProvider != "openai" || len(persisted.Providers) != 1 {
		t.Fatalf("provider config was not preserved: %#v", persisted)
	}
}

func TestSetThemePersistsUserPreference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
		},
	}, 0o600)

	cfg, err := SetTheme(path, "  dracula  ")
	if err != nil {
		t.Fatalf("SetTheme() error = %v", err)
	}
	if cfg.Preferences.Theme != "dracula" {
		t.Fatalf("Theme = %q, want dracula (trimmed)", cfg.Preferences.Theme)
	}
	persisted := readConfigFixture(t, path)
	if persisted.Preferences.Theme != "dracula" {
		t.Fatalf("persisted Theme = %q, want dracula", persisted.Preferences.Theme)
	}
	if persisted.ActiveProvider != "openai" || len(persisted.Providers) != 1 {
		t.Fatalf("provider config was not preserved by SetTheme: %#v", persisted)
	}

	// A blank value clears the stored preference.
	if cfg, err = SetTheme(path, ""); err != nil {
		t.Fatalf("SetTheme(\"\") error = %v", err)
	}
	if cfg.Preferences.Theme != "" {
		t.Fatalf("SetTheme(\"\") should clear the theme, got %q", cfg.Preferences.Theme)
	}
}

func TestRecapsPreferenceRoundTrips(t *testing.T) {
	// Default (unset) is ON.
	if !(PreferencesConfig{}).RecapsEnabled() {
		t.Error("unset recaps should default to ON")
	}

	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{ActiveProvider: "openai"}, 0o600)

	// Persist OFF, then read it back.
	cfg, err := SetRecapsEnabled(path, false)
	if err != nil {
		t.Fatalf("SetRecapsEnabled(false) error = %v", err)
	}
	if cfg.Preferences.RecapsEnabled() {
		t.Error("after SetRecapsEnabled(false), RecapsEnabled() should be false")
	}
	persisted := readConfigFixture(t, path)
	if persisted.Preferences.Recaps == nil || *persisted.Preferences.Recaps {
		t.Errorf("persisted recaps should be explicit false, got %v", persisted.Preferences.Recaps)
	}
	if persisted.ActiveProvider != "openai" {
		t.Errorf("unrelated config must be preserved, got %q", persisted.ActiveProvider)
	}

	// Flip back ON — the write must succeed and persist an explicit true.
	cfg, err = SetRecapsEnabled(path, true)
	if err != nil {
		t.Fatalf("SetRecapsEnabled(true) error = %v", err)
	}
	if !cfg.Preferences.RecapsEnabled() {
		t.Error("after SetRecapsEnabled(true), RecapsEnabled() should be true")
	}
	if reread := readConfigFixture(t, path); reread.Preferences.Recaps == nil || !*reread.Preferences.Recaps {
		t.Errorf("re-enable should persist an explicit true, got %v", reread.Preferences.Recaps)
	}
}

func TestSetFavoriteModelsCreatesMissingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero", "config.json")

	cfg, err := SetFavoriteModels(path, []string{"glm-5.1"})
	if err != nil {
		t.Fatalf("SetFavoriteModels() error = %v", err)
	}

	if !reflect.DeepEqual(cfg.Preferences.FavoriteModels, []string{"glm-5.1"}) {
		t.Fatalf("FavoriteModels = %#v, want glm-5.1", cfg.Preferences.FavoriteModels)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to be created: %v", err)
	}
}

func writeConfigFixture(t *testing.T, path string, cfg FileConfig, mode fs.FileMode) []byte {
	t.Helper()

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return data
}

func readConfigFixture(t *testing.T, path string) FileConfig {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg
}

func TestEnsureCatalogProviderCreatesProfileWithoutStealingActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "opengateway",
		Providers: []ProviderProfile{
			{
				Name:         "opengateway",
				ProviderKind: ProviderKindOpenAICompatible,
				BaseURL:      "https://gateway.example.com/v1",
				APIKeyStored: true,
				Model:        "some-model",
			},
		},
	}, 0o600)

	ensured, err := EnsureCatalogProvider(path, "chatgpt")
	if err != nil {
		t.Fatalf("EnsureCatalogProvider: %v", err)
	}
	if !ensured.Created {
		t.Fatalf("expected profile to be created")
	}
	if ensured.Name != "chatgpt" {
		t.Fatalf("expected profile name chatgpt, got %q", ensured.Name)
	}
	if ensured.Active != "opengateway" {
		t.Fatalf("active provider must stay opengateway, got %q", ensured.Active)
	}

	cfg := readConfigFixture(t, path)
	if cfg.ActiveProvider != "opengateway" {
		t.Fatalf("persisted active provider changed to %q", cfg.ActiveProvider)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(cfg.Providers))
	}
	chatgpt := cfg.Providers[1]
	if chatgpt.Name != "chatgpt" || chatgpt.CatalogID != "chatgpt" {
		t.Fatalf("unexpected created profile: %+v", chatgpt)
	}
	if chatgpt.Model == "" || chatgpt.BaseURL == "" {
		t.Fatalf("created profile must carry catalog defaults, got %+v", chatgpt)
	}
	if chatgpt.APIKey != "" || chatgpt.APIKeyStored {
		t.Fatalf("OAuth-created profile must stay keyless, got %+v", chatgpt)
	}
}

func TestEnsureCatalogProviderLeavesExistingProfileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	original := FileConfig{
		ActiveProvider: "opengateway",
		Providers: []ProviderProfile{
			{Name: "opengateway", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://gateway.example.com/v1", Model: "some-model"},
			// Renamed profile that already serves the chatgpt catalog entry.
			{Name: "codex", CatalogID: "chatgpt", Model: "gpt-5.5"},
		},
	}
	data := writeConfigFixture(t, path, original, 0o600)

	ensured, err := EnsureCatalogProvider(path, "chatgpt")
	if err != nil {
		t.Fatalf("EnsureCatalogProvider: %v", err)
	}
	if ensured.Created {
		t.Fatalf("existing profile must not be recreated")
	}
	if ensured.Name != "codex" {
		t.Fatalf("expected existing profile name codex, got %q", ensured.Name)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(data) {
		t.Fatalf("config rewritten for a no-op ensure:\nbefore: %s\nafter: %s", data, after)
	}
}

func TestEnsureCatalogProviderActivatesOnEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")

	ensured, err := EnsureCatalogProvider(path, "chatgpt")
	if err != nil {
		t.Fatalf("EnsureCatalogProvider: %v", err)
	}
	if !ensured.Created {
		t.Fatalf("expected profile to be created")
	}
	if ensured.Active != "chatgpt" {
		t.Fatalf("blank active must adopt the new provider, got %q", ensured.Active)
	}
}

func TestEnsureCatalogProviderRejectsUnknownCatalogID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	if _, err := EnsureCatalogProvider(path, "no-such-provider"); err == nil {
		t.Fatalf("expected unknown catalog id to error")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("config must not be written for an unknown catalog id")
	}
}

func TestRemoveProviderDeletesAndHandsOffActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "beta",
		Providers: []ProviderProfile{
			{Name: "alpha", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1"},
			{Name: "beta", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://b.example.com/v1", Model: "m2"},
		},
	}, 0o600)

	cfg, err := RemoveProvider(path, " BETA ")
	if err != nil {
		t.Fatalf("RemoveProvider() error = %v", err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "alpha" {
		t.Fatalf("expected only alpha to remain, got %+v", cfg.Providers)
	}
	if cfg.ActiveProvider != "alpha" {
		t.Fatalf("active must hand off to the first remaining provider, got %q", cfg.ActiveProvider)
	}

	persisted := readConfigFixture(t, path)
	if len(persisted.Providers) != 1 || persisted.ActiveProvider != "alpha" {
		t.Fatalf("persisted config wrong: %+v", persisted)
	}

	// Removing the last provider clears the active pointer entirely.
	cfg, err = RemoveProvider(path, "alpha")
	if err != nil {
		t.Fatalf("RemoveProvider(last) error = %v", err)
	}
	if len(cfg.Providers) != 0 || cfg.ActiveProvider != "" {
		t.Fatalf("expected empty providers and no active, got %+v", cfg)
	}
}

func TestRemoveProviderKeepsActiveWhenOtherRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "alpha",
		Providers: []ProviderProfile{
			{Name: "alpha", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1"},
			{Name: "beta", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://b.example.com/v1", Model: "m2"},
		},
	}, 0o600)

	cfg, err := RemoveProvider(path, "beta")
	if err != nil {
		t.Fatalf("RemoveProvider() error = %v", err)
	}
	if cfg.ActiveProvider != "alpha" {
		t.Fatalf("active provider must be untouched, got %q", cfg.ActiveProvider)
	}
}

func TestRemoveProviderRejectsUnknownWithoutRewriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "alpha",
		Providers:      []ProviderProfile{{Name: "alpha", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1"}},
	}, 0o600)

	if _, err := RemoveProvider(path, "ghost"); err == nil {
		t.Fatal("RemoveProvider() error = nil, want not-found error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config was rewritten for unknown provider")
	}
}

func TestRenameProviderFollowsActiveAndMigratesStoredKey(t *testing.T) {
	dir := t.TempDir()
	// Force the file credential backend so the test never touches the real OS
	// keyring regardless of platform.
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "oldname",
		Providers: []ProviderProfile{
			{Name: "oldname", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", APIKeyStored: true, Model: "m1"},
			{Name: "other", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://b.example.com/v1", Model: "m2"},
		},
	}, 0o600)
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("oldname", "sk-secret"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	cfg, err := RenameProvider(path, "oldname", "newname")
	if err != nil {
		t.Fatalf("RenameProvider() error = %v", err)
	}
	if cfg.ActiveProvider != "newname" {
		t.Fatalf("active must follow the rename, got %q", cfg.ActiveProvider)
	}
	if cfg.Providers[0].Name != "newname" || !cfg.Providers[0].APIKeyStored {
		t.Fatalf("renamed profile wrong: %+v", cfg.Providers[0])
	}
	if cfg.Providers[1].Name != "other" {
		t.Fatalf("unrelated profile changed: %+v", cfg.Providers[1])
	}

	key, ok, err := store.Get("newname")
	if err != nil || !ok || key != "sk-secret" {
		t.Fatalf("stored key must migrate to the new name, got key=%q ok=%v err=%v", key, ok, err)
	}
	if _, ok, _ := store.Get("oldname"); ok {
		t.Fatalf("old credential entry must be deleted after migration")
	}
}

func TestRenameProviderRejectsCollisionAndUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "alpha",
		Providers: []ProviderProfile{
			{Name: "alpha", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1"},
			{Name: "beta", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://b.example.com/v1", Model: "m2"},
		},
	}, 0o600)

	if _, err := RenameProvider(path, "alpha", "BETA"); err == nil {
		t.Fatal("rename onto an existing name must fail")
	}
	if _, err := RenameProvider(path, "ghost", "gamma"); err == nil {
		t.Fatal("renaming an unknown provider must fail")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config was rewritten by a rejected rename")
	}
}
