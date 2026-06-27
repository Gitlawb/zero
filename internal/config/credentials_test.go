package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeKeyGetter struct {
	keys map[string]string
	err  error
}

func (f fakeKeyGetter) Get(provider string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.keys[provider]
	return v, ok, nil
}

// fakeKeySetter records Set calls; setErr (when non-nil) makes Set fail.
type fakeKeySetter struct {
	keys   map[string]string
	setErr error
}

func (f *fakeKeySetter) Set(provider, key string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.keys[provider] = key
	return nil
}

func TestApplyStoredAPIKey(t *testing.T) {
	store := fakeKeyGetter{keys: map[string]string{"openai": "sk-stored"}}

	// Stored marker + empty key => filled from the store.
	got := ApplyStoredAPIKey(ProviderProfile{Name: "openai", APIKeyStored: true}, store)
	if got.APIKey != "sk-stored" {
		t.Fatalf("expected stored key to fill empty APIKey, got %q", got.APIKey)
	}

	// No APIKeyStored marker => store is NOT consulted (don't reactivate a stale key).
	got = ApplyStoredAPIKey(ProviderProfile{Name: "openai"}, store)
	if got.APIKey != "" {
		t.Fatalf("expected no load without APIKeyStored, got %q", got.APIKey)
	}

	// Inline key present => store is NOT consulted (inline wins).
	got = ApplyStoredAPIKey(ProviderProfile{Name: "openai", APIKeyStored: true, APIKey: "sk-inline"}, store)
	if got.APIKey != "sk-inline" {
		t.Fatalf("inline key must win, got %q", got.APIKey)
	}

	// Marker set but no stored key for this provider => unchanged (empty).
	got = ApplyStoredAPIKey(ProviderProfile{Name: "anthropic", APIKeyStored: true}, store)
	if got.APIKey != "" {
		t.Fatalf("expected no key for unstored provider, got %q", got.APIKey)
	}

	// Nil store => unchanged.
	got = ApplyStoredAPIKey(ProviderProfile{Name: "openai", APIKeyStored: true}, nil)
	if got.APIKey != "" {
		t.Fatalf("nil store must leave profile unchanged, got %q", got.APIKey)
	}

	// Empty name => unchanged (don't query the store).
	got = ApplyStoredAPIKey(ProviderProfile{APIKeyStored: true}, store)
	if got.APIKey != "" {
		t.Fatalf("empty name must not be filled, got %q", got.APIKey)
	}
}

func TestMigratePlaintextProviderKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfgJSON := `{"activeProvider":"openai","providers":[` +
		`{"name":"openai","apiKey":"sk-PLAINTEXT","model":"gpt"},` +
		`{"name":"local","baseURL":"http://localhost"},` +
		`{"name":"acme","apiKeyEnv":"ACME_KEY"}]}`
	if err := os.WriteFile(path, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &fakeKeySetter{keys: map[string]string{}}

	n, err := MigratePlaintextProviderKeys(path, store)
	if err != nil || n != 1 {
		t.Fatalf("migrate = %d,%v; want 1,nil", n, err)
	}
	if store.keys["openai"] != "sk-PLAINTEXT" {
		t.Fatalf("key not moved to store: %v", store.keys)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "sk-PLAINTEXT") {
		t.Fatalf("plaintext key still in config.json:\n%s", raw)
	}
	if !strings.Contains(string(raw), "apiKeyStored") {
		t.Fatalf("apiKeyStored marker not written:\n%s", raw)
	}
	// Idempotent: a second run migrates nothing.
	if n2, _ := MigratePlaintextProviderKeys(path, store); n2 != 0 {
		t.Fatalf("second migrate = %d, want 0", n2)
	}
}

func TestMigrateLeavesKeyWhenStoreSetFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"openai","apiKey":"sk-KEEP"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &fakeKeySetter{keys: map[string]string{}, setErr: errors.New("keychain locked")}
	n, err := MigratePlaintextProviderKeys(path, store)
	if err != nil || n != 0 {
		t.Fatalf("migrate with failing store = %d,%v; want 0,nil", n, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "sk-KEEP") {
		t.Fatalf("a failed Set must not strand the key; config.json:\n%s", raw)
	}
}

func TestClearProviderKeyStored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"openai","apiKeyStored":true},{"name":"other","apiKeyStored":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cleared, err := ClearProviderKeyStored(path, "openai")
	if err != nil || !cleared {
		t.Fatalf("clear = %v,%v; want true,nil", cleared, err)
	}
	raw, _ := os.ReadFile(path)
	// openai's marker is gone; other's remains.
	var cfg FileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.Providers {
		if p.Name == "openai" && p.APIKeyStored {
			t.Fatal("openai marker should be cleared")
		}
		if p.Name == "other" && !p.APIKeyStored {
			t.Fatal("other marker should be untouched")
		}
	}
	// Idempotent: clearing again reports no change.
	if cleared, _ := ClearProviderKeyStored(path, "openai"); cleared {
		t.Fatal("second clear should report no change")
	}
	// Unknown provider: no change.
	if cleared, _ := ClearProviderKeyStored(path, "nope"); cleared {
		t.Fatal("unknown provider should report no change")
	}
}

func TestProviderProfileAPIKeyStoredRoundTrips(t *testing.T) {
	// The apiKeyStored marker survives JSON decode (custom UnmarshalJSON).
	var p ProviderProfile
	if err := p.UnmarshalJSON([]byte(`{"name":"openai","apiKeyStored":true}`)); err != nil {
		t.Fatal(err)
	}
	if !p.APIKeyStored {
		t.Fatal("expected apiKeyStored=true to decode")
	}
	if p.APIKey != "" {
		t.Fatalf("no inline key expected, got %q", p.APIKey)
	}
}
