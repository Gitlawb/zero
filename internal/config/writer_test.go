package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
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

	cfg, err := SetActiveProvider(path, "  Anthropic  ")
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

func TestSetActiveProviderRequiresExactProviderIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "work",
		Providers: []ProviderProfile{
			{Name: "work", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
		},
	}, 0o600)

	_, err := SetActiveProvider(path, "WORK")
	if err == nil || !strings.Contains(err.Error(), `provider "WORK" not found`) {
		t.Fatalf("SetActiveProvider() error = %v, want exact-case not-found error", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("config was rewritten for case-variant provider\nbefore: %s\nafter: %s", before, after)
	}
}

func TestMarkProviderAPIKeyStoredRequiresExactProviderIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{{Name: "work", APIKeyEnv: "WORK_KEY"}}}, 0o600)
	if err := MarkProviderAPIKeyStored(path, "WORK"); err == nil || !strings.Contains(err.Error(), `provider "WORK" not found`) {
		t.Fatalf("MarkProviderAPIKeyStored() error = %v, want exact-case not-found", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("case-variant mark rewrote config")
	}
}

func TestProviderPersistedRequiresExactProviderIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{{Name: "work"}}}, 0o600)

	persisted, err := ProviderPersisted(path, "WORK")
	if err != nil {
		t.Fatalf("ProviderPersisted() error = %v", err)
	}
	if persisted {
		t.Fatal("ProviderPersisted() = true for case-variant identity, want false")
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

	cfg, err := SetProviderModel(path, " openai ", " gpt-4.1-mini ")
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

// Same scenario as RemoveProvider/RenameProvider: two rows differing only by
// case must not let SetProviderModel update the wrong one.
func TestSetProviderModelRequiresExactProviderIdentityAmongCaseVariants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "work",
		Providers: []ProviderProfile{
			{Name: "work", ProviderKind: ProviderKindOpenAICompatible, Model: "m1"},
			{Name: "WORK", ProviderKind: ProviderKindOpenAICompatible, Model: "m2"},
		},
	}, 0o600)

	_, err := SetProviderModel(path, "WORK", "m2-updated")
	assertAmbiguousConfigUnchanged(t, path, before, err, "work", "WORK")
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

func TestSetRecentModelsPersistsOrderDedupesAndCaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
		},
	}, 0o600)

	cfg, err := SetRecentModels(path, []RecentModelEntry{
		{Provider: " openrouter ", Model: " google/gemini-2.5-pro "},
		{Provider: "openrouter", Model: "minimax/minimax-m2.1"},
		{Provider: "openrouter", Model: "google/gemini-2.5-pro"}, // duplicate pair, older: dropped
		{Provider: "", Model: ""},                                // blank model: dropped
		{Provider: "openrouter", Model: "a"},
		{Provider: "openrouter", Model: "b"},
		{Provider: "openrouter", Model: "c"},
		{Provider: "openrouter", Model: "d"}, // beyond MaxRecentModels (5): dropped
	})
	if err != nil {
		t.Fatalf("SetRecentModels() error = %v", err)
	}

	want := []RecentModelEntry{
		{Provider: "openrouter", Model: "google/gemini-2.5-pro"},
		{Provider: "openrouter", Model: "minimax/minimax-m2.1"},
		{Provider: "openrouter", Model: "a"},
		{Provider: "openrouter", Model: "b"},
		{Provider: "openrouter", Model: "c"},
	}
	if !reflect.DeepEqual(cfg.Preferences.RecentModels, want) {
		t.Fatalf("RecentModels = %#v, want %#v", cfg.Preferences.RecentModels, want)
	}
	persisted := readConfigFixture(t, path)
	if !reflect.DeepEqual(persisted.Preferences.RecentModels, want) {
		t.Fatalf("persisted RecentModels = %#v, want %#v", persisted.Preferences.RecentModels, want)
	}
	if persisted.ActiveProvider != "openai" || len(persisted.Providers) != 1 {
		t.Fatalf("provider config was not preserved: %#v", persisted)
	}
}

// Two providers offering the same model id must both survive normalization —
// recent history de-duplicates by provider+model pair, not model id alone.
func TestSetRecentModelsDedupesByProviderAndModelPair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")

	cfg, err := SetRecentModels(path, []RecentModelEntry{
		{Provider: "provider-a", Model: "shared-model"},
		{Provider: "provider-b", Model: "shared-model"},
	})
	if err != nil {
		t.Fatalf("SetRecentModels() error = %v", err)
	}
	want := []RecentModelEntry{
		{Provider: "provider-a", Model: "shared-model"},
		{Provider: "provider-b", Model: "shared-model"},
	}
	if !reflect.DeepEqual(cfg.Preferences.RecentModels, want) {
		t.Fatalf("RecentModels = %#v, want both providers preserved: %#v", cfg.Preferences.RecentModels, want)
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

func TestEnsureCatalogProviderRequiresPositiveCatalogOwnership(t *testing.T) {
	for _, tc := range []struct {
		name      string
		catalogID string
	}{
		{name: "foreign catalog", catalogID: "custom-openai-compatible"},
		{name: "missing catalog", catalogID: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "zero.json")
			before := writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{{
				Name:         "OpenRouter",
				CatalogID:    tc.catalogID,
				ProviderKind: ProviderKindOpenAICompatible,
				BaseURL:      "https://corp.example/v1",
				Model:        "corp-model",
				APIKeyStored: true,
			}}}, 0o600)

			if _, err := EnsureCatalogProvider(path, "openrouter"); err == nil || !strings.Contains(err.Error(), "does not prove ownership") {
				t.Fatalf("EnsureCatalogProvider error = %v, want ownership rejection", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("foreign profile was rewritten:\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func TestEnsureCatalogProviderRejectsAmbiguousCatalogOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{
		{Name: "work-xai", CatalogID: "xai"},
		{Name: "personal-xai", CatalogID: "xai"},
	}}, 0o600)

	if _, err := EnsureCatalogProvider(path, "xai"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("EnsureCatalogProvider error = %v, want shared-catalog ambiguity", err)
	}
	if err := PreflightCatalogProviderLogin(path, "xai"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("PreflightCatalogProviderLogin error = %v, want the same ambiguity", err)
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

func TestMarkProviderAPIKeyStoredClearsInlineAndEnvKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "openrouter",
		Providers: []ProviderProfile{{
			Name:      "openrouter",
			CatalogID: "openrouter",
			APIKey:    "sk-inline",
			APIKeyEnv: "OPENROUTER_API_KEY",
			Model:     "openai/gpt-4.1",
		}},
	}, 0o600)

	if err := MarkProviderAPIKeyStored(path, "openrouter"); err != nil {
		t.Fatalf("MarkProviderAPIKeyStored: %v", err)
	}

	cfg := readConfigFixture(t, path)
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %#v", cfg.Providers)
	}
	profile := cfg.Providers[0]
	if !profile.APIKeyStored || profile.APIKey != "" || profile.APIKeyEnv != "" {
		t.Fatalf("provider credential fields = %#v", profile)
	}
	if profile.Model != "openai/gpt-4.1" || cfg.ActiveProvider != "openrouter" {
		t.Fatalf("unrelated config changed: %#v", cfg)
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

	cfg, err := RemoveProvider(path, " beta ")
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

func TestProviderMutatorsHandOffCaseVariantActiveProvider(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(string) (FileConfig, error)
		wantActive string
		wantName   string
	}{
		{name: "remove", mutate: func(path string) (FileConfig, error) { return RemoveProvider(path, "work") }},
		{name: "rename", mutate: func(path string) (FileConfig, error) { return RenameProvider(path, "work", "office") }, wantActive: "office", wantName: "office"},
		{name: "edit", mutate: func(path string) (FileConfig, error) {
			return EditProvider(path, ProviderEdit{Name: "work", NewName: "office", Model: "updated"})
		}, wantActive: "office", wantName: "office"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			writeConfigFixture(t, path, FileConfig{ActiveProvider: "WORK", Providers: []ProviderProfile{{Name: "work", Model: "old"}}}, 0o600)
			cfg, err := test.mutate(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ActiveProvider != test.wantActive {
				t.Fatalf("activeProvider = %q, want %q", cfg.ActiveProvider, test.wantActive)
			}
			if test.wantName == "" && len(cfg.Providers) != 0 {
				t.Fatalf("providers = %+v, want none", cfg.Providers)
			}
			if test.wantName != "" && (len(cfg.Providers) != 1 || cfg.Providers[0].Name != test.wantName) {
				t.Fatalf("providers = %+v, want canonical name %q", cfg.Providers, test.wantName)
			}
		})
	}
}

// UpsertProvider merges by exact name, so a config file can end up with two
// rows that differ only by case (e.g. one saved as "work", another later
// saved as "WORK"). RemoveProvider must delete the exact row the caller
// named, not whichever case-variant sorts first.
func TestRemoveProviderRequiresExactProviderIdentityAmongCaseVariants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "work",
		Providers: []ProviderProfile{
			{Name: "work", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1"},
			{Name: "WORK", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://b.example.com/v1", Model: "m2"},
		},
	}, 0o600)

	cfg, err := RemoveProvider(path, "WORK")
	if err != nil {
		t.Fatalf("exact removal should repair case duplicates: %v", err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "work" || cfg.ActiveProvider != "work" {
		t.Fatalf("repaired config = %+v", cfg)
	}
}

func TestRemoveProviderRejectsNonExactCaseDuplicateTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{{Name: "work"}, {Name: "WORK"}}}, 0o600)
	_, err := RemoveProvider(path, "WoRk")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v, want exact-target not-found error", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(after, before) {
		t.Fatalf("rejected removal rewrote config: readErr=%v", readErr)
	}
}

func TestRemoveProviderRejectsRepairThatRemainsAmbiguous(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{{Name: "work"}, {Name: "WORK"}, {Name: "Work"}}}, 0o600)
	_, err := RemoveProvider(path, "Work")
	if err == nil || !strings.Contains(err.Error(), "ambiguous persisted provider names") {
		t.Fatalf("error = %v, want resulting-config validation error", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(after, before) {
		t.Fatalf("invalid repair rewrote config: readErr=%v", readErr)
	}
}

func TestRemoveProviderKeepsExactActiveCaseVariant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "work",
		Providers:      []ProviderProfile{{Name: "alpha"}, {Name: "work"}, {Name: "WORK"}},
	}, 0o600)

	cfg, err := RemoveProvider(path, "WORK")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActiveProvider != "work" {
		t.Fatalf("activeProvider = %q, want exact surviving row work", cfg.ActiveProvider)
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

// Same scenario as RemoveProvider: two rows differing only by case must not
// let RenameProvider act on the wrong one.
func TestRenameProviderRequiresExactProviderIdentityAmongCaseVariants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "work",
		Providers: []ProviderProfile{
			{Name: "work", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1"},
			{Name: "WORK", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://b.example.com/v1", Model: "m2"},
		},
	}, 0o600)

	_, err := RenameProvider(path, "WORK", "renamed")
	assertAmbiguousConfigUnchanged(t, path, before, err, "work", "WORK")
}

func TestUpsertProviderPreservesStoredKeyMarkerOnExistingProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	// An env-keyed profile with NO stored-key marker — the shape a provider has
	// before its key is captured into the credential store.
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "groq",
		Providers: []ProviderProfile{
			{Name: "groq", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", APIKeyEnv: "GROQ_API_KEY", Model: "m1"},
		},
	}, 0o600)

	// The manager/setup edit paths persist a SecureProviderProfile-shaped
	// profile: key already in the store, marker set, inline key cleared.
	cfg, err := UpsertProvider(path, ProviderProfile{Name: "groq", APIKeyStored: true}, false)
	if err != nil {
		t.Fatalf("UpsertProvider() error = %v", err)
	}
	if !cfg.Providers[0].APIKeyStored {
		t.Fatalf("APIKeyStored marker must survive the merge, got %+v", cfg.Providers[0])
	}
	if cfg.Providers[0].APIKeyEnv != "GROQ_API_KEY" || cfg.Providers[0].BaseURL == "" {
		t.Fatalf("other fields must be preserved: %+v", cfg.Providers[0])
	}
	persisted := readConfigFixture(t, path)
	if !persisted.Providers[0].APIKeyStored {
		t.Fatalf("marker not persisted to disk: %+v", persisted.Providers[0])
	}
}

func TestSetProviderDescriptionSetsAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "alpha",
		Providers: []ProviderProfile{
			{Name: "alpha", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1", Description: "old text"},
		},
	}, 0o600)

	cfg, err := SetProviderDescription(path, " ALPHA ", "new text")
	if err != nil {
		t.Fatalf("SetProviderDescription() error = %v", err)
	}
	if cfg.Providers[0].Description != "new text" {
		t.Fatalf("description not set: %+v", cfg.Providers[0])
	}

	// Clearing must persist too — the reason this setter exists (UpsertProvider's
	// merge treats an empty description as "leave unchanged").
	cfg, err = SetProviderDescription(path, "alpha", "  ")
	if err != nil {
		t.Fatalf("SetProviderDescription(clear) error = %v", err)
	}
	if cfg.Providers[0].Description != "" {
		t.Fatalf("description not cleared: %+v", cfg.Providers[0])
	}
	persisted := readConfigFixture(t, path)
	if persisted.Providers[0].Description != "" {
		t.Fatalf("cleared description not persisted: %+v", persisted.Providers[0])
	}

	if _, err := SetProviderDescription(path, "ghost", "x"); err == nil {
		t.Fatal("unknown provider must error")
	}
}

func TestRenameProviderRollsBackKeyMigrationWhenConfigWriteFails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("uses chflags uchg to force the config write to fail; macOS only")
	}
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "oldname",
		Providers: []ProviderProfile{
			{Name: "oldname", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", APIKeyStored: true, Model: "m1"},
		},
	}, 0o600)
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("oldname", "sk-secret"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	// Immutable flag: temp-file creation and store writes in the directory keep
	// working, but the final rename over config.json fails — the exact window
	// where a migrated key would otherwise strand under the new name.
	if out, err := exec.Command("chflags", "uchg", path).CombinedOutput(); err != nil {
		t.Skipf("chflags uchg unavailable: %v (%s)", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("chflags", "nouchg", path).Run() })

	if _, err := RenameProvider(path, "oldname", "newname"); err == nil {
		t.Fatal("expected the config write to fail under the immutable flag")
	}

	key, ok, err := store.Get("oldname")
	if err != nil || !ok || key != "sk-secret" {
		t.Fatalf("key must be rolled back to the old name, got key=%q ok=%v err=%v", key, ok, err)
	}
	if _, ok, _ := store.Get("newname"); ok {
		t.Fatalf("rolled-back migration must not leave a key under the new name")
	}
}

// TestRenameProviderCaseOnlyKeepsStoredKey: the credential store normalizes
// names case-insensitively, so a case-only rename targets ONE store entry —
// migrating would Set and then Delete the same key, losing it (PR #560 review).
func TestRenameProviderCaseOnlyKeepsStoredKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "groq",
		Providers: []ProviderProfile{
			{Name: "groq", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", APIKeyStored: true, Model: "m1"},
		},
	}, 0o600)
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("groq", "sk-secret"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	cfg, err := RenameProvider(path, "groq", "Groq")
	if err != nil {
		t.Fatalf("RenameProvider() error = %v", err)
	}
	if cfg.Providers[0].Name != "Groq" || cfg.ActiveProvider != "Groq" {
		t.Fatalf("case-only rename must still apply to config: %+v", cfg)
	}
	// The store is case-insensitive: the key must remain retrievable under the
	// new casing (same entry), not deleted by a same-entry "migration".
	if key, ok, err := store.Get("Groq"); err != nil || !ok || key != "sk-secret" {
		t.Fatalf("stored key lost on case-only rename: key=%q ok=%v err=%v", key, ok, err)
	}
}

func TestEditProviderAppliesRenameFieldsAndDescriptionAtomically(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "groq",
		Providers: []ProviderProfile{
			{Name: "groq", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", APIKeyStored: true, Model: "m1", Description: "old text"},
			{Name: "other", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://o.example.com/v1", Model: "m2"},
		},
	}, 0o600)
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("groq", "sk-old"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	cfg, err := EditProvider(path, ProviderEdit{
		Name:        "groq",
		NewName:     "grok-main",
		Model:       "m1-pro",
		Description: "", // cleared — must persist verbatim
	})
	if err != nil {
		t.Fatalf("EditProvider() error = %v", err)
	}
	edited := cfg.Providers[0]
	if edited.Name != "grok-main" || edited.Model != "m1-pro" || edited.Description != "" {
		t.Fatalf("edit not applied: %+v", edited)
	}
	if edited.BaseURL != "https://api.groq.com/openai/v1" || !edited.APIKeyStored {
		t.Fatalf("untouched fields must survive: %+v", edited)
	}
	if cfg.ActiveProvider != "grok-main" {
		t.Fatalf("active must follow the rename, got %q", cfg.ActiveProvider)
	}
	if key, ok, _ := store.Get("grok-main"); !ok || key != "sk-old" {
		t.Fatalf("stored key must migrate with the rename, got %q ok=%v", key, ok)
	}
	if len(cfg.Providers) != 2 || cfg.Providers[1].Name != "other" {
		t.Fatalf("unrelated profile changed: %+v", cfg.Providers)
	}
}

func TestEditProviderRequiresExactProviderIdentityAmongCaseVariants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "work",
		Providers: []ProviderProfile{
			{Name: "WORK", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://upper.example.com/v1", Model: "upper"},
			{Name: "work", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://lower.example.com/v1", Model: "lower"},
		},
	}, 0o600)

	_, err := EditProvider(path, ProviderEdit{Name: "WORK", NewName: "renamed", Model: "updated"})
	assertAmbiguousConfigUnchanged(t, path, before, err, "WORK", "work")
}

func assertAmbiguousConfigUnchanged(t *testing.T, path string, before []byte, err error, first, second string) {
	t.Helper()
	want := fmt.Sprintf("ambiguous persisted provider names %q and %q differ only by case; rename or remove one row in config.json", first, second)
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("ambiguous mutation rewrote config\nbefore: %s\nafter: %s", before, after)
	}
}

// TestEditProviderCaseOnlyRenameUpdatesInPlace: the manager previously skipped
// RenameProvider on case-insensitively-equal names and fell into UpsertProvider,
// whose case-SENSITIVE merge appended a duplicate profile. EditProvider applies
// NewName to the exact current profile, so a case-only rename is an in-place
// update and the store entry (case-normalized) survives.
func TestEditProviderCaseOnlyRenameUpdatesInPlace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "groq",
		Providers: []ProviderProfile{
			{Name: "groq", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", APIKeyStored: true, Model: "m1"},
		},
	}, 0o600)
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("groq", "sk-secret"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	cfg, err := EditProvider(path, ProviderEdit{Name: "groq", NewName: "Groq"})
	if err != nil {
		t.Fatalf("EditProvider() error = %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("case-only rename must not duplicate the profile: %+v", cfg.Providers)
	}
	if cfg.Providers[0].Name != "Groq" || !cfg.Providers[0].APIKeyStored {
		t.Fatalf("in-place update wrong: %+v", cfg.Providers[0])
	}
	if cfg.Providers[0].BaseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("fields must survive a case-only rename: %+v", cfg.Providers[0])
	}
	if cfg.ActiveProvider != "Groq" {
		t.Fatalf("active must follow, got %q", cfg.ActiveProvider)
	}
	if key, ok, _ := store.Get("Groq"); !ok || key != "sk-secret" {
		t.Fatalf("stored key lost on case-only rename: %q ok=%v", key, ok)
	}
}

// TestEditProviderRenameMigratesFreshlyCapturedKey: replacing the key AND
// renaming in one edit — the caller captures under the CURRENT name and the
// rename migration moves it, so the new key lands under the new name.
func TestEditProviderRenameMigratesFreshlyCapturedKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "gw",
		Providers: []ProviderProfile{
			{Name: "gw", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://gw.example.com/v1", APIKeyEnv: "GW_KEY", Model: "m1"},
		},
	}, 0o600)
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	// The caller's contract: a replacement key is stored under the CURRENT name
	// before EditProvider runs (what SecureProviderProfile does).
	if err := store.Set("gw", "sk-new"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	cfg, err := EditProvider(path, ProviderEdit{Name: "gw", NewName: "gateway", APIKeyStored: true})
	if err != nil {
		t.Fatalf("EditProvider() error = %v", err)
	}
	if !cfg.Providers[0].APIKeyStored || cfg.Providers[0].Name != "gateway" {
		t.Fatalf("marker/rename wrong: %+v", cfg.Providers[0])
	}
	if key, ok, _ := store.Get("gateway"); !ok || key != "sk-new" {
		t.Fatalf("captured key must migrate to the new name, got %q ok=%v", key, ok)
	}
	if _, ok, _ := store.Get("gw"); ok {
		t.Fatalf("old entry must be cleaned up after migration")
	}
}

func TestEditProviderRejectsCollisionAndUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	before := writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "alpha",
		Providers: []ProviderProfile{
			{Name: "alpha", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1"},
			{Name: "beta", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://b.example.com/v1", Model: "m2"},
		},
	}, 0o600)

	if _, err := EditProvider(path, ProviderEdit{Name: "alpha", NewName: "BETA"}); err == nil {
		t.Fatal("rename onto an existing name must fail")
	}
	if _, err := EditProvider(path, ProviderEdit{Name: "ghost", Model: "x"}); err == nil {
		t.Fatal("editing an unknown provider must fail")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config was rewritten by a rejected edit")
	}
}

// TestValidatePersistedProviderNamesRejectsExactDuplicates covers jatmn's #725
// finding: the validator only rejected a repeated folded name when the
// SPELLINGS differed, so two rows literally named "work" passed. That breaks
// the same one-credential-per-folded-name invariant the case check protects —
// resolver merging coalesces the rows, and plaintext-key migration writes both
// values into one normalized credential-store entry, overwriting the first key.
func TestValidatePersistedProviderNamesRejectsExactDuplicates(t *testing.T) {
	for name, providers := range map[string][]ProviderProfile{
		"identical spellings": {{Name: "work"}, {Name: "work"}},
		"same after trimming": {{Name: "work"}, {Name: "  work  "}},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidatePersistedProviderNames(FileConfig{Providers: providers})
			if err == nil {
				t.Fatal("a repeated folded provider identity must be rejected")
			}
			if want := `duplicate persisted provider name "work"`; !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to contain %q", err, want)
			}
		})
	}
	if err := ValidatePersistedProviderNames(FileConfig{Providers: []ProviderProfile{{Name: "work"}, {Name: "fast"}}}); err != nil {
		t.Fatalf("distinct names must validate: %v", err)
	}
}

// TestAdoptPersistedCatalogProviderNameIgnoresCatalogSiblings covers jatmn's
// #725 finding: adoption followed PersistedProviderIdentity, which returned the
// first row sharing the catalog id. Several profiles may legitimately use one
// catalog provider, so adding the default "xai" profile silently retargeted an
// existing {name:"work-xai", catalogId:"xai"} row and would have overwritten its
// endpoint, model, and stored key.
func TestAdoptPersistedCatalogProviderNameIgnoresCatalogSiblings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFixture(t, path, FileConfig{
		Providers: []ProviderProfile{
			{Name: "work-xai", CatalogID: "xai", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://work.example.com/v1", Model: "m"},
		},
	}, 0o600)

	adopted, err := AdoptPersistedCatalogProviderName(path, ProviderProfile{Name: "xai", CatalogID: "xai"})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if adopted.Name != "xai" {
		t.Fatalf("Name = %q, want the default spelling kept — a catalog sibling is not the same profile", adopted.Name)
	}
}

// TestAdoptPersistedCatalogProviderNameFollowsCaseVariantOfTheDefaultName is the
// behaviour adoption exists for: a re-setup of "openrouter" against a row the
// user saved as "OpenRouter" must UPDATE that row instead of colliding with it.
func TestAdoptPersistedCatalogProviderNameFollowsCaseVariantOfTheDefaultName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFixture(t, path, FileConfig{
		Providers: []ProviderProfile{
			{Name: "OpenRouter", CatalogID: "openrouter", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://openrouter.ai/api/v1", Model: "m"},
		},
	}, 0o600)

	adopted, err := AdoptPersistedCatalogProviderName(path, ProviderProfile{Name: "openrouter", CatalogID: "openrouter"})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if adopted.Name != "OpenRouter" {
		t.Fatalf("Name = %q, want the persisted case variant adopted", adopted.Name)
	}
}

// TestAdoptPersistedCatalogProviderNameIgnoresForeignCatalogRow covers jatmn's
// #725 finding that a case-folded NAME match was taken as proof of ownership.
// A custom profile may legitimately be called "OpenRouter" while pointing at a
// different provider entirely; adopting it for `zero providers add openrouter`
// handed that row to UpsertProvider, whose merge overwrites the catalog id,
// endpoint, model and transport with OpenRouter's defaults while a stored-key
// marker survives the rewrite.
func TestAdoptPersistedCatalogProviderNameIgnoresForeignCatalogRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFixture(t, path, FileConfig{
		Providers: []ProviderProfile{
			{Name: "OpenRouter", CatalogID: "custom-openai-compatible", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://corp.example/v1", Model: "corp-model", APIKeyStored: true},
		},
	}, 0o600)

	adopted, err := AdoptPersistedCatalogProviderName(path, ProviderProfile{Name: "openrouter", CatalogID: "openrouter"})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if adopted.Name != "openrouter" {
		t.Fatalf("Name = %q, want the requested spelling kept — a differently-catalogued row is another provider", adopted.Name)
	}
	// And the write that follows must report the collision rather than rewriting
	// the custom row in place.
	if err := PreflightProviderWrite(path, adopted.Name); err == nil {
		t.Fatal("PreflightProviderWrite accepted a name that collides with the custom row; want a reported collision")
	}
}

// TestAdoptPersistedCatalogProviderNameIgnoresNameOnlyRow pins that a matching
// display name is not proof that a custom profile belongs to a catalog provider.
func TestAdoptPersistedCatalogProviderNameIgnoresNameOnlyRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFixture(t, path, FileConfig{
		Providers: []ProviderProfile{
			{Name: "OpenRouter", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://openrouter.ai/api/v1", Model: "m"},
		},
	}, 0o600)

	adopted, err := AdoptPersistedCatalogProviderName(path, ProviderProfile{Name: "openrouter", CatalogID: "openrouter"})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if adopted.Name != "openrouter" {
		t.Fatalf("Name = %q, want the requested spelling kept without positive catalog ownership", adopted.Name)
	}
	if err := PreflightProviderWrite(path, adopted.Name); err == nil {
		t.Fatal("PreflightProviderWrite accepted a name-only collision; want the custom row preserved")
	}
}

func TestAdoptPersistedCatalogProviderNameRejectsExactForeignOrNameOnlyRow(t *testing.T) {
	for _, tt := range []struct {
		name      string
		catalogID string
	}{
		{name: "foreign catalog", catalogID: "custom-openai-compatible"},
		{name: "missing catalog ownership"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			original := ProviderProfile{
				Name: "openrouter", CatalogID: tt.catalogID, ProviderKind: ProviderKindOpenAICompatible,
				BaseURL: "https://corp.example/v1", Model: "corp-model", APIKeyStored: true,
			}
			writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{original}}, 0o600)

			if _, err := AdoptPersistedCatalogProviderName(path, ProviderProfile{Name: "openrouter", CatalogID: "openrouter"}); err == nil || !strings.Contains(err.Error(), "does not prove ownership") {
				t.Fatalf("AdoptPersistedCatalogProviderName error = %v, want ownership rejection", err)
			}
			cfg, err := loadConfigFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Providers) != 1 || cfg.Providers[0].CatalogID != original.CatalogID || cfg.Providers[0].BaseURL != original.BaseURL || cfg.Providers[0].Model != original.Model || !cfg.Providers[0].APIKeyStored {
				t.Fatalf("foreign exact-name row changed: %+v", cfg.Providers)
			}
		})
	}
}

// TestPersistedProviderIdentityRulesMatchTheCredentialStore pins the identity
// contract this PR introduced across every persisted-config path at once.
// strings.EqualFold folds "s" and Unicode long-s "ſ" together while
// credstore.NormalizeProvider (the store's own rule, and the rule
// ValidatePersistedProviderNames enforces) keeps them apart, so the two
// spellings are separate profiles with separate secrets. Mixing the two
// comparisons made one profile's mutation reach the other's row: destructive
// logout expansion adopted the unrelated row, while ordinary writes rejected
// the pair as a collision.
func TestPersistedProviderIdentityRulesMatchTheCredentialStore(t *testing.T) {
	const longS = "ſ"
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFixture(t, path, FileConfig{
		Providers: []ProviderProfile{
			{Name: "s", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://s.example/v1", Model: "m", APIKeyStored: true},
		},
	}, 0o600)

	t.Run("identity resolution does not adopt the distinct spelling", func(t *testing.T) {
		if _, match, err := ResolvePersistedProviderIdentity(path, longS); err != nil || match != PersistedIdentityNone {
			t.Fatalf("ResolvePersistedProviderIdentity(%q) = %v, %v; want no match", longS, match, err)
		}
	})

	t.Run("a distinct spelling is writable, not a collision", func(t *testing.T) {
		if err := PreflightProviderWrite(path, longS); err != nil {
			t.Fatalf("PreflightProviderWrite(%q) = %v; want the distinct identity accepted", longS, err)
		}
		cfg, err := UpsertProvider(path, ProviderProfile{Name: longS, ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://long-s.example/v1", Model: "m"}, false)
		if err != nil {
			t.Fatalf("UpsertProvider(%q) = %v; want the distinct identity accepted", longS, err)
		}
		if len(cfg.Providers) != 2 {
			t.Fatalf("providers = %+v, want both distinct rows saved", cfg.Providers)
		}
	})

	t.Run("a case variant is still a collision", func(t *testing.T) {
		if err := PreflightProviderWrite(path, "S"); err == nil {
			t.Fatal("PreflightProviderWrite(\"S\") accepted a case variant of a saved row")
		}
	})
}

// TestResolvePersistedProviderIdentityPrefersNames covers jatmn's #725 finding
// that identity resolution took the first row matching EITHER field, so a
// catalog id on an earlier row outranked a later row with the exact name.
func TestResolvePersistedProviderIdentityPrefersNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigFixture(t, path, FileConfig{
		Providers: []ProviderProfile{
			{Name: "work-xai", CatalogID: "xai"},
			{Name: "xai", CatalogID: "xai"},
		},
	}, 0o600)

	t.Run("exact name beats an earlier catalog id", func(t *testing.T) {
		row, match, err := ResolvePersistedProviderIdentity(path, "xai")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if match != PersistedIdentityName || row.Name != "xai" {
			t.Fatalf("row = %q match = %v, want the exactly named row", row.Name, match)
		}
	})

	t.Run("a shared catalog id resolves to nothing", func(t *testing.T) {
		_, match, err := ResolvePersistedProviderIdentity(path, "XAI")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		// "XAI" folds to the "xai" row's NAME, so that wins; the point of the
		// exclusivity rule shows on a catalog id nothing is named after.
		if match != PersistedIdentityName {
			t.Fatalf("match = %v, want the case-variant name match", match)
		}
	})

	t.Run("unique catalog id still resolves", func(t *testing.T) {
		unique := filepath.Join(t.TempDir(), "config.json")
		writeConfigFixture(t, unique, FileConfig{
			Providers: []ProviderProfile{{Name: "my-router", CatalogID: "openrouter"}},
		}, 0o600)
		row, match, err := ResolvePersistedProviderIdentity(unique, "openrouter")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if match != PersistedIdentityCatalog || row.Name != "my-router" {
			t.Fatalf("row = %q match = %v, want the sole catalog owner", row.Name, match)
		}
	})

	t.Run("an ambiguous catalog id resolves to nothing", func(t *testing.T) {
		shared := filepath.Join(t.TempDir(), "config.json")
		writeConfigFixture(t, shared, FileConfig{
			Providers: []ProviderProfile{
				{Name: "work-xai", CatalogID: "xai"},
				{Name: "personal-xai", CatalogID: "xai"},
			},
		}, 0o600)
		if _, match, err := ResolvePersistedProviderIdentity(shared, "xai"); err != nil || match != PersistedIdentityNone {
			t.Fatalf("match = %v err = %v, want no guess at a shared catalog id", match, err)
		}
	})
}

// TestCatalogIdentityExclusive guards the rule credential cleanup depends on:
// a catalog id claimed by any other row is not the target profile's own key.
func TestCatalogIdentityExclusive(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "config.json")
	writeConfigFixture(t, shared, FileConfig{
		Providers: []ProviderProfile{
			{Name: "work-xai", CatalogID: "xai"},
			{Name: "xai", CatalogID: "xai"},
			{Name: "personal-xai", CatalogID: "xai"},
		},
	}, 0o600)
	if exclusive, err := CatalogIdentityExclusive(shared, "xai", "work-xai"); err != nil || exclusive {
		t.Fatalf("exclusive = %v err = %v, want false for a catalog id three rows claim", exclusive, err)
	}

	sole := filepath.Join(t.TempDir(), "config.json")
	writeConfigFixture(t, sole, FileConfig{
		Providers: []ProviderProfile{
			{Name: "my-router", CatalogID: "openrouter"},
			{Name: "work", CatalogID: "xai"},
		},
	}, 0o600)
	if exclusive, err := CatalogIdentityExclusive(sole, "openrouter", "my-router"); err != nil || !exclusive {
		t.Fatalf("exclusive = %v err = %v, want true when only the owner claims the id", exclusive, err)
	}
}

func TestProviderCredentialCandidates(t *testing.T) {
	t.Run("includes the persisted name and exclusive catalog alias", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{
			{Name: "my-router", CatalogID: "openrouter"},
		}}, 0o600)
		candidates, canonical, err := ProviderCredentialCandidates(path, "MY-ROUTER")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"MY-ROUTER", "my-router", "openrouter"}
		if !slices.Equal(candidates, want) || canonical != "my-router" {
			t.Fatalf("candidates = %q canonical = %q, want %q and my-router", candidates, canonical, want)
		}
	})

	t.Run("does not claim a shared catalog alias", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		writeConfigFixture(t, path, FileConfig{Providers: []ProviderProfile{
			{Name: "work-xai", CatalogID: "xai"},
			{Name: "personal-xai", CatalogID: "xai"},
		}}, 0o600)
		candidates, canonical, err := ProviderCredentialCandidates(path, "work-xai")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"work-xai"}; !slices.Equal(candidates, want) || canonical != "work-xai" {
			t.Fatalf("candidates = %q canonical = %q, want %q and work-xai", candidates, canonical, want)
		}
		candidates, _, err = ProviderCredentialCandidates(path, "xai")
		if err == nil || len(candidates) != 0 {
			t.Fatalf("ambiguous alias candidates = %q err = %v, want no destructive candidates and an error", candidates, err)
		}
	})
}
