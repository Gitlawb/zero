package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gitlawb/zero/internal/credstore"
	"github.com/Gitlawb/zero/internal/lockutil"
)

type providerCredentialSnapshot struct {
	name       string
	value      string
	present    bool
	written    string
	writtenSet bool
}

// providerProfileOperation is the authoritative write boundary for provider
// rows and their API-key entries. Callers mutate its in-memory config and use
// setKey/deleteKey; runProviderProfileOperation publishes config atomically and
// conditionally restores credential changes if publication fails.
type providerProfileOperation struct {
	path      string
	config    FileConfig
	store     *credstore.Store
	snapshots map[string]providerCredentialSnapshot
	publish   bool
	exists    bool
}

var publishProviderConfig = writeConfigFile
var acquireProviderWriteLock = lockProviderWrite

func runProviderProfileOperation(path string, allowMissing bool, allowInvalidInput bool, mutate func(*providerProfileOperation) error) (result FileConfig, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	release, err := acquireProviderWriteLock(path)
	if err != nil {
		return FileConfig{}, err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			if err == nil {
				err = fmt.Errorf("provider configuration was committed, but releasing its transaction lock failed: %w", releaseErr)
			} else {
				err = errors.Join(err, releaseErr)
			}
		}
	}()

	cfg := FileConfig{}
	exists := false
	if data, readErr := os.ReadFile(path); readErr == nil {
		exists = true
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if os.IsNotExist(readErr) && !allowMissing {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, readErr)
	} else if !os.IsNotExist(readErr) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, readErr)
	}
	if !allowInvalidInput {
		if err := ValidatePersistedProviderNames(cfg); err != nil {
			return FileConfig{}, err
		}
	}
	op := &providerProfileOperation{path: path, config: cfg, snapshots: map[string]providerCredentialSnapshot{}, publish: true, exists: exists}
	if err := mutate(op); err != nil {
		return FileConfig{}, errors.Join(err, op.rollbackCredentials())
	}
	if op.publish {
		if err := publishProviderConfig(path, op.config); err != nil {
			return FileConfig{}, errors.Join(err, op.rollbackCredentials())
		}
	}
	return op.config, nil
}

func (op *providerProfileOperation) credentialStore() (*credstore.Store, error) {
	if op.store != nil {
		return op.store, nil
	}
	store, err := ProviderKeyStoreAt(filepath.Dir(op.path))
	if err != nil {
		return nil, err
	}
	op.store = store
	return store, nil
}

func (op *providerProfileOperation) snapshotCredential(name string) (providerCredentialSnapshot, error) {
	identity := credstore.NormalizeProvider(name)
	if snapshot, ok := op.snapshots[identity]; ok {
		return snapshot, nil
	}
	store, err := op.credentialStore()
	if err != nil {
		return providerCredentialSnapshot{}, err
	}
	value, present, err := store.Get(name)
	if err != nil {
		return providerCredentialSnapshot{}, err
	}
	snapshot := providerCredentialSnapshot{name: name, value: value, present: present}
	op.snapshots[identity] = snapshot
	return snapshot, nil
}

func (op *providerProfileOperation) setKey(name, value string) error {
	identity := credstore.NormalizeProvider(name)
	snapshot, err := op.snapshotCredential(name)
	if err != nil {
		return err
	}
	store, err := op.credentialStore()
	if err != nil {
		return err
	}
	if err := store.Set(name, value); err != nil {
		return err
	}
	snapshot.written, snapshot.writtenSet = value, true
	op.snapshots[identity] = snapshot
	return nil
}

func (op *providerProfileOperation) deleteKey(name string) (bool, error) {
	identity := credstore.NormalizeProvider(name)
	snapshot, err := op.snapshotCredential(name)
	if err != nil {
		return false, err
	}
	store, err := op.credentialStore()
	if err != nil {
		return false, err
	}
	removed, err := store.Delete(name)
	if err != nil {
		return false, err
	}
	snapshot.written, snapshot.writtenSet = "", false
	op.snapshots[identity] = snapshot
	return removed, nil
}

func (op *providerProfileOperation) rollbackCredentials() error {
	if op.store == nil {
		return nil
	}
	var rollbackErr error
	for _, snapshot := range op.snapshots {
		current, present, err := op.store.Get(snapshot.name)
		if err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("inspect credential %q during rollback: %w", snapshot.name, err))
			continue
		}
		if present != snapshot.writtenSet || (present && current != snapshot.written) {
			continue
		}
		if snapshot.present {
			if err := op.store.Set(snapshot.name, snapshot.value); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore credential %q during rollback: %w", snapshot.name, err))
			}
		} else {
			if _, err := op.store.Delete(snapshot.name); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove credential %q during rollback: %w", snapshot.name, err))
			}
		}
	}
	return rollbackErr
}

// ProviderCommit is one provider write: reject a colliding spelling, capture
// the inline key into the credential store, and persist the row.
type ProviderCommit struct {
	Profile   ProviderProfile
	SetActive bool
	// KeepStoredKey leaves the credential store untouched instead of capturing
	// Profile.APIKey — the case where the profile already references a stored
	// credential that this write must not replace.
	KeepStoredKey bool
}

// ProviderCommitResult reports the config as written and the profile exactly as
// it was persisted: the adopted name, and the key moved into the credential
// store (APIKey cleared, APIKeyStored set) unless KeepStoredKey was requested.
type ProviderCommitResult struct {
	Config    FileConfig
	Persisted ProviderProfile
}

// CommitProviderProfile runs the case-variant collision check, credential
// capture, and config publication as one serialized transaction.
//
// Splitting those steps is what made the credential store and config disagree.
// The capture writes under the credential store's NORMALIZED identity while the
// collision check reads config's EXACT names, so two concurrent writers for
// `foo` and `FOO` could both pass the check against an empty config; the loser
// then overwrote the winner's `foo` credential entry on its way to being
// correctly rejected by the config write, destroying a working profile's API
// key. A cross-process lock keeps a second writer out for the whole sequence,
// and a rejected write restores the credential entry it displaced, so a failed
// mutation cannot leave another profile's secret behind.
func CommitProviderProfile(path string, commit ProviderCommit) (ProviderCommitResult, error) {
	var persisted ProviderProfile
	cfg, err := runProviderProfileOperation(path, true, false, func(op *providerProfileOperation) error {
		persisted = commit.Profile
		if !commit.KeepStoredKey && strings.TrimSpace(persisted.APIKey) != "" {
			if err := op.setKey(persisted.Name, persisted.APIKey); err != nil {
				return fmt.Errorf("store API key for %q: %w", persisted.Name, err)
			}
			persisted.APIKey = ""
			persisted.APIKeyStored = true
		}
		return upsertProviderConfig(&op.config, persisted, commit.SetActive)
	})
	result := ProviderCommitResult{Config: cfg, Persisted: persisted}
	if err != nil {
		if len(cfg.Providers) == 0 {
			return ProviderCommitResult{}, err
		}
		return result, err
	}
	return result, nil
}

var providerWriteLockTimeout = 5 * time.Second

var providerWriteLockSeq atomic.Uint64

// lockProviderWrite takes a cross-process exclusive lock covering the provider
// rows of the config at path and the credential store beside it, which are one
// unit of state written by two files. It mirrors the O_EXCL lock used by the
// oauth token store: a bounded wait and an ownership-checked release. This lock
// is deliberately never reclaimed by age: a credential backend may keep a
// valid holder blocked for an arbitrary duration, so age alone cannot prove
// that stealing the lock is safe. A stranded lock fails writes closed until it
// is removed after the owning process is known to be gone.
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
			if _, werr := file.WriteString(token); werr != nil {
				_ = file.Close()
				_ = lockutil.RemoveLockFile(lockPath)
				return nil, fmt.Errorf("write provider config/key transaction lock: %w", werr)
			}
			if cerr := file.Close(); cerr != nil {
				_ = lockutil.RemoveLockFile(lockPath)
				return nil, fmt.Errorf("close provider config/key transaction lock: %w", cerr)
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
		// On Windows a concurrent holder's remove leaves the file delete-pending, so
		// an O_EXCL create races it with ERROR_ACCESS_DENIED rather than ErrExist.
		// That is contention only on Windows; on Unix ErrPermission is a permanent
		// access failure and must be returned immediately instead of timing out.
		contention := errors.Is(err, os.ErrExist) || runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission)
		if !contention {
			return nil, fmt.Errorf("acquire provider config/key transaction lock: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("provider config/key transaction is busy; retry the operation, or remove %s if no other Zero process is running", lockPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
