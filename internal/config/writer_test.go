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
