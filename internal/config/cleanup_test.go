package config

import (
	"path/filepath"
	"testing"
)

func TestCleanupInvalidFavorites_RemovesInvalidFormatFromAllConfigPaths(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.json")
	projectPath := filepath.Join(dir, "project.json")

	writeConfigFixture(t, userPath, FileConfig{
		Preferences: PreferencesConfig{
			FavoriteModels: []string{
				"openai/gpt-4.1",
				"unknown/model",
				"bare-model",
				"openai/",
				"/gpt-4.1",
				"openai/gpt-4.1/extra",
				" anthropic/claude-sonnet-4 ",
				"open ai/gpt-4.1",
			},
		},
	}, 0o600)

	writeConfigFixture(t, projectPath, FileConfig{
		Preferences: PreferencesConfig{
			FavoriteModels: []string{
				"project/sonnet",
				"",
				"project /sonnet",
				"local/qwen3",
			},
		},
	}, 0o600)

	removed, err := CleanupInvalidFavorites(userPath, projectPath)
	if err != nil {
		t.Fatalf("CleanupInvalidFavorites() error = %v", err)
	}
	if removed != 7 {
		t.Fatalf("removed = %d, want 7", removed)
	}

	userCfg := readConfigFixture(t, userPath)
	wantUser := []string{"anthropic/claude-sonnet-4", "openai/gpt-4.1", "unknown/model"}
	if !sameStrings(userCfg.Preferences.FavoriteModels, wantUser) {
		t.Fatalf("user FavoriteModels = %#v, want %#v", userCfg.Preferences.FavoriteModels, wantUser)
	}

	projectCfg := readConfigFixture(t, projectPath)
	wantProject := []string{"local/qwen3", "project/sonnet"}
	if !sameStrings(projectCfg.Preferences.FavoriteModels, wantProject) {
		t.Fatalf("project FavoriteModels = %#v, want %#v", projectCfg.Preferences.FavoriteModels, wantProject)
	}
}

func TestCleanupStaleFavorites_UsesFormatOnly(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "zero.json")

	writeConfigFixture(t, userPath, FileConfig{
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
		},
		Preferences: PreferencesConfig{
			FavoriteModels: []string{
				"openai/gpt-4.1",
				"stale-provider/qwen3-70b",
				"bare-model",
			},
		},
	}, 0o600)

	removed, err := CleanupStaleFavorites(userPath, "")
	if err != nil {
		t.Fatalf("CleanupStaleFavorites() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	cfg := readConfigFixture(t, userPath)
	want := []string{"openai/gpt-4.1", "stale-provider/qwen3-70b"}
	if !sameStrings(cfg.Preferences.FavoriteModels, want) {
		t.Fatalf("FavoriteModels = %#v, want %#v", cfg.Preferences.FavoriteModels, want)
	}
}

func TestCleanupFavoritesFile_RewritesPassedConfigPath(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.json")
	secondPath := filepath.Join(dir, "second.json")

	writeConfigFixture(t, firstPath, FileConfig{
		Preferences: PreferencesConfig{
			FavoriteModels: []string{"openai/gpt-4.1", "bare-model"},
		},
	}, 0o600)
	writeConfigFixture(t, secondPath, FileConfig{
		Preferences: PreferencesConfig{
			FavoriteModels: []string{"anthropic/claude-sonnet-4", "bare-model"},
		},
	}, 0o600)

	removed, err := cleanupFavoritesFile(secondPath)
	if err != nil {
		t.Fatalf("cleanupFavoritesFile() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	first := readConfigFixture(t, firstPath)
	if want := []string{"openai/gpt-4.1", "bare-model"}; !sameStrings(first.Preferences.FavoriteModels, want) {
		t.Fatalf("first FavoriteModels = %#v, want %#v", first.Preferences.FavoriteModels, want)
	}

	second := readConfigFixture(t, secondPath)
	if want := []string{"anthropic/claude-sonnet-4"}; !sameStrings(second.Preferences.FavoriteModels, want) {
		t.Fatalf("second FavoriteModels = %#v, want %#v", second.Preferences.FavoriteModels, want)
	}
}

func TestCleanupFavorites_EmptyAndMissingPathsAreNoOp(t *testing.T) {
	dir := t.TempDir()
	nonexistent := filepath.Join(dir, "no-such-file.json")

	removed, err := CleanupInvalidFavorites("", nonexistent)
	if err != nil {
		t.Fatalf("CleanupInvalidFavorites() error = %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestCleanupFavorites_NoOpWhenNoFavorites(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "zero.json")

	writeConfigFixture(t, userPath, FileConfig{
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1"},
		},
	}, 0o600)

	removed, err := CleanupInvalidFavorites(userPath, "")
	if err != nil {
		t.Fatalf("CleanupInvalidFavorites() error = %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestCleanupFavorites_PreservesOtherConfig(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "zero.json")

	writeConfigFixture(t, userPath, FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{
			{Name: "openai", ProviderKind: ProviderKindOpenAI, Model: "gpt-4.1", APIKey: "sk-test"},
		},
		Preferences: PreferencesConfig{
			FavoriteModels: []string{"openai/gpt-4.1", "bare-model"},
		},
		MaxTurns: 42,
		MCP: MCPConfig{
			Servers: map[string]MCPServerConfig{
				"filesystem": {Type: "stdio", Command: "npx"},
			},
		},
	}, 0o600)

	removed, err := CleanupInvalidFavorites(userPath, "")
	if err != nil {
		t.Fatalf("CleanupInvalidFavorites() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	cfg := readConfigFixture(t, userPath)
	if cfg.ActiveProvider != "openai" {
		t.Fatalf("ActiveProvider = %q, want openai", cfg.ActiveProvider)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].APIKey != "sk-test" {
		t.Fatalf("providers were corrupted: %#v", cfg.Providers)
	}
	if cfg.MaxTurns != 42 {
		t.Fatalf("MaxTurns = %d, want 42", cfg.MaxTurns)
	}
	if len(cfg.MCP.Servers) != 1 || cfg.MCP.Servers["filesystem"].Command != "npx" {
		t.Fatalf("MCP was corrupted: %#v", cfg.MCP)
	}
}

// sameStrings compares two string slices ignoring order (set equality).
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}
