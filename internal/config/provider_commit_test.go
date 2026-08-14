package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestCommitProviderProfileCrossProcessCaseVariantsKeepOneKey(t *testing.T) {
	// The children below pin the encrypted-file backend, so this process must
	// pin it too: auto resolution picks the macOS keychain, and the parent would
	// then verify a different backend than the one the children wrote to.
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	commands := []*exec.Cmd{
		exec.Command(os.Args[0], "-test.run=^TestProviderCommitProcessHelper$"),
		exec.Command(os.Args[0], "-test.run=^TestProviderCommitProcessHelper$"),
	}
	commands[0].Env = append(os.Environ(), "ZERO_PROVIDER_COMMIT_HELPER=1", "ZERO_CRED_STORAGE=encrypted-file", "ZERO_PROVIDER_COMMIT_PATH="+path, "ZERO_PROVIDER_COMMIT_NAME=foo", "ZERO_PROVIDER_COMMIT_KEY=sk-lower")
	commands[1].Env = append(os.Environ(), "ZERO_PROVIDER_COMMIT_HELPER=1", "ZERO_CRED_STORAGE=encrypted-file", "ZERO_PROVIDER_COMMIT_PATH="+path, "ZERO_PROVIDER_COMMIT_NAME=FOO", "ZERO_PROVIDER_COMMIT_KEY=sk-upper")
	for _, command := range commands {
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	successes := 0
	for _, command := range commands {
		if command.Wait() == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful child commits = %d, want exactly one", successes)
	}
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("persisted providers = %+v, want one", cfg.Providers)
	}
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := store.Get(cfg.Providers[0].Name)
	if err != nil || !ok || (key != "sk-lower" && key != "sk-upper") {
		t.Fatalf("committed key = %q ok=%v err=%v", key, ok, err)
	}
}

func TestProviderCommitProcessHelper(t *testing.T) {
	if os.Getenv("ZERO_PROVIDER_COMMIT_HELPER") != "1" {
		return
	}
	_, err := CommitProviderProfile(os.Getenv("ZERO_PROVIDER_COMMIT_PATH"), ProviderCommit{Profile: ProviderProfile{
		Name:   os.Getenv("ZERO_PROVIDER_COMMIT_NAME"),
		APIKey: os.Getenv("ZERO_PROVIDER_COMMIT_KEY"),
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCommitProviderProfileFailsClosedWhenLockIsBusy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	lockPath := filepath.Join(dir, ".zero-provider-write.lock")
	if err := os.WriteFile(lockPath, []byte("live-holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTimeout := providerWriteLockTimeout
	providerWriteLockTimeout = 20 * time.Millisecond
	t.Cleanup(func() { providerWriteLockTimeout = oldTimeout })

	_, err := CommitProviderProfile(path, ProviderCommit{Profile: ProviderProfile{Name: "work", APIKey: "sk-new"}})
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("CommitProviderProfile error = %v, want retryable busy error", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("config was written without the lock: %v", statErr)
	}
	store, storeErr := ProviderKeyStoreAt(dir)
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	if key, ok, getErr := store.Get("work"); getErr != nil || ok {
		t.Fatalf("credential changed without the lock: key=%q ok=%v err=%v", key, ok, getErr)
	}
}

func TestCommitProviderProfileFailsClosedWhenLockCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "config.json")
	_, err := CommitProviderProfile(path, ProviderCommit{Profile: ProviderProfile{Name: "work", APIKey: "sk-new"}})
	if err == nil || !strings.Contains(err.Error(), "transaction lock") {
		t.Fatalf("CommitProviderProfile error = %v, want lock acquisition error", err)
	}
}

func TestCommitProviderProfileRollsBackKeyWhenPublicationFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("work", "sk-original"); err != nil {
		t.Fatal(err)
	}
	oldPublish := publishProviderConfig
	publishProviderConfig = func(string, FileConfig) error { return errors.New("disk full") }
	t.Cleanup(func() { publishProviderConfig = oldPublish })

	if _, err := CommitProviderProfile(path, ProviderCommit{Profile: ProviderProfile{Name: "work", APIKey: "sk-new"}}); err == nil {
		t.Fatal("CommitProviderProfile error = nil, want publication failure")
	}
	if key, ok, err := store.Get("work"); err != nil || !ok || key != "sk-original" {
		t.Fatalf("rollback key = %q ok=%v err=%v, want original", key, ok, err)
	}
}

func TestCommitCatalogProviderKeyConcurrentWithCaseVariantAddStaysConsistent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(dir, "config.json")
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, err := CommitProviderProfile(path, ProviderCommit{Profile: ProviderProfile{Name: "OpenRouter", CatalogID: "openrouter", APIKey: "sk-add"}})
		errs <- err
	}()
	go func() {
		<-start
		_, err := CommitCatalogProviderKey(path, "openrouter", "sk-auth")
		errs <- err
	}()
	close(start)
	firstErr, secondErr := <-errs, <-errs
	if firstErr != nil && secondErr != nil {
		t.Fatalf("both transactions failed: %v; %v", firstErr, secondErr)
	}
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || !cfg.Providers[0].APIKeyStored {
		t.Fatalf("persisted providers = %+v, want one keyed row", cfg.Providers)
	}
	store, err := ProviderKeyStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := store.Get(cfg.Providers[0].Name)
	if err != nil || !ok || (key != "sk-add" && key != "sk-auth") {
		t.Fatalf("resolved key = %q ok=%v err=%v", key, ok, err)
	}
}
