package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/providercatalog"
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

// EnsuredProvider reports the outcome of EnsureCatalogProvider: the profile name
// that serves the catalog entry, whether it was newly created, and which provider
// is active after the call (unchanged unless it was blank).
type EnsuredProvider struct {
	Name    string
	Created bool
	Active  string
}

// EnsureCatalogProvider guarantees a provider profile exists in the config at
// path for the given catalog entry. OAuth login flows call this right after
// storing a token: a login is only reachable from the provider list and
// `zero providers use` when a profile exists, but a login must never replace or
// deactivate the user's current active provider — so an existing profile whose
// Name or CatalogID already matches is left completely untouched (its name,
// credentials, and model are the user's), and a created profile is NOT marked
// active unless no provider was active at all.
func EnsureCatalogProvider(path string, catalogID string) (EnsuredProvider, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return EnsuredProvider{}, fmt.Errorf("config path is required")
	}
	descriptor, err := providercatalog.Require(catalogID)
	if err != nil {
		return EnsuredProvider{}, err
	}

	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return EnsuredProvider{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return EnsuredProvider{}, fmt.Errorf("read config %s: %w", path, err)
	}
	for _, provider := range cfg.Providers {
		if strings.EqualFold(strings.TrimSpace(provider.CatalogID), descriptor.ID) ||
			strings.EqualFold(strings.TrimSpace(provider.Name), descriptor.ID) {
			return EnsuredProvider{Name: provider.Name, Active: cfg.ActiveProvider}, nil
		}
	}

	profile := ProviderProfile{
		Name:         descriptor.ID,
		ProviderKind: providerKindForCatalogTransport(descriptor.Transport),
		CatalogID:    descriptor.ID,
		BaseURL:      descriptor.DefaultBaseURL,
		Model:        descriptor.DefaultModel,
	}
	written, err := UpsertProvider(path, profile, false)
	if err != nil {
		return EnsuredProvider{}, err
	}
	return EnsuredProvider{Name: profile.Name, Created: true, Active: written.ActiveProvider}, nil
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

// RemoveProvider deletes the named provider profile from the config at path.
// When the removed profile was active, activeProvider hands off to the first
// remaining provider (or clears when none remain) so the config never points at
// a profile that no longer exists. The caller owns cleaning up the credential
// store entry — config stays pure of secret I/O on the read path, and remove
// keeps that symmetry by only touching config.json.
func RemoveProvider(path string, name string) (FileConfig, error) {
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

	index := -1
	for i, provider := range cfg.Providers {
		if strings.EqualFold(strings.TrimSpace(provider.Name), name) {
			index = i
			break
		}
	}
	if index < 0 {
		return FileConfig{}, fmt.Errorf("provider %q not found", name)
	}
	removed := cfg.Providers[index]
	cfg.Providers = append(cfg.Providers[:index], cfg.Providers[index+1:]...)
	if strings.EqualFold(strings.TrimSpace(cfg.ActiveProvider), strings.TrimSpace(removed.Name)) {
		cfg.ActiveProvider = ""
		if len(cfg.Providers) > 0 {
			cfg.ActiveProvider = cfg.Providers[0].Name
		}
	}
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// RenameProvider renames a provider profile, keeping everything keyed by the
// profile name consistent: the activeProvider pointer follows the rename, and a
// key in the encrypted credential store (APIKeyStored) is migrated to the new
// name BEFORE the config is rewritten — the store write must succeed first so a
// failed migration never strands the config pointing at a key that no longer
// resolves. OAuth tokens are deliberately not migrated: the runtime's login
// candidates fall back to the profile's CatalogID, which every OAuth-capable
// catalog profile carries, so a rename keeps the login reachable.
func RenameProvider(path string, oldName string, newName string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return FileConfig{}, fmt.Errorf("provider names are required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}

	index := -1
	for i, provider := range cfg.Providers {
		providerName := strings.TrimSpace(provider.Name)
		if strings.EqualFold(providerName, oldName) {
			index = i
			continue
		}
		if strings.EqualFold(providerName, newName) {
			return FileConfig{}, fmt.Errorf("provider %q already exists", newName)
		}
	}
	if index < 0 {
		return FileConfig{}, fmt.Errorf("provider %q not found", oldName)
	}
	if strings.EqualFold(oldName, newName) && cfg.Providers[index].Name == newName {
		return cfg, nil
	}

	if cfg.Providers[index].APIKeyStored {
		if err := migrateStoredProviderKey(path, cfg.Providers[index].Name, newName); err != nil {
			return FileConfig{}, fmt.Errorf("migrate stored key for %q: %w", oldName, err)
		}
	}
	if strings.EqualFold(strings.TrimSpace(cfg.ActiveProvider), strings.TrimSpace(cfg.Providers[index].Name)) {
		cfg.ActiveProvider = newName
	}
	cfg.Providers[index].Name = newName
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// migrateStoredProviderKey moves a credential-store entry to a new provider
// name: write-new-then-delete-old, so an interruption can leave a duplicate but
// never a missing key. A missing source entry is a no-op (the marker may be
// stale); only a failed WRITE aborts the rename.
func migrateStoredProviderKey(configPath string, oldName string, newName string) error {
	store, err := ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		return err
	}
	key, ok, err := store.Get(oldName)
	if err != nil {
		return err
	}
	if !ok || strings.TrimSpace(key) == "" {
		return nil
	}
	if err := store.Set(newName, key); err != nil {
		return err
	}
	_, _ = store.Delete(oldName)
	return nil
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
