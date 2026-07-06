package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// CleanupStaleFavorites removes favorite model entries from the user and
// project configs that do not reference a provider/model available from the
// global user config.
//
// The project config path is optional (empty = no project config). Returns the
// number of entries removed; safe to run on every startup (idempotent).
func CleanupStaleFavorites(userConfigPath, projectConfigPath string) (int, error) {
	inventory, err := favoriteModelInventoryFromUserConfig(userConfigPath)
	if err != nil {
		return 0, err
	}
	return cleanupFavoritesForConfigPaths(inventory, userConfigPath, projectConfigPath)
}

func cleanupFavoritesForConfigPaths(inventory favoriteModelInventory, paths ...string) (int, error) {
	totalRemoved := 0
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		removed, err := cleanupFavoritesFile(path, inventory)
		totalRemoved += removed
		if err != nil {
			return totalRemoved, err
		}
	}
	return totalRemoved, nil
}

func cleanupFavoritesFile(configPath string, inventory favoriteModelInventory) (int, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return 0, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read config %s: %w", configPath, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("invalid config JSON %s: %w", configPath, err)
	}

	if len(cfg.Preferences.FavoriteModels) == 0 {
		return 0, nil
	}

	cleaned := make([]string, 0, len(cfg.Preferences.FavoriteModels))
	removed := 0
	for _, entry := range cfg.Preferences.FavoriteModels {
		entry = strings.TrimSpace(entry)
		if !inventory.validFavoriteModelRef(entry) {
			removed++
			continue
		}
		cleaned = append(cleaned, entry)
	}

	if removed == 0 {
		return 0, nil
	}

	cfg.Preferences.FavoriteModels = cleaned
	if err := writeConfigFile(configPath, cfg); err != nil {
		return removed, fmt.Errorf("rewrite config after cleanup %s: %w", configPath, err)
	}

	return removed, nil
}

type favoriteModelInventory struct {
	enforce   bool
	providers map[string]map[string]bool
}

func favoriteModelInventoryFromUserConfig(userConfigPath string) (favoriteModelInventory, error) {
	userConfigPath = strings.TrimSpace(userConfigPath)
	if userConfigPath == "" {
		return favoriteModelInventory{}, nil
	}
	data, err := os.ReadFile(userConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return favoriteModelInventory{}, nil
		}
		return favoriteModelInventory{}, fmt.Errorf("read config %s: %w", userConfigPath, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return favoriteModelInventory{}, fmt.Errorf("invalid config JSON %s: %w", userConfigPath, err)
	}
	return newFavoriteModelInventory(cfg), nil
}

func newFavoriteModelInventory(cfg FileConfig) favoriteModelInventory {
	inventory := favoriteModelInventory{
		enforce:   true,
		providers: map[string]map[string]bool{},
	}
	for _, provider := range cfg.Providers {
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			continue
		}
		var models map[string]bool
		if len(provider.Models) > 0 {
			models = map[string]bool{}
			for _, model := range provider.Models {
				if id := strings.TrimSpace(model.ID); id != "" {
					models[strings.ToLower(id)] = true
				}
			}
		}
		inventory.providers[strings.ToLower(name)] = models
	}
	return inventory
}

func (inventory favoriteModelInventory) validFavoriteModelRef(entry string) bool {
	if strings.ContainsAny(entry, " \t\r\n") {
		return false
	}
	provider, model, ok := strings.Cut(entry, "/")
	if !ok || provider == "" || model == "" {
		return false
	}
	if strings.Contains(model, "/") {
		return false
	}
	if !inventory.enforce {
		return true
	}
	models, ok := inventory.providers[strings.ToLower(provider)]
	if !ok {
		return false
	}
	if models == nil {
		return true
	}
	return models[strings.ToLower(model)]
}
