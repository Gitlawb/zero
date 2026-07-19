package oauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// failingKR wraps fakeKR and fails the Nth mutating operation (Set/Delete),
// for exercising every interruption boundary of the multi-step write.
type failingKR struct {
	*fakeKR
	failAt int // 1-based mutating-operation number to fail; 0 disables
	ops    int
}

func (f *failingKR) Set(service, account, secret string) error {
	f.ops++
	if f.failAt != 0 && f.ops == f.failAt {
		return errKRInjected
	}
	return f.fakeKR.Set(service, account, secret)
}

func (f *failingKR) Delete(service, account string) (bool, error) {
	f.ops++
	if f.failAt != 0 && f.ops == f.failAt {
		return false, errKRInjected
	}
	return f.fakeKR.Delete(service, account)
}

var errKRInjected = errKR("injected keyring failure")

type errKR string

func (e errKR) Error() string { return string(e) }

// indexedKeysOf parses the (possibly chunked) index in kr and returns every
// listed key.
func indexedKeysOf(t *testing.T, kr *fakeKR) map[string]bool {
	t.Helper()
	blob := keyringBlob{kr: kr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount}
	keys, _, _, err := blob.readKeyIndex()
	if err != nil {
		t.Fatalf("readKeyIndex: %v", err)
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// TestStoreKeyringIndexStaysUnderEntryLimit is the regression test for the
// index itself hitting the same macOS `security -i` line cap the per-token
// split fixed for token entries: with enough maximum-length keys, a single
// index entry base64-expands past 4095 bytes even when every token is tiny.
// The index must therefore be bounded per entry (chunked) like everything
// else, and still round-trip.
func TestStoreKeyringIndexStaysUnderEntryLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	// 40 keys near ValidateKey's cap: an unchunked index of these would
	// serialize to ~5.5KB before base64.
	names := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		names = append(names, strings.Repeat("p", 100)+"-"+strings.Repeat("0123456789", 2)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	for _, name := range names {
		if err := s.Save(ProviderKey(name), Token{AccessToken: "a"}); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}
	// Every keyring value, index entries included, must stay under the cap
	// with generous framing margin.
	const entryCeiling = 3800
	for k, v := range kr.data {
		if len(v) > entryCeiling {
			t.Fatalf("keyring entry %q is %d bytes, want <= %d (index cap regression)", k, len(v), entryCeiling)
		}
	}
	// The index actually chunked (otherwise the ceiling check proves nothing).
	if _, ok := kr.data[keyringService+"/"+keyringIndexAccount+"-1"]; !ok {
		t.Fatal("expected the index to split into continuation chunks")
	}
	for _, name := range names {
		if _, ok, err := s.Load(ProviderKey(name)); err != nil || !ok {
			t.Fatalf("Load(%s): ok=%v err=%v", name, ok, err)
		}
	}
	// Shrinking back to one token must also shrink the index and drop the
	// stale continuation chunks.
	for _, name := range names[1:] {
		if _, err := s.Delete(ProviderKey(name)); err != nil {
			t.Fatalf("Delete(%s): %v", name, err)
		}
	}
	if _, ok := kr.data[keyringService+"/"+keyringIndexAccount+"-1"]; ok {
		t.Fatal("stale index continuation chunk left behind after shrink")
	}
}

// TestStoreKeyringWriteInterruptionsLeaveNoInvisibleTokens drives a write
// through an injected failure at every mutating operation in turn and checks
// the recoverable-store invariant at each boundary: every token entry present
// in the keyring is listed in the published index (so no credential is ever
// stranded invisibly), and a subsequent unimpeded write fully reconciles.
func TestStoreKeyringWriteInterruptionsLeaveNoInvisibleTokens(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for failAt := 1; ; failAt++ {
		kr := &failingKR{fakeKR: newFakeKR()}
		s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		// Seed two tokens cleanly, then fail the Nth mutating operation of a
		// write that both adds a token and (via the later delete pass of a
		// Delete call) removes one.
		if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
			t.Fatal(err)
		}
		kr.ops = 0
		kr.failAt = failAt
		saveErr := s.Save(ProviderKey("gamma"), Token{AccessToken: "c"})
		opsUsed := kr.ops
		kr.failAt = 0

		// Invariant at the interruption boundary: nothing invisible.
		indexed := indexedKeysOf(t, kr.fakeKR)
		for entry := range kr.data {
			account := strings.TrimPrefix(entry, keyringService+"/")
			if account == keyringIndexAccount || strings.HasPrefix(account, keyringIndexAccount+"-") || account == keyringLegacyAccount {
				continue
			}
			if !indexed[account] {
				t.Fatalf("failAt=%d: token entry %q exists but is not listed in the index (invisible credential)", failAt, account)
			}
		}

		// The tokens this write didn't touch must stay readable at the
		// interruption boundary itself, not just after a later reconciling
		// write papers over an incorrect intermediate state.
		for _, name := range []string{"alpha", "beta"} {
			if _, ok, err := s.Load(ProviderKey(name)); err != nil || !ok {
				t.Fatalf("failAt=%d: Load(%s) before reconcile: ok=%v err=%v", failAt, name, ok, err)
			}
		}

		// A later unimpeded write must reconcile completely.
		if err := s.Save(ProviderKey("gamma"), Token{AccessToken: "c"}); err != nil {
			t.Fatalf("failAt=%d: reconciling Save: %v", failAt, err)
		}
		for _, name := range []string{"alpha", "beta", "gamma"} {
			if _, ok, err := s.Load(ProviderKey(name)); err != nil || !ok {
				t.Fatalf("failAt=%d: Load(%s) after reconcile: ok=%v err=%v", failAt, name, ok, err)
			}
		}
		// Every mutating boundary of the write path now surfaces its failure,
		// including the legacy-entry delete (a swallowed failure there could
		// let a later save resurrect logged-out credentials).
		if opsUsed >= failAt && saveErr == nil {
			t.Fatalf("failAt=%d: injected keyring failure was swallowed", failAt)
		}
		if opsUsed < failAt {
			// The write used fewer mutating ops than failAt, so the injection
			// never fired and every boundary has been covered.
			break
		}
	}
}

// TestStoreKeyringDeleteInterruptionsLeaveNoInvisibleTokens is the Delete
// counterpart: a logout interrupted at any boundary must not leave a
// logged-out credential invisibly resident in the OS keychain (the index is
// only shrunk after the entry deletion), and a repeated delete reconciles.
func TestStoreKeyringDeleteInterruptionsLeaveNoInvisibleTokens(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for failAt := 1; ; failAt++ {
		kr := &failingKR{fakeKR: newFakeKR()}
		s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
			t.Fatal(err)
		}
		kr.ops = 0
		kr.failAt = failAt
		_, deleteErr := s.Delete(ProviderKey("beta"))
		opsUsed := kr.ops
		kr.failAt = 0

		indexed := indexedKeysOf(t, kr.fakeKR)
		for entry := range kr.data {
			account := strings.TrimPrefix(entry, keyringService+"/")
			if account == keyringIndexAccount || strings.HasPrefix(account, keyringIndexAccount+"-") || account == keyringLegacyAccount {
				continue
			}
			if !indexed[account] {
				t.Fatalf("failAt=%d: token entry %q exists but is not listed in the index (invisible credential)", failAt, account)
			}
		}

		// Retrying the delete must fully reconcile: beta gone from both the
		// index and the keyring, alpha intact.
		if _, err := s.Delete(ProviderKey("beta")); err != nil {
			t.Fatalf("failAt=%d: reconciling Delete: %v", failAt, err)
		}
		if _, ok := kr.data[keyringService+"/"+ProviderKey("beta")]; ok {
			t.Fatalf("failAt=%d: logged-out credential still resident after reconcile", failAt)
		}
		if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || !ok {
			t.Fatalf("failAt=%d: Load(alpha): ok=%v err=%v", failAt, ok, err)
		}
		// Every mutating boundary of the delete path must surface its failure,
		// mirroring the Save-interruption assertion: a swallowed error here
		// would let a caller believe a logout succeeded when it didn't.
		if opsUsed >= failAt && deleteErr == nil {
			t.Fatalf("failAt=%d: injected keyring failure was swallowed", failAt)
		}
		if opsUsed < failAt {
			break
		}
	}
}

// TestStoreKeyringMergesFreshLegacyWriteFromOldBinary covers the mixed-version
// window: after migration to the indexed format, an old binary still running
// can save a token into the legacy combined entry. The next new-binary write
// must merge that fresh token instead of deleting the legacy entry over it.
func TestStoreKeyringMergesFreshLegacyWriteFromOldBinary(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}

	// An old binary saves token "carol" through the legacy combined entry.
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("carol"): {AccessToken: "c", RefreshToken: "cr"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	// The next new-binary save must keep carol, not silently lose it.
	if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta", "carol"} {
		if _, ok, err := s.Load(ProviderKey(name)); err != nil || !ok {
			t.Fatalf("Load(%s): ok=%v err=%v (fresh legacy write lost)", name, ok, err)
		}
	}
	// Presence alone doesn't rule out the merge corrupting carol's credential
	// material; check the actual values survived the legacy->indexed merge.
	if got, _, err := s.Load(ProviderKey("carol")); err != nil || got.AccessToken != "c" || got.RefreshToken != "cr" {
		t.Fatalf("Load(carol) = %#v, err=%v, want the legacy access/refresh tokens intact", got, err)
	}
	if _, ok := kr.data[keyringService+"/"+keyringLegacyAccount]; ok {
		t.Fatal("legacy entry should be removed once its fresh writes are merged")
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

// TestStoreKeyringWithLockRefreshesLease guards the stale-reclaim race: one
// keyring command can take up to 10s and a multi-entry pass runs several, so
// a lock held for a legitimately slow operation can outlive the fixed 30s
// stale threshold. withLock must keep the lock file's mtime fresh while its
// critical section runs, so only a genuinely crashed holder ever looks stale.
func TestStoreKeyringWithLockRefreshesLease(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "oauth-keyring.lockfile")
	blob := keyringBlob{kr: newFakeKR(), service: "zero-test", indexAccount: "idx", lockPath: lockPath}

	previous := fileLockRefreshInterval
	fileLockRefreshInterval = 20 * time.Millisecond
	defer func() { fileLockRefreshInterval = previous }()

	var first, second time.Time
	err := blob.withLock(time.Now, func() error {
		info, err := os.Stat(lockPath)
		if err != nil {
			return err
		}
		first = info.ModTime()
		time.Sleep(150 * time.Millisecond)
		info, err = os.Stat(lockPath)
		if err != nil {
			return err
		}
		second = info.ModTime()
		return nil
	})
	if err != nil {
		t.Fatalf("withLock: %v", err)
	}
	if !second.After(first) {
		t.Fatalf("lock mtime was not refreshed during the critical section: %v then %v", first, second)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file not released: %v", err)
	}
}

// TestStoreFileLoadToleratesCrashedWriterLock: file-backend reads must stay
// lock-free. A writer that crashed after taking the lock leaves a fresh lock
// file behind; the store file itself is always complete (writes are atomic
// renames), so Load must read it rather than waiting out the lock and
// failing for the ~30 seconds the stale threshold takes to expire.
func TestStoreFileLoadToleratesCrashedWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	s, err := NewStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("demo"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	// Simulate the crashed writer: a fresh, never-released lock file.
	if err := os.WriteFile(path+".lockfile", []byte("someone-else"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, ok, err := s.Load(ProviderKey("demo"))
	if err != nil || !ok || got.AccessToken != "a" {
		t.Fatalf("Load behind a crashed writer's lock: ok=%v err=%v token=%#v", ok, err, got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Load waited on the write lock (%v); reads must be lock-free", elapsed)
	}
	statusStart := time.Now()
	statuses, err := s.Status("")
	if err != nil || len(statuses) != 1 {
		t.Fatalf("Status behind a crashed writer's lock: %v (%d entries)", err, len(statuses))
	}
	if elapsed := time.Since(statusStart); elapsed > 2*time.Second {
		t.Fatalf("Status waited on the write lock (%v); reads must be lock-free", elapsed)
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

// TestStoreKeyringMigrationInterruptionsPreserveLegacyTokens drives the initial
// legacy->indexed migration through an injected failure at every mutating
// operation and checks that no pre-existing legacy credential is ever lost.
// write() publishes the index before the per-key entries, so a crash after the
// index appears but before an entry is written must still leave that token
// readable in the not-yet-deleted legacy blob; read() recovers it, and a
// following unimpeded save completes the migration.
func TestStoreKeyringMigrationInterruptionsPreserveLegacyTokens(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seeded := map[string]Token{
		ProviderKey("demo"):  {AccessToken: "demo-a", RefreshToken: "demo-r"},
		ProviderKey("other"): {AccessToken: "other-a"},
	}
	for failAt := 1; ; failAt++ {
		kr := &failingKR{fakeKR: newFakeKR()}
		// A legacy-only install: one combined entry, no index yet.
		legacyData, err := json.Marshal(storeFile{SchemaVersion: storeSchemaVersion, Tokens: seeded})
		if err != nil {
			t.Fatal(err)
		}
		kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(legacyData)

		s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		kr.ops = 0
		kr.failAt = failAt
		_ = s.Save(ProviderKey("new"), Token{AccessToken: "new-c"})
		opsUsed := kr.ops
		kr.failAt = 0

		// Regardless of where the migration was interrupted, a subsequent
		// unimpeded save must complete it with every token intact.
		if err := s.Save(ProviderKey("new"), Token{AccessToken: "new-c"}); err != nil {
			t.Fatalf("failAt=%d: reconciling Save: %v", failAt, err)
		}
		for key, want := range seeded {
			got, ok, err := s.Load(key)
			if err != nil || !ok {
				t.Fatalf("failAt=%d: Load(%s) after migration: ok=%v err=%v (legacy token lost)", failAt, key, ok, err)
			}
			if got.AccessToken != want.AccessToken {
				t.Fatalf("failAt=%d: Load(%s) = %q, want %q", failAt, key, got.AccessToken, want.AccessToken)
			}
		}
		if got, ok, err := s.Load(ProviderKey("new")); err != nil || !ok || got.AccessToken != "new-c" {
			t.Fatalf("failAt=%d: Load(new): ok=%v err=%v token=%#v", failAt, ok, err, got)
		}
		// The completed migration drops the legacy entry.
		if _, ok := kr.data[keyringService+"/"+keyringLegacyAccount]; ok {
			t.Fatalf("failAt=%d: legacy entry not removed after migration completed", failAt)
		}
		if opsUsed < failAt {
			break
		}
	}
}

// TestStoreKeyringMergesFreshLegacyRefreshOfIndexedKey covers the mixed-version
// window for a key that already exists in the index: an old binary refreshes
// provider:alpha in the legacy combined entry (a strictly later expiry). The
// next new-binary save must keep that fresher refresh instead of overwriting it
// with the stale indexed value and then deleting the legacy entry.
func TestStoreKeyringMergesFreshLegacyRefreshOfIndexedKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(1 * time.Hour)
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a-old", RefreshToken: "r-old", ExpiresAt: stale}); err != nil {
		t.Fatal(err)
	}

	// An old binary refreshes alpha through the legacy combined entry, pushing
	// the expiry later than the indexed copy.
	fresh := stale.Add(1 * time.Hour)
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "a-new", RefreshToken: "r-new", ExpiresAt: fresh},
	}}
	legacyData, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(legacyData)

	// A new-binary save of an unrelated key must reconcile alpha, not clobber it.
	if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load(ProviderKey("alpha"))
	if err != nil || !ok {
		t.Fatalf("Load(alpha): ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "a-new" || got.RefreshToken != "r-new" || !got.ExpiresAt.Equal(fresh) {
		t.Fatalf("Load(alpha) = %#v, want the refreshed legacy value (tokens and expiry) with ExpiresAt=%v", got, fresh)
	}
	if _, ok, _ := s.Load(ProviderKey("beta")); !ok {
		t.Fatal("Load(beta): not stored")
	}
	if _, ok := kr.data[keyringService+"/"+keyringLegacyAccount]; ok {
		t.Fatal("legacy entry should be removed once its refresh is merged")
	}
}

// TestAcquireFileLockDeadlineUsesWallClockNotInjectedClock guards the other
// half of the same hazard as the lease test below: acquireFileLock's own
// acquisition deadline must be measured against the real wall clock, not the
// injectable now parameter. StoreOptions.Now may legitimately be a fixed
// clock (as this test uses), and deadline := now().Add(fileLockTimeout)
// followed by now().After(deadline) would then never become true, so a
// contested lock would retry forever instead of returning a timeout error.
func TestAcquireFileLockDeadlineUsesWallClockNotInjectedClock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lockfile")
	// A fresh (non-stale) lock held by someone else: acquireFileLock must not
	// reclaim it, only wait out fileLockTimeout and report a timeout.
	if err := os.WriteFile(lockPath, []byte("someone-else"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	start := time.Now()
	_, err := acquireFileLock(lockPath, func() time.Time { return fixed })
	if err == nil {
		t.Fatal("expected a timeout error acquiring an already-held, non-stale lock")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("acquireFileLock took %v with a fixed clock; the deadline must use the wall clock, not now()", elapsed)
	}
}

// TestStoreKeyringLeaseUsesWallClockNotStoreClock guards the lock lease against
// a fixed or stale StoreOptions.Now. acquireFileLock judges staleness with real
// time.Since(mtime), so the lease must stamp the live lock with wall-clock time;
// leasing with an old injectable clock would let a peer immediately reclaim the
// held lock and re-enter the keyring read-modify-write concurrently.
func TestStoreKeyringLeaseUsesWallClockNotStoreClock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "oauth-keyring.lockfile")
	blob := keyringBlob{kr: newFakeKR(), service: "zero-test", indexAccount: "idx", lockPath: lockPath}

	previous := fileLockRefreshInterval
	fileLockRefreshInterval = 20 * time.Millisecond
	defer func() { fileLockRefreshInterval = previous }()

	// A deliberately stale, fixed clock: if the lease used it, the lock mtime
	// would land decades in the past and look stale immediately.
	fixed := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	var mtime time.Time
	err := blob.withLock(func() time.Time { return fixed }, func() error {
		time.Sleep(150 * time.Millisecond)
		info, statErr := os.Stat(lockPath)
		if statErr != nil {
			return statErr
		}
		mtime = info.ModTime()
		return nil
	})
	if err != nil {
		t.Fatalf("withLock: %v", err)
	}
	if time.Since(mtime) > fileLockStaleAfter {
		t.Fatalf("lease stamped the lock with the store clock (%v); a peer would reclaim the live lock", mtime)
	}
}

// countingKR counts Get calls so a test can prove a corrupt index is rejected
// before it fans out into a keyring lookup per advertised chunk.
type countingKR struct {
	*fakeKR
	gets int
}

func (c *countingKR) Get(service, account string) (string, bool, error) {
	c.gets++
	return c.fakeKR.Get(service, account)
}

// TestStoreKeyringReadIndexRejectsCorruptHeader is the regression test for an
// index header whose advertised chunk count is unbounded: readKeyIndex must
// reject an out-of-range or unsupported header up front rather than issue up to
// that many blocking keyring lookups while holding the store lock.
func TestStoreKeyringReadIndexRejectsCorruptHeader(t *testing.T) {
	ckr := &countingKR{fakeKR: newFakeKR()}
	blob := keyringBlob{kr: ckr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount}

	oversized, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: 1_000_000_000, Keys: []string{ProviderKey("demo")}})
	if err != nil {
		t.Fatal(err)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(oversized)
	ckr.gets = 0
	if _, _, _, err := blob.readKeyIndex(); err == nil {
		t.Fatal("expected an oversized chunk count to be rejected")
	}
	if ckr.gets != 1 {
		t.Fatalf("readKeyIndex issued %d keyring gets on a corrupt header; it must reject before fanning out over chunks", ckr.gets)
	}

	unsupported, err := json.Marshal(keyIndexHeader{Version: 2, Chunks: 1, Keys: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(unsupported)
	ckr.gets = 0
	if _, _, _, err := blob.readKeyIndex(); err == nil {
		t.Fatal("expected an unsupported index version to be rejected")
	}
	if ckr.gets != 1 {
		t.Fatalf("readKeyIndex issued %d gets for an unsupported version; want header lookup only", ckr.gets)
	}
}

// TestStoreKeyringReadIndexRejectsOversizedKeyList regresses a corrupt index
// that claims more keys than maxKeyringIndexKeys: maxKeyringIndexChunks alone
// bounds only how many chunk entries are fetched, not how many keys a single
// chunk's JSON (or the legacy bare-array format) can claim, so without this
// check readKeyIndex would hand read()/write() an oversized key list to fan
// out into one blocking kr.Get per key while holding the store lock.
func TestStoreKeyringReadIndexRejectsOversizedKeyList(t *testing.T) {
	ckr := &countingKR{fakeKR: newFakeKR()}
	blob := keyringBlob{kr: ckr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount}

	tooMany := make([]string, maxKeyringIndexKeys+1)
	for i := range tooMany {
		tooMany[i] = ProviderKey(fmt.Sprintf("p%d", i))
	}

	header, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: 1, Keys: tooMany})
	if err != nil {
		t.Fatal(err)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(header)
	ckr.gets = 0
	if _, _, _, err := blob.readKeyIndex(); err == nil {
		t.Fatal("expected an oversized key list in a chunk-0 header to be rejected")
	}
	if ckr.gets != 1 {
		t.Fatalf("readKeyIndex issued %d gets for an oversized header keys list; want header lookup only", ckr.gets)
	}

	// The pre-chunking bare-array format must be capped the same way.
	legacyArray, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(legacyArray)
	ckr.gets = 0
	if _, _, _, err := blob.readKeyIndex(); err == nil {
		t.Fatal("expected an oversized legacy-format key array to be rejected")
	}
	if ckr.gets != 1 {
		t.Fatalf("readKeyIndex issued %d gets for an oversized legacy array; want header lookup only", ckr.gets)
	}

	// Accumulation across continuation chunks must hit the same total cap: a
	// small header plus an oversized chunk-1 would otherwise fan out past the
	// bound after the per-header check has already passed.
	headerOK, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: 2, Keys: []string{ProviderKey("seed")}})
	if err != nil {
		t.Fatal(err)
	}
	chunk1, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(headerOK)
	ckr.data[keyringService+"/"+keyringIndexAccount+"-1"] = base64.StdEncoding.EncodeToString(chunk1)
	ckr.gets = 0
	if _, _, _, err := blob.readKeyIndex(); err == nil {
		t.Fatal("expected an oversized key list accumulated across chunks to be rejected")
	}
	if ckr.gets != 2 {
		t.Fatalf("readKeyIndex issued %d gets for a header+oversize-chunk; want header and chunk-1 only", ckr.gets)
	}
}

// TestKeyringFallbackLockPathIsPerUser covers the fallback taken when no config
// location resolves. It must not be the single shared temp path that any account
// on a multi-user host could pre-create or hold, and the last-resort temp name
// must be scoped by uid so different users never collide on one lock file.
func TestKeyringFallbackLockPathIsPerUser(t *testing.T) {
	got := keyringFallbackLockPath()
	if got == filepath.Join(os.TempDir(), "zero-oauth-keyring.lockfile") {
		t.Fatalf("fallback lock path is the shared temp path %q; a co-tenant could grief it", got)
	}
	if cache, err := os.UserCacheDir(); err == nil && strings.TrimSpace(cache) != "" {
		if want := filepath.Join(cache, "zero", "oauth-keyring.lockfile"); got != want {
			t.Fatalf("fallback = %q, want per-user cache path %q", got, want)
		}
	}
	name := keyringTempLockName()
	if uid := os.Getuid(); uid >= 0 {
		if !strings.Contains(name, fmt.Sprintf("%d", uid)) {
			t.Fatalf("temp lock name %q is not scoped by uid %d", name, uid)
		}
	} else if name == "" {
		t.Fatal("temp lock name is empty")
	}
}

// legacyDeleteFailKR fails Delete for the legacy combined entry only, to
// exercise the boundary where a logout has already rewritten the indexed
// state but the stale legacy blob cannot be removed.
type legacyDeleteFailKR struct {
	*fakeKR
	fail bool
}

func (f *legacyDeleteFailKR) Delete(service, account string) (bool, error) {
	if f.fail && account == keyringLegacyAccount {
		return false, errKRInjected
	}
	return f.fakeKR.Delete(service, account)
}

// TestStoreKeyringLogoutSurfacesLegacyBlobDeleteFailure: when the final
// legacy-blob delete fails during a logout, the operation must report the
// failure (not success with the secret still resident), and after a clean
// retry the logged-out credential must not be resurrected by a later save.
func TestStoreKeyringLogoutSurfacesLegacyBlobDeleteFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := &legacyDeleteFailKR{fakeKR: newFakeKR()}
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	// A leftover legacy blob still carries alpha (e.g. written by an old
	// binary during the upgrade window).
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "stale-a"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	kr.fail = true
	if _, err := s.Delete(ProviderKey("alpha")); err == nil {
		t.Fatal("Delete reported success although the stale legacy blob could not be removed")
	}

	// A clean retry succeeds, and a later save must not classify the stale
	// legacy alpha as a fresh old-binary login.
	kr.fail = false
	if _, err := s.Delete(ProviderKey("alpha")); err != nil {
		t.Fatalf("retried Delete: %v", err)
	}
	if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || ok {
		t.Fatalf("logged-out credential resurrected: ok=%v err=%v", ok, err)
	}
}

// TestStoreKeyringWriteIndexRejectsOverCapChunks: writeKeyIndex must refuse
// to publish an index header that readKeyIndex would reject, instead of
// persisting a store no later operation can open.
func TestStoreKeyringWriteIndexRejectsOverCapChunks(t *testing.T) {
	kr := newFakeKR()
	b := keyringBlob{kr: kr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount}
	// Each key exceeds the per-chunk byte budget on its own, forcing one
	// chunk per key.
	long := strings.Repeat("k", maxKeyringIndexChunkBytes)
	keys := make([]string, maxKeyringIndexChunks+1)
	for i := range keys {
		keys[i] = fmt.Sprintf("%s-%d", long, i)
	}
	if _, err := b.writeKeyIndex(keys, 0); err == nil {
		t.Fatal("writeKeyIndex published an index readKeyIndex would refuse")
	}
	if len(kr.data) != 0 {
		t.Fatalf("over-cap index write must publish nothing, found %d entries", len(kr.data))
	}
}

// TestStoreKeyringWriteIndexRejectsOverCapKeys: short keys can still fit under
// the chunk-count cap while exceeding maxKeyringIndexKeys. writeKeyIndex must
// refuse that set before publishing, matching the reader-side total key cap.
func TestStoreKeyringWriteIndexRejectsOverCapKeys(t *testing.T) {
	kr := newFakeKR()
	b := keyringBlob{kr: kr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount}
	keys := make([]string, maxKeyringIndexKeys+1)
	for i := range keys {
		// Short keys pack densely into chunks so the chunk-count check alone
		// would not catch this over-cap set.
		keys[i] = fmt.Sprintf("p%d", i)
	}
	if _, err := b.writeKeyIndex(keys, 0); err == nil {
		t.Fatal("writeKeyIndex published a key count readKeyIndex would refuse")
	}
	if len(kr.data) != 0 {
		t.Fatalf("over-cap key write must publish nothing, found %d entries", len(kr.data))
	}
}
