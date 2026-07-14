package oauth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// fakeKR is an in-memory KeyringClient for exercising the keyring backend
// without touching a real OS keychain.
type fakeKR struct{ data map[string]string }

func newFakeKR() *fakeKR { return &fakeKR{data: map[string]string{}} }

func (f *fakeKR) Get(service, account string) (string, bool, error) {
	v, ok := f.data[service+"/"+account]
	return v, ok, nil
}
func (f *fakeKR) Set(service, account, secret string) error {
	f.data[service+"/"+account] = secret
	return nil
}
func (f *fakeKR) Delete(service, account string) (bool, error) {
	key := service + "/" + account
	_, ok := f.data[key]
	delete(f.data, key)
	return ok, nil
}

func TestStoreKeyringBackendRoundTrip(t *testing.T) {
	// Keep the cross-process keyring lock file inside a temp config dir.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatalf("NewStore(keyring): %v", err)
	}
	if !strings.HasPrefix(s.FilePath(), "keyring:") {
		t.Fatalf("FilePath = %q, want keyring identifier", s.FilePath())
	}

	if err := s.Save(ProviderKey("demo"), Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := s.Load(ProviderKey("demo"))
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "a" || got.RefreshToken != "r" {
		t.Fatalf("Load = %#v", got)
	}

	// The token lives under its own entry (account = key), not one combined
	// blob, and is base64-encoded so the raw JSON field names never appear.
	raw := kr.data[keyringService+"/"+ProviderKey("demo")]
	if raw == "" {
		t.Fatal("nothing stored under the token's own keyring entry")
	}
	if strings.Contains(raw, "access_token") {
		t.Fatalf("keyring entry is not encoded: %s", raw)
	}
	if raw := kr.data[keyringService+"/"+keyringLegacyAccount]; raw != "" {
		t.Fatalf("legacy combined entry should not be written by new code: %s", raw)
	}

	removed, err := s.Delete(ProviderKey("demo"))
	if err != nil || !removed {
		t.Fatalf("Delete: removed=%v err=%v", removed, err)
	}
	if _, ok, _ := s.Load(ProviderKey("demo")); ok {
		t.Fatal("token still present after delete")
	}
	// Delete must also drop the now-unused entry, not just remove it from the
	// index, or a stale keyring item accumulates for every logout.
	if _, ok := kr.data[keyringService+"/"+ProviderKey("demo")]; ok {
		t.Fatal("deleted token's keyring entry was not removed")
	}
}

// TestStoreKeyringManyProvidersStayUnderEntryLimit is the regression test for
// the bug this backend originally shipped with: every provider's tokens were
// combined into one keyring entry, and on macOS that entry is written through
// `security -i`, whose command parser caps a single write around 4KB. Three or
// more logged-in providers routinely exceeded it, so Set() would start failing
// for every provider, not just the one pushing it over. Splitting into one
// entry per key bounds each individual write to a single token regardless of
// how many providers are logged in.
func TestStoreKeyringManyProvidersStayUnderEntryLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	// A realistically large single token: JWT-shaped access/ID tokens plus an
	// opaque refresh token, comparable to what OIDC providers actually issue.
	big := Token{
		AccessToken:  "eyJhbGciOiJSUzI1NiJ9." + strings.Repeat("QUJDRA", 60) + ".sig",
		RefreshToken: "rt_" + strings.Repeat("x", 80),
		TokenType:    "Bearer",
		Scopes:       []string{"openid", "profile", "email", "offline_access"},
		Account:      "user@example.com",
		IDToken:      "eyJhbGciOiJSUzI1NiJ9." + strings.Repeat("QUJDRA", 70) + ".sig",
	}
	providers := []string{"anthropic", "openai", "minimax", "zai", "google"}
	for _, name := range providers {
		if err := s.Save(ProviderKey(name), big); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}
	// Each individual keyring value must stay small even with 5 providers
	// logged in: no entry aggregates more than one provider's tokens.
	const singleTokenCeiling = 3000 // generous margin under the ~4095-byte line cap
	for k, v := range kr.data {
		if len(v) > singleTokenCeiling {
			t.Fatalf("keyring entry %q is %d bytes, want < %d (aggregation regression)", k, len(v), singleTokenCeiling)
		}
	}
	for _, name := range providers {
		got, ok, err := s.Load(ProviderKey(name))
		if err != nil || !ok {
			t.Fatalf("Load(%s): ok=%v err=%v", name, ok, err)
		}
		if got.AccessToken != big.AccessToken {
			t.Fatalf("Load(%s) = %#v", name, got)
		}
	}
}

// TestStoreKeyringMigratesLegacyCombinedEntry ensures installs upgrading from
// the original single-blob format keep reading their existing tokens, and get
// migrated to per-key entries (with the legacy entry removed) the next time
// anything is saved.
func TestStoreKeyringMigratesLegacyCombinedEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("demo"): {AccessToken: "legacy-a", RefreshToken: "legacy-r"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load(ProviderKey("demo"))
	if err != nil || !ok {
		t.Fatalf("Load legacy token: ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "legacy-a" {
		t.Fatalf("Load = %#v", got)
	}

	// Saving a second provider must migrate: the legacy entry is dropped, and
	// both tokens end up as their own entries.
	if err := s.Save(ProviderKey("other"), Token{AccessToken: "other-a"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := kr.data[keyringService+"/"+keyringLegacyAccount]; ok {
		t.Fatal("legacy combined entry should be removed after migration")
	}
	for _, name := range []string{"demo", "other"} {
		if _, ok, err := s.Load(ProviderKey(name)); err != nil || !ok {
			t.Fatalf("Load(%s) after migration: ok=%v err=%v", name, ok, err)
		}
	}
}

// TestStoreKeyringSkipsIndexedKeyMissingItsEntry covers read()'s recovery from
// an index/entry desync: a key listed in the index whose own entry is
// missing (e.g. a process killed between writing the entry and updating the
// index, or between updating the index and deleting a removed entry). read()
// must skip that key rather than fail the whole read, since the next
// Save/Delete reconciles the index against what's actually there.
func TestStoreKeyringSkipsIndexedKeyMissingItsEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()

	present := Token{AccessToken: "present-a", RefreshToken: "present-r"}
	raw, err := json.Marshal(present)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+ProviderKey("present")] = base64.StdEncoding.EncodeToString(raw)

	// The index references both keys, but "missing"'s own entry was never
	// written (or was already deleted) — the desync this test targets.
	index, err := json.Marshal([]string{ProviderKey("missing"), ProviderKey("present")})
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(index)

	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.Load(ProviderKey("missing")); err != nil || ok {
		t.Fatalf("Load(missing): ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	got, ok, err := s.Load(ProviderKey("present"))
	if err != nil || !ok {
		t.Fatalf("Load(present): ok=%v err=%v", ok, err)
	}
	if got.AccessToken != present.AccessToken {
		t.Fatalf("Load(present) = %#v", got)
	}

	statuses, err := s.Status("")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Key != ProviderKey("present") {
		t.Fatalf("Status = %#v, want only the present key", statuses)
	}
}

func TestNewStoreStorageSelection(t *testing.T) {
	// Unknown storage is rejected (fail closed).
	if _, err := NewStore(StoreOptions{Storage: "bogus"}); err == nil {
		t.Fatal("unknown storage should error")
	}
	// ZERO_OAUTH_STORAGE selects the keyring (with an injected client).
	s, err := NewStore(StoreOptions{
		Env:     map[string]string{"ZERO_OAUTH_STORAGE": "keyring"},
		Keyring: newFakeKR(),
	})
	if err != nil {
		t.Fatalf("NewStore(env keyring): %v", err)
	}
	if !strings.HasPrefix(s.FilePath(), "keyring:") {
		t.Fatalf("env did not select keyring backend: %q", s.FilePath())
	}
	// Default is the file backend.
	fileStore, err := NewStore(StoreOptions{FilePath: t.TempDir() + "/oauth-tokens.json"})
	if err != nil {
		t.Fatalf("NewStore(file): %v", err)
	}
	if strings.HasPrefix(fileStore.FilePath(), "keyring:") {
		t.Fatalf("default backend should be file, got %q", fileStore.FilePath())
	}
}

func TestStoreKeyringStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("demo"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	statuses, err := s.Status(KeyPrefixProvider)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Key != ProviderKey("demo") || !statuses[0].HasToken {
		t.Fatalf("status = %#v", statuses)
	}
}
