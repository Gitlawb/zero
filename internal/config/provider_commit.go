package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gitlawb/zero/internal/lockutil"
)

// ProviderCommit is one provider write: resolve the name against what is
// already saved, reject a colliding spelling, capture the inline key into the
// credential store, and persist the row.
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

// CommitProviderProfile runs name adoption, the case-variant collision check,
// credential capture, and the config write as one serialized transaction.
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
	path = strings.TrimSpace(path)
	if path == "" {
		return ProviderCommitResult{}, fmt.Errorf("config path is required")
	}
	release, err := lockProviderWrite(path)
	if err != nil {
		return ProviderCommitResult{}, err
	}
	defer release()

	profile, err := AdoptPersistedCatalogProviderName(path, commit.Profile)
	if err != nil {
		return ProviderCommitResult{}, err
	}
	if err := PreflightProviderWrite(path, profile.Name); err != nil {
		return ProviderCommitResult{}, err
	}

	persisted := profile
	restore := func() {}
	if !commit.KeepStoredKey {
		persisted, restore = secureProviderProfileWithRestore(profile, path)
	}
	cfg, err := UpsertProvider(path, persisted, commit.SetActive)
	if err != nil {
		restore()
		return ProviderCommitResult{}, err
	}
	return ProviderCommitResult{Config: cfg, Persisted: persisted}, nil
}

// secureProviderProfileWithRestore captures the profile's inline key like
// SecureProviderProfile and additionally returns a function that puts the
// credential store back the way it was — restoring a displaced key, or removing
// the entry this capture created. The caller runs it when the config write that
// justified the capture is rejected.
func secureProviderProfileWithRestore(profile ProviderProfile, configPath string) (ProviderProfile, func()) {
	noRestore := func() {}
	if strings.TrimSpace(profile.APIKey) == "" || strings.TrimSpace(profile.Name) == "" {
		return profile, noRestore
	}
	store, err := ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		return profile, noRestore
	}
	// Read the displaced value before overwriting it. A store that cannot be read
	// cannot be rolled back either, so leave it alone rather than capture a key
	// this call would not be able to undo.
	previous, hadPrevious, err := store.Get(profile.Name)
	if err != nil {
		return profile, noRestore
	}
	if err := store.Set(profile.Name, profile.APIKey); err != nil {
		return profile, noRestore
	}
	secured := profile
	secured.APIKey = ""
	secured.APIKeyStored = true
	return secured, func() {
		if hadPrevious {
			_ = store.Set(profile.Name, previous)
			return
		}
		_, _ = store.Delete(profile.Name)
	}
}

const (
	providerWriteLockTimeout    = 5 * time.Second
	providerWriteLockStaleAfter = 30 * time.Second
)

var providerWriteLockSeq atomic.Uint64

// lockProviderWrite takes a cross-process exclusive lock covering the provider
// rows of the config at path and the credential store beside it, which are one
// unit of state written by two files. It mirrors the O_EXCL lock used by the
// oauth token store: a bounded wait, a stale-holder reclaim so a crashed writer
// cannot deadlock the next one, and an ownership-checked release.
//
// Failing to take the lock does not fail the write. This lock removes a
// concurrency window; treating a locking problem as a reason to refuse a
// provider write would turn a rare race into a routine outage.
func lockProviderWrite(configPath string) (func(), error) {
	lockPath := filepath.Join(filepath.Dir(configPath), ".zero-provider-write.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return func() {}, nil
	}
	token := fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), providerWriteLockSeq.Add(1))
	deadline := time.Now().Add(providerWriteLockTimeout)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, werr := file.WriteString(token); werr != nil {
				_ = file.Close()
				_ = lockutil.RemoveLockFile(lockPath)
				return func() {}, nil
			}
			if cerr := file.Close(); cerr != nil {
				_ = lockutil.RemoveLockFile(lockPath)
				return func() {}, nil
			}
			released := false
			return func() {
				if released {
					return
				}
				released = true
				if data, rerr := os.ReadFile(lockPath); rerr == nil && string(data) == token {
					_ = lockutil.RemoveLockFile(lockPath)
				}
			}, nil
		}
		// On Windows a concurrent holder's remove leaves the file delete-pending, so
		// an O_EXCL create races it with ERROR_ACCESS_DENIED rather than ErrExist.
		// Both mean contention.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return func() {}, nil
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > providerWriteLockStaleAfter {
			cleared, rerr := lockutil.ReclaimStaleLock(lockPath, token, func(reclaimedPath string) bool {
				info, err := os.Stat(reclaimedPath)
				return err == nil && time.Since(info.ModTime()) <= providerWriteLockStaleAfter
			})
			if rerr == nil && cleared {
				continue
			}
		}
		if time.Now().After(deadline) {
			return func() {}, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}
