package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gitlawb/zero/internal/credstore"
)

// ProviderKeyStoreAt opens the encrypted credential store whose file backend lives
// in dir. The backend resolves keyring-first, then encrypted-file, with a plaintext
// opt-out via ZERO_CRED_STORAGE. The dir parameter exists so tests can point the file
// backend at a temp directory; production always uses the user config directory
// (ProviderKeyStore) because provider API keys are user-scoped by design — they are
// only ever captured under the user config, never project config (a cloned repo must
// not carry keys), so runtime lookups deliberately use the user store regardless of
// where a provider profile was resolved from.
func ProviderKeyStoreAt(dir string) (*credstore.Store, error) {
	return credstore.New(credstore.Options{Dir: dir})
}

// ProviderKeyStore opens the credential store beside the default user config.
func ProviderKeyStore() (*credstore.Store, error) {
	configPath, err := DefaultUserConfigPath()
	if err != nil {
		return nil, err
	}
	return ProviderKeyStoreAt(filepath.Dir(configPath))
}

// APIKeyGetter is the read surface of the credential store; *credstore.Store
// satisfies it. Kept here so callers can inject a fake in tests.
type APIKeyGetter interface {
	Get(provider string) (string, bool, error)
}

// APIKeySetter is the write surface of the credential store.
type APIKeySetter interface {
	Set(provider, key string) error
}

// SecureProviderProfile moves an inline APIKey on the profile into the credential
// store co-located with configPath, returning a profile with APIKeyStored set and
// APIKey cleared so the secret is never written to config.json. On any store error
// (e.g. no keyring) it returns the profile unchanged — the inline key persists and
// the startup migration retries later — so capturing a provider never fails on a
// flaky credential backend.
func SecureProviderProfile(profile ProviderProfile, configPath string) ProviderProfile {
	if strings.TrimSpace(profile.APIKey) == "" || strings.TrimSpace(profile.Name) == "" {
		return profile
	}
	store, err := ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		return profile
	}
	if err := store.Set(profile.Name, profile.APIKey); err != nil {
		return profile
	}
	secured := profile
	secured.APIKey = ""
	secured.APIKeyStored = true
	return secured
}

// ForgetProviderKey removes a provider's stored API key from the credential store,
// reporting whether one existed. Used by the lifecycle "remove key" / auth logout.
func ForgetProviderKey(provider string) (bool, error) {
	store, err := ProviderKeyStore()
	if err != nil {
		return false, err
	}
	return store.Delete(provider)
}

// ClearProviderKeyStored unsets the APIKeyStored marker for a provider in the
// config at path, so credential checks no longer claim a stored key after one is
// removed. No-op when path/provider is empty, the config is absent, or the marker
// is already unset.
func ClearProviderKeyStored(path, provider string) (bool, error) {
	path = strings.TrimSpace(path)
	provider = strings.TrimSpace(provider)
	if path == "" || provider == "" {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	changed := false
	for index := range cfg.Providers {
		if strings.EqualFold(strings.TrimSpace(cfg.Providers[index].Name), provider) && cfg.Providers[index].APIKeyStored {
			cfg.Providers[index].APIKeyStored = false
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, writeConfigFile(path, cfg)
}

// MigratePlaintextProviderKeys moves any inline plaintext API key in the config at
// path into the credential store, marking the profile APIKeyStored and stripping
// the inline secret — but ONLY after the store write succeeds, so a failed Set
// leaves the plaintext key in place and never strands a credential. Returns how
// many keys were migrated; a no-op (0, nil) when path is empty/absent or nothing
// needs migrating. Safe to run on every startup (idempotent).
func MigratePlaintextProviderKeys(path string, store APIKeySetter) (int, error) {
	path = strings.TrimSpace(path)
	if path == "" || store == nil {
		return 0, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	migrated := 0
	for index := range cfg.Providers {
		profile := &cfg.Providers[index]
		key := strings.TrimSpace(profile.APIKey)
		if key == "" || strings.TrimSpace(profile.Name) == "" {
			continue
		}
		if err := store.Set(profile.Name, key); err != nil {
			// Leave the plaintext key untouched; a failed Set must not strand it.
			continue
		}
		profile.APIKey = ""
		profile.APIKeyStored = true
		migrated++
	}
	if migrated == 0 {
		return 0, nil
	}
	if err := writeConfigFile(path, cfg); err != nil {
		return migrated, fmt.Errorf("rewrite config after migration %s: %w", path, err)
	}
	return migrated, nil
}

// ApplyStoredAPIKey fills profile.APIKey from the credential store when it is not
// already resolved — an inline config key or a resolved APIKeyEnv always wins, so
// this only supplies a key for providers whose secret lives in the store. A nil
// store, a miss, or a store error leaves the profile unchanged.
func ApplyStoredAPIKey(profile ProviderProfile, store APIKeyGetter) ProviderProfile {
	// Only load for profiles that opted into stored-key auth (APIKeyStored). Without
	// this gate a stale keyring/file entry could silently reactivate credentials for a
	// profile that no longer uses the store.
	if store == nil || !profile.APIKeyStored || strings.TrimSpace(profile.APIKey) != "" {
		return profile
	}
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		return profile
	}
	if key, ok, err := store.Get(name); err == nil && ok && strings.TrimSpace(key) != "" {
		profile.APIKey = key
	}
	return profile
}
