package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gitlawb/zero/internal/credstore"
	"github.com/Gitlawb/zero/internal/lockutil"
)

var providerWriteLockTimeout = 5 * time.Second
var providerWriteLockSeq atomic.Uint64

type ProviderCommit struct {
	Profile       ProviderProfile
	SetActive     bool
	KeepStoredKey bool
}

type ProviderCommitResult struct {
	Config    FileConfig
	Persisted ProviderProfile
}

// CommitProviderProfile serializes validation, credential capture, and config
// publication so a rejected concurrent case-variant write cannot replace the
// winning provider's credential.
func CommitProviderProfile(path string, commit ProviderCommit) (result ProviderCommitResult, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ProviderCommitResult{}, fmt.Errorf("config path is required")
	}
	release, err := lockProviderWrite(path)
	if err != nil {
		return ProviderCommitResult{}, err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			result = ProviderCommitResult{}
			err = errors.Join(err, releaseErr)
		}
	}()

	cfg := FileConfig{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return ProviderCommitResult{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(readErr) {
		return ProviderCommitResult{}, fmt.Errorf("read config %s: %w", path, readErr)
	}
	if err := ValidatePersistedProviderNames(cfg); err != nil {
		return ProviderCommitResult{}, err
	}

	persisted := commit.Profile
	var store *credstore.Store
	var previous string
	var previousPresent bool
	var written string
	captured := !commit.KeepStoredKey && strings.TrimSpace(persisted.APIKey) != ""
	if captured {
		store, err = ProviderKeyStoreAt(filepath.Dir(path))
		if err != nil {
			return ProviderCommitResult{}, err
		}
		previous, previousPresent, err = store.Get(persisted.Name)
		if err != nil {
			return ProviderCommitResult{}, err
		}
		written = persisted.APIKey
		if err := store.Set(persisted.Name, written); err != nil {
			return ProviderCommitResult{}, fmt.Errorf("store API key for %q: %w", persisted.Name, err)
		}
		persisted.APIKey = ""
		persisted.APIKeyStored = true
	}

	rollback := func() {
		if !captured {
			return
		}
		current, present, getErr := store.Get(persisted.Name)
		if getErr != nil || !present || current != written {
			return
		}
		if previousPresent {
			_ = store.Set(persisted.Name, previous)
		} else {
			_, _ = store.Delete(persisted.Name)
		}
	}
	if err := upsertProviderConfig(&cfg, persisted, commit.SetActive); err != nil {
		rollback()
		return ProviderCommitResult{}, err
	}
	if err := writeConfigFile(path, cfg); err != nil {
		rollback()
		return ProviderCommitResult{}, err
	}
	return ProviderCommitResult{Config: cfg, Persisted: persisted}, nil
}

func lockProviderWrite(configPath string) (func() error, error) {
	lockPath := filepath.Join(filepath.Dir(configPath), ".zero-provider-write.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("acquire provider config/key transaction lock: %w", err)
	}
	token := fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), providerWriteLockSeq.Add(1))
	deadline := time.Now().Add(providerWriteLockTimeout)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, writeErr := file.WriteString(token); writeErr != nil {
				_ = file.Close()
				_ = lockutil.RemoveLockFile(lockPath)
				return nil, fmt.Errorf("write provider config/key transaction lock: %w", writeErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = lockutil.RemoveLockFile(lockPath)
				return nil, fmt.Errorf("close provider config/key transaction lock: %w", closeErr)
			}
			released := false
			return func() error {
				if released {
					return nil
				}
				released = true
				data, readErr := os.ReadFile(lockPath)
				if readErr != nil {
					return fmt.Errorf("release provider config/key transaction lock: %w", readErr)
				}
				if string(data) != token {
					return fmt.Errorf("release provider config/key transaction lock: ownership changed")
				}
				if err := lockutil.RemoveLockFile(lockPath); err != nil {
					return fmt.Errorf("release provider config/key transaction lock: %w", err)
				}
				return nil
			}, nil
		}
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("acquire provider config/key transaction lock: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("provider config/key transaction is busy; retry the operation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
