package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCommitProviderProfileSerializesCaseVariantCredentialCapture(t *testing.T) {
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	path := filepath.Join(t.TempDir(), "config.json")

	type outcome struct {
		name string
		key  string
		err  error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, candidate := range []outcome{{name: "work", key: "key-one"}, {name: "WORK", key: "key-two"}} {
		candidate := candidate
		go func() {
			ready.Done()
			<-start
			_, err := CommitProviderProfile(path, ProviderCommit{Profile: ProviderProfile{Name: candidate.name, APIKey: candidate.key}})
			candidate.err = err
			outcomes <- candidate
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-outcomes, <-outcomes
	if (first.err == nil) == (second.err == nil) {
		t.Fatalf("success count = %d, want 1", boolInt(first.err == nil)+boolInt(second.err == nil))
	}
	winner := first
	if winner.err != nil {
		winner = second
	}
	loser := second
	if loser.err == nil {
		loser = first
	}
	if !strings.Contains(loser.err.Error(), "already exists as") {
		t.Fatalf("loser error = %v", loser.err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != winner.name || !cfg.Providers[0].APIKeyStored {
		t.Fatalf("persisted providers = %+v, want stored winner %q", cfg.Providers, winner.name)
	}
	store, err := ProviderKeyStoreAt(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := store.Get(winner.name)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || key != winner.key {
		t.Fatalf("winner credential present=%v matches=%v", ok, key == winner.key)
	}
}

func TestProviderCredentialSurvivesRequiresStoredMarker(t *testing.T) {
	providers := []ProviderProfile{{Name: "WORK"}}
	if ProviderCredentialSurvives(providers, "work") {
		t.Fatal("markerless case-variant row must not retain the credential")
	}
	providers[0].APIKeyStored = true
	if !ProviderCredentialSurvives(providers, "work") {
		t.Fatal("stored-key case-variant row must retain the credential")
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
