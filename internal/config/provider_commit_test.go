package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A rejected case-variant write must not take the accepted profile's secret with
// it. The credential store is keyed case-insensitively while the config
// collision check compares exact names, so the capture and the check must happen
// as one transaction: capturing first and rejecting afterwards overwrote the
// working `foo` key with the rejected `FOO` write's key.
func TestCommitProviderProfileRejectedCollisionKeepsExistingKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "foo",
		Providers: []ProviderProfile{
			{Name: "foo", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", APIKeyStored: true, Model: "m1"},
		},
	}, 0o600)
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("foo", "sk-original"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	_, err = CommitProviderProfile(path, ProviderCommit{Profile: ProviderProfile{
		Name:         "FOO",
		ProviderKind: ProviderKindOpenAICompatible,
		BaseURL:      "https://b.example.com/v1",
		APIKey:       "sk-replacement",
		Model:        "m2",
	}})
	if err == nil {
		t.Fatal("CommitProviderProfile must reject a case-variant collision")
	}
	if !strings.Contains(err.Error(), "already exists as") {
		t.Fatalf("CommitProviderProfile error = %v, want a collision report", err)
	}

	key, ok, err := store.Get("foo")
	if err != nil || !ok {
		t.Fatalf("existing key must survive a rejected write, got ok=%v err=%v", ok, err)
	}
	if key != "sk-original" {
		t.Fatalf("rejected write replaced the existing profile's key: got %q", key)
	}
}

// The same guarantee for a name that had no stored key before: a rejected write
// must not leave its captured secret behind under the shared identity.
func TestCommitProviderProfileRejectedCollisionRemovesCapturedKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	writeConfigFixture(t, path, FileConfig{
		ActiveProvider: "work",
		Providers: []ProviderProfile{
			{Name: "work", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://a.example.com/v1", Model: "m1"},
		},
	}, 0o600)

	if _, err := CommitProviderProfile(path, ProviderCommit{Profile: ProviderProfile{
		Name:         "WORK",
		ProviderKind: ProviderKindOpenAICompatible,
		BaseURL:      "https://b.example.com/v1",
		APIKey:       "sk-replacement",
	}}); err == nil {
		t.Fatal("CommitProviderProfile must reject a case-variant collision")
	}

	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if key, ok, err := store.Get("work"); err != nil || ok {
		t.Fatalf("rejected write left a captured key behind: key=%q ok=%v err=%v", key, ok, err)
	}
}

func TestCommitProviderProfileCapturesKeyAndPersistsRow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")

	result, err := CommitProviderProfile(path, ProviderCommit{
		Profile: ProviderProfile{
			Name:         "work",
			ProviderKind: ProviderKindOpenAICompatible,
			BaseURL:      "https://a.example.com/v1",
			APIKey:       "sk-secret",
			Model:        "m1",
		},
		SetActive: true,
	})
	if err != nil {
		t.Fatalf("CommitProviderProfile() error = %v", err)
	}
	if result.Persisted.APIKey != "" || !result.Persisted.APIKeyStored {
		t.Fatalf("persisted profile must carry the capture flip, got %+v", result.Persisted)
	}
	if result.Config.ActiveProvider != "work" {
		t.Fatalf("active provider = %q, want work", result.Config.ActiveProvider)
	}
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if key, ok, err := store.Get("work"); err != nil || !ok || key != "sk-secret" {
		t.Fatalf("captured key = %q ok=%v err=%v, want the secret in the store", key, ok, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "sk-secret") {
		t.Fatal("config.json must never hold the cleartext key")
	}
}

// KeepStoredKey is the path that must not touch the store at all: the profile
// already references a credential this write is not replacing.
func TestCommitProviderProfileKeepStoredKeyLeavesStoreUntouched(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Set("work", "sk-existing"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	if _, err := CommitProviderProfile(path, ProviderCommit{
		Profile: ProviderProfile{
			Name:         "work",
			ProviderKind: ProviderKindOpenAICompatible,
			BaseURL:      "https://a.example.com/v1",
			APIKeyStored: true,
			Model:        "m1",
		},
		SetActive:     true,
		KeepStoredKey: true,
	}); err != nil {
		t.Fatalf("CommitProviderProfile() error = %v", err)
	}
	if key, ok, err := store.Get("work"); err != nil || !ok || key != "sk-existing" {
		t.Fatalf("stored key = %q ok=%v err=%v, want it untouched", key, ok, err)
	}
}

// The restore half of the capture, exercised directly: inside the lock the
// collision is caught before any capture, so this covers the remaining case —
// the config write failing after the key has already moved into the store
// (a full disk, a permission error) — where the credential store must not keep
// a secret no config row refers to.
func TestSecureProviderProfileRestoreUndoesCapture(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	// No previous entry: restore removes what the capture created.
	secured, restore := secureProviderProfileWithRestore(ProviderProfile{Name: "work", APIKey: "sk-new"}, path)
	if secured.APIKey != "" || !secured.APIKeyStored {
		t.Fatalf("capture must flip the profile, got %+v", secured)
	}
	if key, ok, err := store.Get("work"); err != nil || !ok || key != "sk-new" {
		t.Fatalf("captured key = %q ok=%v err=%v, want sk-new", key, ok, err)
	}
	restore()
	if key, ok, err := store.Get("work"); err != nil || ok {
		t.Fatalf("restore must remove the captured key, got %q ok=%v err=%v", key, ok, err)
	}

	// Previous entry: restore puts the displaced secret back.
	if err := store.Set("work", "sk-original"); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	_, restore = secureProviderProfileWithRestore(ProviderProfile{Name: "WORK", APIKey: "sk-new"}, path)
	if key, _, _ := store.Get("work"); key != "sk-new" {
		t.Fatalf("capture must overwrite the normalized entry, got %q", key)
	}
	restore()
	if key, ok, err := store.Get("work"); err != nil || !ok || key != "sk-original" {
		t.Fatalf("restore must put the displaced key back, got %q ok=%v err=%v", key, ok, err)
	}
}

// Concurrent writers for the same case-insensitive identity must not both
// commit, and whichever loses must leave the winner's key intact. This is the
// race the transaction exists for; the lock also has to be released so the
// loser's attempt completes rather than hanging.
func TestCommitProviderProfileConcurrentCaseVariantsKeepOneKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")

	names := []string{"foo", "FOO"}
	keys := map[string]string{"foo": "sk-lower", "FOO": "sk-upper"}
	errs := make([]error, len(names))
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index, name := range names {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, errs[index] = CommitProviderProfile(path, ProviderCommit{
				Profile: ProviderProfile{
					Name:         name,
					ProviderKind: ProviderKindOpenAICompatible,
					BaseURL:      "https://a.example.com/v1",
					APIKey:       keys[name],
					Model:        "m1",
				},
				SetActive: true,
			})
		}()
	}
	close(start)
	wait.Wait()

	committed := ""
	for index, err := range errs {
		if err == nil {
			if committed != "" {
				t.Fatalf("both %q and %q committed; case variants must collide", committed, names[index])
			}
			committed = names[index]
		}
	}
	if committed == "" {
		t.Fatalf("neither write committed: %v", errs)
	}

	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	key, ok, err := store.Get(committed)
	if err != nil || !ok {
		t.Fatalf("committed profile lost its key: ok=%v err=%v", ok, err)
	}
	if key != keys[committed] {
		t.Fatalf("stored key = %q, want %q — the rejected write replaced it", key, keys[committed])
	}
	names, err = PersistedProviderNames(path)
	if err != nil {
		t.Fatalf("PersistedProviderNames() error = %v", err)
	}
	if len(names) != 1 || names[0] != committed {
		t.Fatalf("persisted rows = %v, want only %q", names, committed)
	}
}
