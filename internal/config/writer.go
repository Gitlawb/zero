package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func UpsertProvider(path string, profile ProviderProfile, setActive bool) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}

	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	mergeProvider(&cfg, profile)
	if setActive || strings.TrimSpace(cfg.ActiveProvider) == "" {
		cfg.ActiveProvider = profile.Name
	}

	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func SetActiveProvider(path string, name string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}

	for _, provider := range cfg.Providers {
		if strings.EqualFold(provider.Name, name) {
			cfg.ActiveProvider = provider.Name
			if err := writeConfigFile(path, cfg); err != nil {
				return FileConfig{}, err
			}
			return cfg, nil
		}
	}

	return FileConfig{}, fmt.Errorf("provider %q not found", name)
}

func SetProviderModel(path string, name string, model string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return FileConfig{}, fmt.Errorf("model is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}

	for index := range cfg.Providers {
		if strings.EqualFold(cfg.Providers[index].Name, name) {
			cfg.Providers[index].Model = model
			if err := writeConfigFile(path, cfg); err != nil {
				return FileConfig{}, err
			}
			return cfg, nil
		}
	}

	return FileConfig{}, fmt.Errorf("provider %q not found", name)
}

// SetProviderDiscoveredModels persists a fresh list of discovered models for a
// named provider. The merge preserves any existing APIModel overrides for ids
// that survive into the new set, so a manual per-model mapping is not wiped by
// a subsequent discovery refresh. Models whose id is absent from the new set
// are dropped. The resulting list is sorted by id.
func SetProviderDiscoveredModels(path string, name string, models []DiscoveredModel) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}

	for index := range cfg.Providers {
		if strings.EqualFold(cfg.Providers[index].Name, name) {
			// Build a lookup of existing models by id to preserve APIModel overrides.
			existing := map[string]string{}
			for _, m := range cfg.Providers[index].Models {
				if id := strings.TrimSpace(m.ID); id != "" {
					if m.APIModel != "" {
						existing[id] = m.APIModel
					}
				}
			}

			// Merge: keep existing APIModel for surviving ids, use fresh id order.
			merged := make([]DiscoveredModel, 0, len(models))
			for _, m := range models {
				id := strings.TrimSpace(m.ID)
				if id == "" {
					continue
				}
				dm := DiscoveredModel{ID: id}
				if override, ok := existing[id]; ok {
					dm.APIModel = override
				}
				merged = append(merged, dm)
			}

			// Drop duplicates (first occurrence wins) then sort by id.
			merged = dedupDiscoveredModels(merged)
			sort.Slice(merged, func(i, j int) bool {
				return merged[i].ID < merged[j].ID
			})

			cfg.Providers[index].Models = merged
			if err := writeConfigFile(path, cfg); err != nil {
				return FileConfig{}, err
			}
			return cfg, nil
		}
	}

	return FileConfig{}, fmt.Errorf("provider %q not found", name)
}

// IsAnthropicModelFunc is a function type used by PersistTwoProfiles to
// classify model IDs as Anthropic-compatible or OpenAI-compatible.
type IsAnthropicModelFunc func(modelID string) bool

// PersistTwoProfiles splits a list of discovered models by API transport
// (OpenAI-compatible vs Anthropic-compatible) and, when BOTH formats are
// present, persists each group to its own named profile. If all models
// share one format this is a no-op (the primary profile already covers them).
//
// The classifier parameter determines which models go to which profile;
// pass providermodeldiscovery.IsAnthropicModel for gateway providers.
func PersistTwoProfiles(path string, catalogID string, baseURL string, models []DiscoveredModel, apiKey string, apiKeyEnv string, isAnthropic IsAnthropicModelFunc) {
	var openAI, anthropic []DiscoveredModel
	for _, m := range models {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if isAnthropic != nil && isAnthropic(id) {
			anthropic = append(anthropic, m)
		} else {
			openAI = append(openAI, m)
		}
	}
	// Only split when models of both formats exist.
	if len(openAI) > 0 && len(anthropic) > 0 {
		persistProfileGroup(path, catalogID+"-openaisdk", catalogID, baseURL, ProviderKindOpenAICompatible, "chat-completions", openAI, apiKey, apiKeyEnv, true)
		persistProfileGroup(path, catalogID+"-anthropicsdk", catalogID, baseURL, ProviderKindAnthropicCompat, "messages", anthropic, apiKey, apiKeyEnv, false)
	}
}

func persistProfileGroup(path string, name string, catalogID string, baseURL string, kind ProviderKind, apiFormat string, models []DiscoveredModel, apiKey string, apiKeyEnv string, setActive bool) {
	if len(models) == 0 {
		return
	}
	profile := ProviderProfile{
		Name:         name,
		CatalogID:    catalogID,
		BaseURL:      baseURL,
		ProviderKind: kind,
		APIFormat:    apiFormat,
		Model:        models[0].ID,
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		profile.APIKey = key
	} else if env := strings.TrimSpace(apiKeyEnv); env != "" {
		profile.APIKeyEnv = env
	}
	profile = SecureProviderProfile(profile, path)
	if _, err := UpsertProvider(path, profile, setActive); err != nil {
		return
	}
	if _, err := SetProviderDiscoveredModels(path, name, models); err != nil {
		return
	}
}

// dedupDiscoveredModels returns a copy with duplicate IDs removed (first wins).
func dedupDiscoveredModels(models []DiscoveredModel) []DiscoveredModel {
	seen := map[string]bool{}
	out := make([]DiscoveredModel, 0, len(models))
	for _, m := range models {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	return out
}

func SetFavoriteModels(path string, models []string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}

	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg.Preferences.FavoriteModels = normalizeFavoriteModels(models)
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetRecapsEnabled persists the post-turn recap preference, mirroring
// SetFavoriteModels (read-modify-atomic-write).
func SetRecapsEnabled(path string, enabled bool) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	v := enabled
	cfg.Preferences.Recaps = &v
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetTheme persists the TUI theme preference, mirroring SetFavoriteModels
// (read-modify-atomic-write). A blank theme clears the stored preference.
func SetTheme(path string, theme string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg.Preferences.Theme = strings.TrimSpace(theme)
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func normalizeFavoriteModels(models []string) []string {
	seen := map[string]bool{}
	favorites := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		favorites = append(favorites, model)
	}
	sort.Strings(favorites)
	return favorites
}

func writeConfigFile(path string, cfg FileConfig) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create config directory %s: %w", dir, err)
		}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config JSON: %w", err)
	}
	data = append(data, '\n')
	// Write-to-temp + rename: an in-place write interrupted mid-way (crash,
	// disk full) would leave the user's only config truncated or corrupt.
	tmp, err := os.CreateTemp(dir, ".zero-config-*.tmp")
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure config permissions %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
