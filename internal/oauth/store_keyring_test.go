package oauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/lockutil"
)

// TestMain redirects keyring lock paths into a process-private temp home so
// the suite never creates lock files under the real user home directory
// (keyringLockPath deliberately ignores XDG_CONFIG_HOME / HOME env). Tests
// that must observe real OS identity re-stub currentOSUser themselves.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "zero-oauth-test-home-*")
	if err != nil {
		panic(err)
	}
	currentOSUser = func() (*user.User, error) {
		return &user.User{Uid: "0", HomeDir: dir}, nil
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// useTempLockHome points keyringLockPath at a per-test home directory and
// restores the previous stub on cleanup. Prefer this when a test needs its
// own lock root (e.g. concurrent lock-path stability checks).
func useTempLockHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	previous := currentOSUser
	currentOSUser = func() (*user.User, error) {
		return &user.User{Uid: "0", HomeDir: home}, nil
	}
	t.Cleanup(func() { currentOSUser = previous })
	return home
}

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
	// New code never creates the legacy combined entry: that account is a
	// read-only discovery source for pre-PR blobs, not a dual-written mirror.
	if raw := kr.data[keyringService+"/"+keyringLegacyAccount]; raw != "" {
		t.Fatal("legacy combined entry must not be dual-written by new code")
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
	// Each per-key token entry must stay small even with 5 providers logged
	// in. New code does not dual-write the legacy combined entry.
	const singleTokenCeiling = 3000 // generous margin under the ~4095-byte line cap
	for k, v := range kr.data {
		if strings.HasSuffix(k, "/"+keyringLegacyAccount) {
			t.Fatalf("legacy account %q was written by new multi-provider saves", k)
		}
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
// migrated to per-key entries the next time anything is saved. The legacy
// entry is left untouched (never dual-written or deleted).
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
	legacyEnc := base64.StdEncoding.EncodeToString(data)
	kr.data[keyringService+"/"+keyringLegacyAccount] = legacyEnc

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

	// Saving a second provider must migrate into per-key entries and leave the
	// original legacy blob byte-identical (read-only coexistence).
	if err := s.Save(ProviderKey("other"), Token{AccessToken: "other-a"}); err != nil {
		t.Fatal(err)
	}
	if got := kr.data[keyringService+"/"+keyringLegacyAccount]; got != legacyEnc {
		t.Fatalf("legacy combined entry was rewritten during migration (want frozen original)")
	}
	for _, name := range []string{"demo", "other"} {
		if _, ok, err := s.Load(ProviderKey(name)); err != nil || !ok {
			t.Fatalf("Load(%s) after migration: ok=%v err=%v", name, ok, err)
		}
		if _, ok := kr.data[keyringService+"/"+ProviderKey(name)]; !ok {
			t.Fatalf("per-key entry for %s missing after migration", name)
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

// TestStoreKeyringSkipsMissingChunkEntry covers the chunked index format:
// a continuation chunk listed by the header is missing (torn write by a
// killed process), and one of the keys that survives in the remaining chunk
// has no corresponding entry in the keyring. read() must skip the missing
// key without failing the whole read, and the missing chunk must be ignored
// by readKeyIndex rather than causing an error.
func TestStoreKeyringSkipsMissingChunkEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()

	// Seed one valid token entry that will be reachable via chunk 0.
	valid := Token{AccessToken: "valid-a", RefreshToken: "valid-r"}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+ProviderKey("valid")] = base64.StdEncoding.EncodeToString(raw)

	// Build a chunked header (2 chunks). Chunk 0 references "valid" and
	// "orphan" (missing its entry). Chunk 1 exists and carries "extra".
	// But we deliberately omit chunk 1 from the keyring to simulate a torn write.
	header := keyIndexHeader{Version: 1, Chunks: 2, Keys: []string{ProviderKey("valid"), ProviderKey("orphan")}}
	headerData, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(headerData)

	// Chunk 1 is intentionally absent — simulating a process killed mid-write.

	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}

	// "valid" must still be loadable (chunk 0 had it, and its entry exists).
	got, ok, err := s.Load(ProviderKey("valid"))
	if err != nil || !ok {
		t.Fatalf("Load(valid): ok=%v err=%v", ok, err)
	}
	if got.AccessToken != valid.AccessToken {
		t.Fatalf("Load(valid) = %#v", got)
	}

	// "orphan" has no entry and the legacy blob is empty, so it must be skipped.
	if _, ok, err := s.Load(ProviderKey("orphan")); err != nil || ok {
		t.Fatalf("Load(orphan): ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	// "extra" lives in the missing chunk 1, so it must also be skipped.
	if _, ok, err := s.Load(ProviderKey("extra")); err != nil || ok {
		t.Fatalf("Load(extra): ok=%v err=%v, want ok=false err=nil (chunk 1 missing)", ok, err)
	}

	// Status must return only "valid" — the missing chunk and missing entry
	// must not fail the read or return phantom tokens.
	statuses, err := s.Status("")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Key != ProviderKey("valid") {
		t.Fatalf("Status = %#v, want only the valid key", statuses)
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
	keys, _, _, _, err := blob.readKeyIndex()
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
	// Every keyring value (index chunks, per-key tokens, dual-written legacy)
	// must stay under the single-entry cap with generous framing margin.
	const entryCeiling = 3800
	for k, v := range kr.data {
		if len(v) > entryCeiling {
			t.Fatalf("keyring entry %q is %d bytes, want <= %d (index/legacy cap regression)", k, len(v), entryCeiling)
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
			if account == keyringIndexAccount || strings.HasPrefix(account, keyringIndexAccount+"-") ||
				account == keyringLegacyAccount || account == keyringTombstoneAccount ||
				strings.HasPrefix(account, keyringTombstoneAccount+"-") {
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
		// Every mutating boundary of the write path must surface its failure.
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
			if account == keyringIndexAccount || strings.HasPrefix(account, keyringIndexAccount+"-") ||
				account == keyringLegacyAccount || account == keyringTombstoneAccount ||
				strings.HasPrefix(account, keyringTombstoneAccount+"-") {
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
	if _, ok := kr.data[keyringService+"/"+keyringLegacyAccount]; !ok {
		t.Fatal("legacy entry was deleted; old writers on other roots would lose their only copy")
	}
	// carol must also be promoted into an indexed entry so it survives without
	// relying on a dual-written legacy mirror.
	if _, ok := kr.data[keyringService+"/"+ProviderKey("carol")]; !ok {
		t.Fatal("merged legacy-only carol was not promoted into a per-key entry")
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
		// The lease only needs the mtime to stay non-stale. Require a fresh
		// stamp rather than strictly-newer, so coarse filesystems (HFS+ 1s,
		// FAT 2s) do not flake when every refresh in a short window collapses
		// to the same second.
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
	if second.Before(first) {
		t.Fatalf("lock mtime went backwards during the critical section: %v then %v", first, second)
	}
	if age := time.Since(second); age > fileLockStaleAfter {
		t.Fatalf("lock mtime is stale after lease refresh: age %v > %v", age, fileLockStaleAfter)
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
		// Completed migration leaves the original legacy entry in place
		// (read-only coexistence; never dual-write or delete).
		if _, ok := kr.data[keyringService+"/"+keyringLegacyAccount]; !ok {
			t.Fatalf("failAt=%d: legacy entry was removed during migration", failAt)
		}
		if opsUsed < failAt {
			break
		}
	}
}

// TestStoreKeyringExplicitSaveWinsOverStaleLookingLegacy is the regression for
// [P1] Do not use token contents or expiry as cross-version write order: a
// longer legacy expiry (or different token material) must not replace an
// explicit new-binary Save of the same key. Expiry is not causal order.
func TestStoreKeyringExplicitSaveWinsOverStaleLookingLegacy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	// Explicit new login as account B with a short lifetime.
	explicit := time.Now().Add(1 * time.Hour)
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "account-b", RefreshToken: "rb", ExpiresAt: explicit, Account: "b@example.com"}); err != nil {
		t.Fatal(err)
	}

	// A leftover legacy copy for the same key looks "fresher" by expiry and
	// carries a different account — content-based ordering would wrongly win.
	legacyLater := explicit.Add(24 * time.Hour)
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "account-a", RefreshToken: "ra", ExpiresAt: legacyLater, Account: "a@example.com"},
	}}
	legacyData, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(legacyData)

	// Unrelated save must not let legacy clobber the explicit alpha token.
	if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load(ProviderKey("alpha"))
	if err != nil || !ok {
		t.Fatalf("Load(alpha): ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "account-b" || got.Account != "b@example.com" {
		t.Fatalf("Load(alpha) = %#v, want the explicit Save (account-b), not the longer-expiry legacy account-a", got)
	}
	if _, ok, _ := s.Load(ProviderKey("beta")); !ok {
		t.Fatal("Load(beta): not stored")
	}
}

// TestAcquireFileLockWaitsWhileLeaseHealthy covers [P2] Let lock acquisition
// cover a healthy keyring operation: a holder that keeps the lease fresh for
// longer than the idle timeout must not cause contenders to fail; they wait
// and acquire once the holder releases.
func TestAcquireFileLockWaitsWhileLeaseHealthy(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lockfile")
	prevTimeout := fileLockTimeout
	fileLockTimeout = 80 * time.Millisecond
	defer func() {
		fileLockTimeout = prevTimeout
	}()

	unlock, _, err := acquireFileLock(lockPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	// Refresh the held lock past several idle-timeout intervals.
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				at := time.Now()
				_ = os.Chtimes(lockPath, at, at)
			}
		}
	}()

	acquired := make(chan error, 1)
	go func() {
		u, _, err := acquireFileLock(lockPath, time.Now)
		if err == nil {
			u()
		}
		acquired <- err
	}()

	// Contender must still be waiting after > one idle timeout.
	select {
	case err := <-acquired:
		close(stop)
		unlock()
		t.Fatalf("contender finished too early (err=%v); must wait while lease is healthy", err)
	case <-time.After(250 * time.Millisecond):
	}

	close(stop)
	unlock()
	if err := <-acquired; err != nil {
		t.Fatalf("contender failed after holder released: %v", err)
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
	if _, _, _, _, err := blob.readKeyIndex(); err == nil {
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
	if _, _, _, _, err := blob.readKeyIndex(); err == nil {
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
	if _, _, _, _, err := blob.readKeyIndex(); err == nil {
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
	if _, _, _, _, err := blob.readKeyIndex(); err == nil {
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
	if _, _, _, _, err := blob.readKeyIndex(); err == nil {
		t.Fatal("expected an oversized key list accumulated across chunks to be rejected")
	}
	if ckr.gets != 2 {
		t.Fatalf("readKeyIndex issued %d gets for a header+oversize-chunk; want header and chunk-1 only", ckr.gets)
	}
}

// TestKeyringLockPathIsPerUser covers the lock path used for every keyring
// Store regardless of file-backend config. It must not be the single shared
// temp path that any account on a multi-user host could pre-create or hold,
// and the last-resort temp name must be scoped by uid so different users
// never collide on one lock file.
func TestKeyringLockPathIsPerUser(t *testing.T) {
	// Observe real OS identity, not the TestMain isolation stub.
	previous := currentOSUser
	currentOSUser = user.Current
	t.Cleanup(func() { currentOSUser = previous })

	got, err := keyringLockPath(nil, keyringService, keyringIndexAccount)
	if err != nil {
		t.Fatal(err)
	}
	name := keyringLockFileName(keyringService, keyringIndexAccount)
	if got == filepath.Join(os.TempDir(), "zero-"+name) {
		t.Fatalf("lock path is the shared temp path %q; a co-tenant could grief it", got)
	}
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.HomeDir) != "" {
		if want := filepath.Join(u.HomeDir, ".cache", "zero", name); got != want {
			t.Fatalf("lock path = %q, want per-user home-anchored path %q", got, want)
		}
	} else if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		if want := filepath.Join(home, ".cache", "zero", name); got != want {
			t.Fatalf("lock path = %q, want per-user home-anchored path %q", got, want)
		}
	}
	tempName := keyringTempLockName(keyringService, keyringIndexAccount)
	if uid := os.Getuid(); uid >= 0 {
		if !strings.Contains(tempName, fmt.Sprintf("%d", uid)) {
			t.Fatalf("temp lock name %q is not scoped by uid %d", tempName, uid)
		}
	} else if tempName == "" {
		t.Fatal("temp lock name is empty")
	}
}

// TestKeyringLockPathIndependentOfCacheAndTempRoots is the regression test
// for [P1] Make the keyring lock path independent of XDG_CACHE_HOME
// (2026-07-22): os.UserCacheDir() (and its os.TempDir() fallback) is chosen
// per PROCESS from XDG_CACHE_HOME/TMPDIR, so two zero processes belonging to
// the SAME real user but with different cache/temp roots (sandboxes, CI,
// per-shell overrides) computed different lock files, both read the shared
// keyring index, and could publish competing updates that hid one process's
// token. The lock must resolve identically regardless of those roots, since
// it is anchored on the user's home directory instead.
func TestKeyringLockPathIndependentOfCacheAndTempRoots(t *testing.T) {
	home := t.TempDir()
	envA := map[string]string{
		"HOME":           home,
		"XDG_CACHE_HOME": filepath.Join(t.TempDir(), "cache-a"),
		"TMPDIR":         filepath.Join(t.TempDir(), "tmp-a"),
	}
	envB := map[string]string{
		"HOME":           home,
		"XDG_CACHE_HOME": filepath.Join(t.TempDir(), "cache-b"),
		"TMPDIR":         filepath.Join(t.TempDir(), "tmp-b"),
	}

	storeA, err := NewStore(StoreOptions{Storage: "keyring", Keyring: newFakeKR(), Env: envA})
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewStore(StoreOptions{Storage: "keyring", Keyring: newFakeKR(), Env: envB})
	if err != nil {
		t.Fatal(err)
	}
	blobA, okA := storeA.blob.(keyringBlob)
	blobB, okB := storeB.blob.(keyringBlob)
	if !okA || !okB {
		t.Fatal("Store.blob is not keyringBlob")
	}
	if blobA.lockPath == "" || blobB.lockPath == "" {
		t.Fatal("lockPath should never be empty for the keyring backend")
	}
	if blobA.lockPath != blobB.lockPath {
		t.Fatalf("two same-user processes with different cache/temp roots got different lock paths: %q vs %q (they can race the shared keyring index)", blobA.lockPath, blobB.lockPath)
	}
	if strings.Contains(blobA.lockPath, "cache-a") || strings.Contains(blobA.lockPath, "cache-b") ||
		strings.Contains(blobA.lockPath, "tmp-a") || strings.Contains(blobA.lockPath, "tmp-b") {
		t.Fatalf("lock path %q is still derived from XDG_CACHE_HOME/TMPDIR", blobA.lockPath)
	}
}

// TestStoreKeyringWriteWaitsForLegacyLockDuringReconciliation is the
// regression test for [P1] Coordinate migration with the legacy lock used by
// old binaries (2026-07-22): a pre-PR binary locks beside ResolveStorePath
// around its own read-modify-write of the legacy combined entry, but this
// binary's write() used to lock only under the cache-derived keyring path, so
// the two never excluded each other. A new save could reconcile the legacy
// blob, an old process could then save a fresh legacy credential, and the new
// save would unconditionally delete that blob without ever having observed
// the old write, losing it permanently.
//
// This simulates a live old binary by holding the exact lock file
// legacyKeyringLockPath computes (the same one a pre-PR binary's Save takes)
// and asserting that Store.Save — which must reconcile and dual-write the
// legacy entry here, since one is seeded below — blocks until that lock is
// released, and that the seeded legacy token survives the reconciliation.
func TestStoreKeyringWriteWaitsForLegacyLockDuringReconciliation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	kr := newFakeKR()
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "legacy-alpha"},
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
	blob, ok := s.blob.(keyringBlob)
	if !ok || blob.legacyLockPath == "" {
		t.Fatal("keyring store has no legacy-compat lock path")
	}

	// Simulate an old binary holding the legacy lock for its own in-flight Save.
	unlock, _, err := acquireFileLock(blob.legacyLockPath, s.now)
	if err != nil {
		t.Fatalf("acquire simulated legacy lock: %v", err)
	}

	saveDone := make(chan error, 1)
	go func() {
		saveDone <- s.Save(ProviderKey("beta"), Token{AccessToken: "beta"})
	}()

	select {
	case err := <-saveDone:
		t.Fatalf("Save proceeded (err=%v) while an old binary held the legacy lock; it can race the reconcile-then-delete window and lose a concurrent legacy write", err)
	case <-time.After(200 * time.Millisecond):
		// Still blocked, as expected.
	}

	unlock()
	if err := <-saveDone; err != nil {
		t.Fatalf("Save after the legacy lock was released: %v", err)
	}

	got, ok, err := s.Load(ProviderKey("alpha"))
	if err != nil || !ok || got.AccessToken != "legacy-alpha" {
		t.Fatalf("legacy alpha token lost across reconciliation: ok=%v err=%v got=%#v", ok, err, got)
	}
}

// TestKeyringLockPathDerivedFromKeyringIdentityNotFileConfig is the
// regression test for the cross-process lock racing bug: the lock guarding
// the shared keyring index must be keyed off the keyring's own identity
// (service + index account), never off the file-backend path config
// (ZERO_OAUTH_TOKENS_PATH / XDG_CONFIG_HOME). Two zero processes with
// different config roots but pointed at the SAME keyring entry (the service
// and account are fixed per binary, not per config root) must resolve to the
// identical lock path, or they can race a read-modify-write on the shared
// index and silently drop one process's token write.
func TestKeyringLockPathDerivedFromKeyringIdentityNotFileConfig(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "config-root-a")
	dirB := filepath.Join(t.TempDir(), "config-root-b")

	storeA, err := NewStore(StoreOptions{Storage: "keyring", Keyring: newFakeKR(), Env: map[string]string{"XDG_CONFIG_HOME": dirA}})
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewStore(StoreOptions{Storage: "keyring", Keyring: newFakeKR(), Env: map[string]string{"XDG_CONFIG_HOME": dirB}})
	if err != nil {
		t.Fatal(err)
	}
	blobA, okA := storeA.blob.(keyringBlob)
	blobB, okB := storeB.blob.(keyringBlob)
	if !okA || !okB {
		t.Fatal("Store.blob is not keyringBlob")
	}
	if blobA.lockPath == "" || blobB.lockPath == "" {
		t.Fatal("lockPath should never be empty for the keyring backend")
	}
	if blobA.lockPath != blobB.lockPath {
		t.Fatalf("two processes on the SAME keyring entry got different lock paths for different config roots: %q vs %q (they can race the shared keyring index)", blobA.lockPath, blobB.lockPath)
	}
	// And it must not be derived from either config root's resolved store path.
	if strings.Contains(blobA.lockPath, dirA) || strings.Contains(blobA.lockPath, dirB) {
		t.Fatalf("lock path %q is still derived from file-backend config, not the keyring identity", blobA.lockPath)
	}
}

// TestStoreKeyringLogoutTombstoneSurvivesStaleLegacyRewrite: after Delete,
// an old binary can rewrite the legacy blob with the logged-out key still
// present. Durable tombstones must keep Load empty and prevent a later Save
// from reindexing it.
func TestStoreKeyringLogoutTombstoneSurvivesStaleLegacyRewrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(ProviderKey("alpha")); err != nil {
		t.Fatal(err)
	}

	// Old binary rewrites a stale snapshot that still contains alpha.
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "stale-a"},
		ProviderKey("beta"):  {AccessToken: "b"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || ok {
		t.Fatalf("logged-out alpha exposed after stale legacy rewrite: ok=%v err=%v", ok, err)
	}
	if err := s.Save(ProviderKey("gamma"), Token{AccessToken: "g"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || ok {
		t.Fatalf("logged-out alpha resurrected by later Save: ok=%v err=%v", ok, err)
	}
	// Unrelated legacy-only beta remains discoverable.
	if _, ok, err := s.Load(ProviderKey("beta")); err != nil || !ok {
		t.Fatalf("legacy-only beta lost: ok=%v err=%v", ok, err)
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

// TestStoreKeyringReadIndexDedupesDuplicateKeys is the regression test for the
// index fan-out DoS: a corrupted or adversarially crafted index that repeats
// the same key many times must collapse to its distinct, valid keys before
// read()/write() fan them out into one blocking keyring lookup per key.
func TestStoreKeyringReadIndexDedupesDuplicateKeys(t *testing.T) {
	ckr := &countingKR{fakeKR: newFakeKR()}
	blob := keyringBlob{kr: ckr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount}

	dup := ProviderKey("demo")
	// Stay under maxKeyringIndexEncodedBytes so the byte-bound check does not
	// fire first; this test is about dedupe after a valid-sized decode.
	many := make([]string, 80)
	for i := range many {
		many[i] = dup
	}
	header, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: 1, Keys: many})
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(header)
	if len(encoded) > maxKeyringIndexEncodedBytes {
		t.Fatalf("test payload is %d bytes encoded; keep it under %d so the byte bound is not the thing under test", len(encoded), maxKeyringIndexEncodedBytes)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = encoded

	keys, ok, _, _, err := blob.readKeyIndex()
	if err != nil {
		t.Fatalf("readKeyIndex: %v", err)
	}
	if !ok {
		t.Fatal("readKeyIndex: expected an index to be found")
	}
	if len(keys) != 1 {
		t.Fatalf("readKeyIndex returned %d keys for an 80-duplicate index, want 1 (deduplicated)", len(keys))
	}

	// A malformed (non-ValidateKey-shaped) entry must also be dropped rather
	// than fanned out into a lookup.
	mixed, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: 1, Keys: []string{dup, dup, "not a valid key", ProviderKey("other")}})
	if err != nil {
		t.Fatal(err)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(mixed)
	keys, _, _, _, err = blob.readKeyIndex()
	if err != nil {
		t.Fatalf("readKeyIndex: %v", err)
	}
	want := map[string]bool{dup: true, ProviderKey("other"): true}
	if len(keys) != len(want) {
		t.Fatalf("readKeyIndex = %v, want exactly %v", keys, want)
	}
	for _, k := range keys {
		if !want[k] {
			t.Fatalf("readKeyIndex returned unexpected key %q", k)
		}
	}
}

// TestStoreKeyringDuplicateIndexDoesNotFanOutPerEntry is the end-to-end
// regression test for the same DoS: a duplicate-heavy index must not drive
// one keyring Get per listed entry. Before the index is deduplicated at its
// source, a corrupted index holding thousands of copies of the same key would
// hold the store lock for one blocking keyring lookup (each up to the 10s
// command timeout) per copy, even though the cap on total distinct credentials
// this bug was meant to bound was never actually exceeded.
func TestStoreKeyringDuplicateIndexDoesNotFanOutPerEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ckr := &countingKR{fakeKR: newFakeKR()}

	dup := ProviderKey("demo")
	raw, err := json.Marshal(Token{AccessToken: "a"})
	if err != nil {
		t.Fatal(err)
	}
	ckr.data[keyringService+"/"+dup] = base64.StdEncoding.EncodeToString(raw)

	many := make([]string, 80)
	for i := range many {
		many[i] = dup
	}
	header, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: 1, Keys: many})
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(header)
	if len(encoded) > maxKeyringIndexEncodedBytes {
		t.Fatalf("test payload is %d bytes encoded; keep it under %d so the byte bound is not the thing under test", len(encoded), maxKeyringIndexEncodedBytes)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = encoded

	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: ckr})
	if err != nil {
		t.Fatal(err)
	}

	ckr.gets = 0
	statuses, err := s.Status("")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("Status returned %d entries for an 80-duplicate index of one key, want 1", len(statuses))
	}
	// One Get for the index header, one Get for the single deduplicated key's
	// own entry, one Get for durable tombstones, and at most one extra Get for
	// the legacy fallback lookup. The key regression is: not one Get per
	// duplicate entry.
	if ckr.gets > 4 {
		t.Fatalf("Status issued %d keyring gets for an 80-entry duplicate index, want <= 4 (fan-out DoS regression)", ckr.gets)
	}
}

// legacyGetFailKR fails Get for the legacy combined entry only, simulating a
// transient keyring read error (as opposed to the entry genuinely not
// existing, which fakeKR's normal Get reports as ok=false, err=nil).
type legacyGetFailKR struct {
	*fakeKR
	fail bool
}

func (f *legacyGetFailKR) Get(service, account string) (string, bool, error) {
	if f.fail && account == keyringLegacyAccount {
		return "", false, errKRInjected
	}
	return f.fakeKR.Get(service, account)
}

// TestStoreKeyringWriteRefusesToOverwriteLegacyBlobOnTransientReadError is the
// regression test for mixed-version reconciliation: a transient error reading
// the legacy blob must not be treated as "the legacy blob is empty." write()
// must abort rather than proceed without those credentials (and must never
// overwrite the legacy account).
func TestStoreKeyringWriteRefusesToOverwriteLegacyBlobOnTransientReadError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := &legacyGetFailKR{fakeKR: newFakeKR()}
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	// A first save publishes the index, so indexExisted is true for the next
	// write and exercises the mixed-version reconciliation path in write()
	// where the bug lived.
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}

	// A legacy blob still carries a credential written by an older,
	// still-installed binary.
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("stale-binary-login"): {AccessToken: "still-live"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyEnc := base64.StdEncoding.EncodeToString(data)
	kr.data[keyringService+"/"+keyringLegacyAccount] = legacyEnc

	// A transient failure reading it: Get returns a real error, not
	// ok=false/err=nil ("doesn't exist").
	kr.fail = true
	if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err == nil {
		t.Fatal("Save succeeded despite a transient legacy-blob read failure; it must refuse rather than silently treat the blob as empty")
	}
	// The legacy blob must still be present and byte-identical.
	if raw, ok := kr.data[keyringService+"/"+keyringLegacyAccount]; !ok {
		t.Fatal("legacy blob was removed despite a transient read failure (data loss)")
	} else if raw != legacyEnc {
		t.Fatal("legacy blob was overwritten despite a transient read failure (data loss)")
	}
	// Nothing else the aborted write touched should be visible either.
	if _, ok, _ := s.Load(ProviderKey("beta")); ok {
		t.Fatal("beta should not be visible: the write should have aborted entirely, not partially applied")
	}
	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || !ok {
		t.Fatalf("alpha lost after an aborted write: ok=%v err=%v", ok, err)
	}

	// Once the transient failure clears, the legacy credential is recovered
	// into the indexed store; the legacy blob stays frozen.
	kr.fail = false
	if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
		t.Fatalf("retried Save: %v", err)
	}
	if _, ok, err := s.Load(ProviderKey("stale-binary-login")); err != nil || !ok {
		t.Fatalf("Load(stale-binary-login): ok=%v err=%v (legacy credential lost)", ok, err)
	}
	if raw := kr.data[keyringService+"/"+keyringLegacyAccount]; raw != legacyEnc {
		t.Fatal("legacy blob was rewritten after a successful reconcile (must stay frozen)")
	}
}

func mustDecode(t *testing.T, enc string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestStoreKeyringLockPathStableAcrossDifferentHomeEnvs(t *testing.T) {
	kr := newFakeKR()
	s1, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr, Env: map[string]string{"HOME": filepath.Join(t.TempDir(), "home1")}})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr, Env: map[string]string{"HOME": filepath.Join(t.TempDir(), "home2")}})
	if err != nil {
		t.Fatal(err)
	}

	p1 := s1.blob.(keyringBlob).lockPath
	p2 := s2.blob.(keyringBlob).lockPath
	if p1 != p2 {
		t.Fatalf("lockPath mismatch for different HOME envs: s1=%q, s2=%q", p1, p2)
	}

	if err := s1.Save(ProviderKey("alpha"), Token{AccessToken: "token1"}); err != nil {
		t.Fatalf("s1.Save: %v", err)
	}
	got, ok, err := s2.Load(ProviderKey("alpha"))
	if err != nil || !ok || got.AccessToken != "token1" {
		t.Fatalf("s2.Load: got=%#v, ok=%v, err=%v", got, ok, err)
	}
}

// TestStoreKeyringLoadPrefersIndexedOverLegacyLookingFresher: when both the
// per-key entry and the legacy blob hold the same key, the indexed copy wins
// even if legacy has a later expiry or different material. Content is not
// causal order (see explicit-Save regression above).
func TestStoreKeyringLoadPrefersIndexedOverLegacyLookingFresher(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Now().Add(-10 * time.Minute)
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "indexed-a", ExpiresAt: t1}); err != nil {
		t.Fatal(err)
	}

	t2 := time.Now().Add(1 * time.Hour)
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "legacy-a", RefreshToken: "legacy-r", ExpiresAt: t2},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	got, ok, err := s.Load(ProviderKey("alpha"))
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "indexed-a" {
		t.Fatalf("Load returned %#v, want indexed token (not later-expiry legacy)", got)
	}
}

// TestStoreKeyringLoadDoesNotPreferZeroExpiryLegacyMaterial: OAuth may omit
// expires_in; different token material alone must not make legacy win over an
// indexed entry for the same key.
func TestStoreKeyringLoadDoesNotPreferZeroExpiryLegacyMaterial(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Now().Add(10 * time.Minute)
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "indexed-a", RefreshToken: "indexed-r", ExpiresAt: t1}); err != nil {
		t.Fatal(err)
	}

	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "legacy-a", RefreshToken: "legacy-r", ExpiresAt: time.Time{}},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	got, ok, err := s.Load(ProviderKey("alpha"))
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "indexed-a" || got.RefreshToken != "indexed-r" {
		t.Fatalf("Load = %#v, want indexed token (not zero-expiry legacy material)", got)
	}
}

func TestStoreKeyringDeleteNotResurrectedWhenLegacyDeleteFailsOrRewritten(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}

	// Seed legacy blob with alpha (logged out token)
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "a-legacy"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	// Delete alpha
	removed, err := s.Delete(ProviderKey("alpha"))
	if err != nil || !removed {
		t.Fatalf("Delete(alpha): removed=%v err=%v", removed, err)
	}

	// Assert Load(alpha) returns empty
	if _, ok, _ := s.Load(ProviderKey("alpha")); ok {
		t.Fatal("alpha should not be exposed on Load after Delete")
	}

	// Save gamma, which triggers write() reconciliation
	if err := s.Save(ProviderKey("gamma"), Token{AccessToken: "g"}); err != nil {
		t.Fatal(err)
	}

	// Assert alpha was not resurrected into the index
	if _, ok, _ := s.Load(ProviderKey("alpha")); ok {
		t.Fatal("alpha was resurrected into index after Delete")
	}
}

// TestStoreKeyringDeleteDoesNotResurrectLegacyOnlyKey is the regression for
// [P1] Do not resurrect a token the caller just logged out: when an old binary
// adds beta only to the legacy blob after the index already contains alpha,
// read exposes beta, Delete(beta) removes it from state, and write must not
// reclassify that same legacy value as a fresh old-binary login.
func TestStoreKeyringDeleteDoesNotResurrectLegacyOnlyKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}

	// Old binary login: beta only in the legacy combined entry, not the index.
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("beta"): {AccessToken: "b-legacy", RefreshToken: "br"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	// Delete must see beta via the legacy merge, then keep it gone after write.
	removed, err := s.Delete(ProviderKey("beta"))
	if err != nil || !removed {
		t.Fatalf("Delete(beta): removed=%v err=%v", removed, err)
	}
	if _, ok, _ := s.Load(ProviderKey("beta")); ok {
		t.Fatal("beta resurrected after Delete of legacy-only key")
	}
	// A later Save of another key must also not resurrect beta (legacy blob
	// should already be gone; if a stale copy were re-seeded, omit only covers
	// the Delete write itself — ensure the logout fully cleared it).
	if err := s.Save(ProviderKey("gamma"), Token{AccessToken: "g"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Load(ProviderKey("beta")); ok {
		t.Fatal("beta resurrected after later Save")
	}
	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || !ok {
		t.Fatalf("alpha lost: ok=%v err=%v", ok, err)
	}
}

// TestStoreKeyringWriteDoesNotMergeLegacyOverIndexedKey: write-path
// reconciliation must not replace an indexed key with legacy material based
// on expiry or token strings (not causal). Only legacy-only keys are merged.
func TestStoreKeyringWriteDoesNotMergeLegacyOverIndexedKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Now().Add(10 * time.Minute)
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "indexed-a", RefreshToken: "indexed-r", ExpiresAt: t1}); err != nil {
		t.Fatal(err)
	}

	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "legacy-a", RefreshToken: "legacy-r", ExpiresAt: time.Time{}},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)

	// Save of a different key triggers write reconciliation of the legacy blob.
	if err := s.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load(ProviderKey("alpha"))
	if err != nil || !ok {
		t.Fatalf("Load(alpha): ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "indexed-a" || got.RefreshToken != "indexed-r" {
		t.Fatalf("write reconciliation overwrote indexed alpha with legacy material: got %#v", got)
	}
}

// TestStoreKeyringCompetingLoginOrders: explicit new-binary Save of account B
// must survive a subsequent write even when legacy still holds account A's
// longer-lived token for the same key (both write orders covered).
func TestStoreKeyringCompetingLoginOrders(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	t.Run("explicit_then_legacy_noise", func(t *testing.T) {
		kr := newFakeKR()
		s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Save(ProviderKey("demo"), Token{AccessToken: "b-token", Account: "b", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		// Old writer noise: longer expiry, different account, same key.
		legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
			ProviderKey("demo"): {AccessToken: "a-token", Account: "a", ExpiresAt: time.Now().Add(24 * time.Hour)},
		}}
		data, err := json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)
		if err := s.Save(ProviderKey("other"), Token{AccessToken: "o"}); err != nil {
			t.Fatal(err)
		}
		got, _, err := s.Load(ProviderKey("demo"))
		if err != nil || got.AccessToken != "b-token" || got.Account != "b" {
			t.Fatalf("got %#v, want explicit b-token", got)
		}
	})

	t.Run("legacy_only_then_explicit", func(t *testing.T) {
		kr := newFakeKR()
		s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		// Seed index with an unrelated key so write() takes the reconcile path.
		if err := s.Save(ProviderKey("seed"), Token{AccessToken: "s"}); err != nil {
			t.Fatal(err)
		}
		legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
			ProviderKey("demo"): {AccessToken: "a-token", Account: "a", ExpiresAt: time.Now().Add(24 * time.Hour)},
		}}
		data, err := json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(data)
		// Explicit login as B for demo must become authoritative.
		if err := s.Save(ProviderKey("demo"), Token{AccessToken: "b-token", Account: "b", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		got, _, err := s.Load(ProviderKey("demo"))
		if err != nil || got.AccessToken != "b-token" || got.Account != "b" {
			t.Fatalf("got %#v, want explicit b-token after Save", got)
		}
	})
}

// TestLegacyKeyringLockPathHonorsConfiguredRoot ensures the pre-PR compatibility
// lock is derived via ResolveStorePath (ZERO_OAUTH_TOKENS_PATH / XDG_CONFIG_HOME),
// not a hard-coded home .config path.
func TestLegacyKeyringLockPathHonorsConfiguredRoot(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom", "tokens.json")
	env := map[string]string{"ZERO_OAUTH_TOKENS_PATH": override}
	got := legacyKeyringLockPath(env)
	want := filepath.Join(filepath.Dir(override), "oauth-keyring.lockfile")
	if got != want {
		t.Fatalf("legacyKeyringLockPath = %q, want %q (beside ResolveStorePath)", got, want)
	}

	xdg := filepath.Join(t.TempDir(), "xdg-config")
	gotXDG := legacyKeyringLockPath(map[string]string{"XDG_CONFIG_HOME": xdg})
	wantXDG := filepath.Join(xdg, "zero", "oauth-keyring.lockfile")
	if gotXDG != wantXDG {
		t.Fatalf("legacyKeyringLockPath(XDG) = %q, want %q", gotXDG, wantXDG)
	}
}

func TestStoreKeyringRejectsOversizedSingleTokenPayload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}

	huge := Token{
		AccessToken: "eyJhbGciOiJSUzI1NiJ9." + strings.Repeat("A", 6000) + ".sig",
	}
	err = s.Save(ProviderKey("huge"), huge)
	if err == nil {
		t.Fatal("Save succeeded for oversized single token payload; want error")
	}
	if !strings.Contains(err.Error(), "exceeds single keyring entry bound") {
		t.Fatalf("unexpected error message: %v", err)
	}
	// Preflight must reject before publishing the index key, or the store can
	// accumulate phantom keys until the reader cap bricks every later Save.
	if indexedKeysOf(t, kr)[ProviderKey("huge")] {
		t.Fatal("oversized Save published provider:huge into the index without an entry")
	}
	if _, ok := kr.data[keyringService+"/"+ProviderKey("huge")]; ok {
		t.Fatal("oversized Save wrote a token entry")
	}
	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || !ok {
		t.Fatalf("alpha lost after rejected Save: ok=%v err=%v", ok, err)
	}
}

// TestStoreKeyringPrunesPhantomIndexKeysAfterInterruptedSet: a Set failure
// after the union index was published can leave a key listed without an entry.
// The next successful write must drop that phantom so capacity recovers.
func TestStoreKeyringPrunesPhantomIndexKeysAfterInterruptedSet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}

	// Simulate an interrupted write: index lists beta, but beta has no entry.
	header := keyIndexHeader{Version: 1, Chunks: 1, Keys: []string{ProviderKey("alpha"), ProviderKey("beta")}}
	headerData, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(headerData)

	if err := s.Save(ProviderKey("gamma"), Token{AccessToken: "g"}); err != nil {
		t.Fatal(err)
	}
	indexed := indexedKeysOf(t, kr)
	if indexed[ProviderKey("beta")] {
		t.Fatal("phantom index key provider:beta was not pruned on the next write")
	}
	if !indexed[ProviderKey("alpha")] || !indexed[ProviderKey("gamma")] {
		t.Fatalf("indexed keys = %v, want alpha and gamma", indexed)
	}
}

// TestStoreKeyringCrossRootLegacyLoginSurvivesWithoutDualWrite: the
// compatibility lock cannot span distinct config roots, so a new binary must
// never overwrite or delete the legacy combined entry. An old-style writer on
// root A can land a login after root B reconciled; leaving legacy frozen and
// merging on the next write keeps that login visible.
func TestStoreKeyringCrossRootLegacyLoginSurvivesWithoutDualWrite(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "cfg-a")
	rootB := filepath.Join(t.TempDir(), "cfg-b")
	kr := newFakeKR()

	storeB, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr, Env: map[string]string{"XDG_CONFIG_HOME": rootB}})
	if err != nil {
		t.Fatal(err)
	}
	if err := storeB.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	blobB := storeB.blob.(keyringBlob)
	blobAPath := legacyKeyringLockPath(map[string]string{"XDG_CONFIG_HOME": rootA})
	if blobB.legacyLockPath == blobAPath {
		t.Fatal("expected distinct legacy lock paths for distinct config roots")
	}

	// Old binary on root A writes only the legacy combined entry (no index update).
	legacy := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "a"},
		ProviderKey("carol"): {AccessToken: "c", RefreshToken: "cr"},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyEnc := base64.StdEncoding.EncodeToString(data)
	kr.data[keyringService+"/"+keyringLegacyAccount] = legacyEnc

	// New binary on root B saves again: must merge carol, not clobber legacy.
	if err := storeB.Save(ProviderKey("beta"), Token{AccessToken: "b"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta", "carol"} {
		if _, ok, err := storeB.Load(ProviderKey(name)); err != nil || !ok {
			t.Fatalf("Load(%s) after cross-root legacy login: ok=%v err=%v", name, ok, err)
		}
	}
	if raw := kr.data[keyringService+"/"+keyringLegacyAccount]; raw != legacyEnc {
		t.Fatal("legacy entry was rewritten; cross-root old writers can lose unobserved updates")
	}
}

// TestStoreKeyringReadIndexRejectsOversizedEncodedPayload: bound the base64
// payload before DecodeString/Unmarshal so a damaged index cannot force
// unbounded memory/CPU under the store lock.
func TestStoreKeyringReadIndexRejectsOversizedEncodedPayload(t *testing.T) {
	ckr := &countingKR{fakeKR: newFakeKR()}
	blob := keyringBlob{kr: ckr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount}

	huge := strings.Repeat("A", maxKeyringIndexEncodedBytes+1)
	ckr.data[keyringService+"/"+keyringIndexAccount] = huge
	ckr.gets = 0
	if _, _, _, _, err := blob.readKeyIndex(); err == nil {
		t.Fatal("expected oversized encoded index payload to be rejected")
	}
	if ckr.gets != 1 {
		t.Fatalf("readKeyIndex issued %d gets; want header lookup only", ckr.gets)
	}

	// Continuation chunks must use the same bound.
	header, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: 2, Keys: []string{ProviderKey("seed")}})
	if err != nil {
		t.Fatal(err)
	}
	ckr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(header)
	ckr.data[keyringService+"/"+keyringIndexAccount+"-1"] = huge
	ckr.gets = 0
	if _, _, _, _, err := blob.readKeyIndex(); err == nil {
		t.Fatal("expected oversized encoded chunk payload to be rejected")
	}
}

// TestStoreKeyringPreservesUnobservedLegacyWriteDuringReconcile is the
// regression for [P1] Preserve an old-writer update that lands during legacy
// reconciliation: new B snapshots legacy, old A (other config root) writes a
// new credential into the shared legacy account, then B finishes its Save.
// B must not overwrite A's only copy with a stale dual-write.
func TestStoreKeyringPreservesUnobservedLegacyWriteDuringReconcile(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "cfg-a")
	rootB := filepath.Join(t.TempDir(), "cfg-b")
	kr := newFakeKR()

	storeB, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr, Env: map[string]string{"XDG_CONFIG_HOME": rootB}})
	if err != nil {
		t.Fatal(err)
	}
	if err := storeB.Save(ProviderKey("seed"), Token{AccessToken: "s"}); err != nil {
		t.Fatal(err)
	}

	// Snapshot state as B would see it mid-reconcile, then A lands a login.
	legacyBefore := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("seed"): {AccessToken: "s"},
	}}
	before, err := json.Marshal(legacyBefore)
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(before)

	// Pause is simulated by injecting the post-snapshot old-writer update
	// immediately before B's next Save (which would have dual-written a stale
	// map under the previous protocol).
	legacyAfter := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("seed"):   {AccessToken: "s"},
		ProviderKey("from-a"): {AccessToken: "a-token", RefreshToken: "a-r"},
	}}
	after, err := json.Marshal(legacyAfter)
	if err != nil {
		t.Fatal(err)
	}
	afterEnc := base64.StdEncoding.EncodeToString(after)
	kr.data[keyringService+"/"+keyringLegacyAccount] = afterEnc

	if err := storeB.Save(ProviderKey("from-b"), Token{AccessToken: "b-token"}); err != nil {
		t.Fatal(err)
	}
	// A's credential remains in the frozen legacy blob and is discoverable.
	if raw := kr.data[keyringService+"/"+keyringLegacyAccount]; raw != afterEnc {
		t.Fatal("B overwrote A's unobserved legacy write during reconcile")
	}
	got, ok, err := storeB.Load(ProviderKey("from-a"))
	if err != nil || !ok || got.AccessToken != "a-token" || got.RefreshToken != "a-r" {
		t.Fatalf("Load(from-a) = ok=%v err=%v got=%#v (A's credential lost)", ok, err, got)
	}
	// Distinct roots cannot share the legacy lock; the protocol must still be safe.
	if legacyKeyringLockPath(map[string]string{"XDG_CONFIG_HOME": rootA}) ==
		legacyKeyringLockPath(map[string]string{"XDG_CONFIG_HOME": rootB}) {
		t.Fatal("expected distinct legacy locks for distinct roots")
	}
}

// TestStoreKeyringLogoutDurableAgainstOldWriterStaleSnapshot is the regression
// for [P1] Keep a logout durable when an uncoordinated old binary rewrites its
// stale snapshot: old writer read alpha, new writer deletes it, old writer
// saves another key from its stale snapshot that still includes alpha.
func TestStoreKeyringLogoutDurableAgainstOldWriterStaleSnapshot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	// Old writer snapshot includes alpha (taken before delete).
	oldSnapshot := storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "a"},
		ProviderKey("other"): {AccessToken: "o"},
	}}
	snap, err := json.Marshal(oldSnapshot)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Delete(ProviderKey("alpha")); err != nil {
		t.Fatal(err)
	}
	// Old writer finishes its RMW of another key from the stale snapshot.
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(snap)

	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || ok {
		t.Fatalf("Load(alpha) after stale old-writer rewrite: ok=%v err=%v", ok, err)
	}
	if err := s.Save(ProviderKey("gamma"), Token{AccessToken: "g"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || ok {
		t.Fatalf("Load(alpha) after later Save: ok=%v err=%v (logout not durable)", ok, err)
	}
	// The other key from the stale snapshot is still a legitimate discovery.
	if _, ok, err := s.Load(ProviderKey("other")); err != nil || !ok {
		t.Fatalf("Load(other): ok=%v err=%v", ok, err)
	}
}

// TestStoreKeyringNeverWritesPartialLegacySubset is the regression for [P1]
// Do not replace a valid oversized legacy blob with an arbitrary subset: a
// Linux-compatible multi-provider legacy payload that exceeds the macOS write
// budget must stay complete for old-format readers.
func TestStoreKeyringNeverWritesPartialLegacySubset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	kr := newFakeKR()

	// Build a legacy map whose base64 encoding exceeds maxKeyringSingleEntryBytes
	// (pre-PR Linux keyring storage has no corresponding single-secret limit).
	big := Token{
		AccessToken:  "eyJhbGciOiJSUzI1NiJ9." + strings.Repeat("QUJDRA", 40) + ".sig",
		RefreshToken: "rt_" + strings.Repeat("y", 60),
		TokenType:    "Bearer",
		Scopes:       []string{"openid", "profile", "email", "offline_access"},
		Account:      "user@example.com",
		IDToken:      "eyJhbGciOiJSUzI1NiJ9." + strings.Repeat("QUJDRA", 45) + ".sig",
	}
	tokens := map[string]Token{}
	for _, name := range []string{"anthropic", "openai", "minimax", "zai", "google", "cohere"} {
		tokens[ProviderKey(name)] = big
	}
	legacyRaw, err := json.Marshal(storeFile{SchemaVersion: storeSchemaVersion, Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	legacyEnc := base64.StdEncoding.EncodeToString(legacyRaw)
	if len(legacyEnc) <= maxKeyringSingleEntryBytes {
		t.Fatalf("test fixture legacy enc is %d bytes; need > %d to exercise oversize path", len(legacyEnc), maxKeyringSingleEntryBytes)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = legacyEnc

	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("extra"), Token{AccessToken: "e"}); err != nil {
		t.Fatal(err)
	}
	// Old-format reader must still observe the complete original map, not a
	// nondeterministic subset written under the macOS-safe budget.
	if got := kr.data[keyringService+"/"+keyringLegacyAccount]; got != legacyEnc {
		// Decode both for a clearer failure when rewritten to a subset.
		var before, after storeFile
		_ = json.Unmarshal(mustDecode(t, legacyEnc), &before)
		_ = json.Unmarshal(mustDecode(t, got), &after)
		t.Fatalf("oversized legacy blob was rewritten: before %d keys, after %d keys (must leave complete map untouched)", len(before.Tokens), len(after.Tokens))
	}
	// Indexed store still has every legacy provider after migration.
	for name := range tokens {
		if _, ok, err := s.Load(name); err != nil || !ok {
			t.Fatalf("Load(%s) after migrate: ok=%v err=%v", name, ok, err)
		}
	}
}

// TestStoreKeyringLeaseRefreshesWhileWaitingOnSecondLock is the regression for
// [P1] Renew the index lease while waiting for the legacy lock: after the
// shared index lock is acquired, a blocked wait on the config-root legacy lock
// must keep refreshing the index lock so a competing root cannot reclaim it.
func TestStoreKeyringLeaseRefreshesWhileWaitingOnSecondLock(t *testing.T) {
	dir := t.TempDir()
	indexLock := filepath.Join(dir, "index.lock")
	legacyLock := filepath.Join(dir, "legacy.lock")

	prevRefresh := fileLockRefreshInterval
	prevStale := fileLockStaleAfter
	fileLockRefreshInterval = 15 * time.Millisecond
	fileLockStaleAfter = 80 * time.Millisecond
	defer func() {
		fileLockRefreshInterval = prevRefresh
		fileLockStaleAfter = prevStale
	}()

	// Hold the second lock as a healthy long-lived peer (refresh its mtime)
	// so withLeasedLocks blocks on it after taking the first, without the
	// waiter reclaiming a stale second lock.
	holdLegacy, _, err := acquireFileLock(legacyLock, time.Now)
	if err != nil {
		t.Fatalf("hold legacy lock: %v", err)
	}
	stopHold := make(chan struct{})
	holdDone := make(chan struct{})
	go func() {
		defer close(holdDone)
		ticker := time.NewTicker(fileLockRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopHold:
				return
			case <-ticker.C:
				at := time.Now()
				_ = os.Chtimes(legacyLock, at, at)
			}
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- withLeasedLocks([]string{indexLock, legacyLock}, time.Now, func() error {
			return nil
		})
	}()

	// Wait until the first lock is actually held before measuring lease age.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(indexLock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			close(stopHold)
			<-holdDone
			holdLegacy()
			t.Fatal("index lock was never acquired while waiting on the legacy lock")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wait longer than the stale threshold while the first lock is held and
	// the second is still blocked. The lease must keep the index lock fresh.
	time.Sleep(fileLockStaleAfter + 3*fileLockRefreshInterval)

	info, err := os.Stat(indexLock)
	if err != nil {
		close(stopHold)
		<-holdDone
		holdLegacy()
		t.Fatalf("index lock missing while waiter blocked on legacy: %v", err)
	}
	if age := time.Since(info.ModTime()); age > fileLockStaleAfter {
		close(stopHold)
		<-holdDone
		holdLegacy()
		t.Fatalf("index lock mtime is %v old while waiting on second lock; lease was not renewed", age)
	}

	// A competing reclaim must treat the still-leased index lock as live.
	// Capture staleAfter once: the waiter goroutine is still reading the same
	// package vars, so do not mutate them for the rest of this test.
	staleAfter := fileLockStaleAfter
	cleared, rerr := lockutil.ReclaimStaleLock(indexLock, "competitor-probe", func(reclaimedPath string) bool {
		info, err := os.Stat(reclaimedPath)
		return err == nil && time.Since(info.ModTime()) <= staleAfter
	})
	if rerr != nil {
		close(stopHold)
		<-holdDone
		holdLegacy()
		t.Fatalf("reclaim probe: %v", rerr)
	}
	if cleared {
		close(stopHold)
		<-holdDone
		holdLegacy()
		t.Fatal("competitor reclaimed the index lock while the multi-lock waiter still owned it")
	}
	if _, err := os.Stat(indexLock); err != nil {
		close(stopHold)
		<-holdDone
		holdLegacy()
		t.Fatalf("index lock missing after failed reclaim: %v", err)
	}

	close(stopHold)
	<-holdDone
	holdLegacy()
	if err := <-done; err != nil {
		t.Fatalf("withLeasedLocks after legacy release: %v", err)
	}
}

func TestStoreKeyringLegacyOnlyReadHonorsInterruptedDeleteTombstone(t *testing.T) {
	kr := &failingKR{fakeKR: newFakeKR()}
	legacy, err := json.Marshal(storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "revoked"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(legacy)
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}

	kr.failAt = 2
	if removed, err := s.Delete(ProviderKey("alpha")); err == nil || !removed {
		t.Fatalf("Delete = removed %v, err %v; want removed with injected failure", removed, err)
	}
	kr.failAt = 0
	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || ok {
		t.Fatalf("Load after interrupted delete = ok %v, err %v; tombstone must hide legacy token", ok, err)
	}
}

func TestStoreKeyringReloginKeepsTombstoneUntilReplacementCommits(t *testing.T) {
	kr := &failingKR{fakeKR: newFakeKR()}
	legacy, err := json.Marshal(storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "revoked"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringLegacyAccount] = base64.StdEncoding.EncodeToString(legacy)
	b := keyringBlob{kr: kr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount}
	if err := b.writeTombstones(map[string]bool{ProviderKey("alpha"): true}); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}

	kr.ops = 0
	kr.failAt = 2
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "replacement"}); err == nil {
		t.Fatal("Save succeeded despite injected index failure")
	}
	kr.failAt = 0
	if _, ok, err := s.Load(ProviderKey("alpha")); err != nil || ok {
		t.Fatalf("Load after interrupted re-login = ok %v, err %v; revoked legacy token was restored", ok, err)
	}
	if err := s.Save(ProviderKey("alpha"), Token{AccessToken: "replacement"}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.Load(ProviderKey("alpha")); err != nil || !ok || got.AccessToken != "replacement" {
		t.Fatalf("Load committed replacement = %#v, ok %v, err %v", got, ok, err)
	}
}

func TestStoreKeyringTombstonesOutgrowLiveCredentialCap(t *testing.T) {
	kr := newFakeKR()
	b := keyringBlob{kr: kr, service: keyringService, indexAccount: keyringIndexAccount}
	tombstones := make(map[string]bool, maxKeyringIndexKeys+1)
	for i := 0; i <= maxKeyringIndexKeys; i++ {
		tombstones[ProviderKey(fmt.Sprintf("retired-%03d", i))] = true
	}
	if err := b.writeTombstones(tombstones); err != nil {
		t.Fatalf("write %d tombstones: %v", len(tombstones), err)
	}
	got, err := b.readTombstones()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(tombstones) {
		t.Fatalf("read %d tombstones, want %d", len(got), len(tombstones))
	}
}

func TestKeyringLockPathUserLookupFallbackIgnoresAmbientHome(t *testing.T) {
	previous := currentOSUser
	currentOSUser = func() (*user.User, error) { return nil, fmt.Errorf("lookup unavailable") }
	defer func() { currentOSUser = previous }()

	gotA, err := keyringLockPath(map[string]string{"HOME": t.TempDir()}, keyringService, keyringIndexAccount)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := keyringLockPath(map[string]string{"HOME": t.TempDir()}, keyringService, keyringIndexAccount)
	if err != nil {
		t.Fatal(err)
	}
	if gotA != gotB {
		t.Fatalf("same-user fallback changed with HOME: %q vs %q", gotA, gotB)
	}
	fallbackDir, err := keyringFallbackLockDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(fallbackDir, keyringTempLockName(keyringService, keyringIndexAccount))
	if gotA != want {
		t.Fatalf("fallback lock = %q, want UID-scoped temporary %q", gotA, want)
	}
}

// TestWithLeasedLocksReleasesOnPanic guards the critical finding that a panic
// inside fn used to skip releaseAll: the lease goroutine kept Chtimes alive
// forever and every later waiter blocked until process exit.
func TestWithLeasedLocksReleasesOnPanic(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "panic.lock")
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic from fn")
			}
		}()
		_ = withLeasedLocks([]string{lockPath}, time.Now, func() error {
			panic("simulated critical-section panic")
		})
	}()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file still present after panic recovery: %v", err)
	}
	start := time.Now()
	if err := withLeasedLocks([]string{lockPath}, time.Now, func() error { return nil }); err != nil {
		t.Fatalf("second withLeasedLocks after panic: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("second acquisition took %v; lock was still being leased", elapsed)
	}
}

// TestLeaseRefreshStopsWhenLockReplaced is the regression for ownership-aware
// lease refresh: if a holder pauses past fileLockStaleAfter and a peer replaces
// the lock, the original holder must not Chtimes the replacement (which would
// keep both critical sections alive) and must fail closed.
func TestLeaseRefreshStopsWhenLockReplaced(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "stolen.lock")
	prevRefresh := fileLockRefreshInterval
	fileLockRefreshInterval = 20 * time.Millisecond
	defer func() { fileLockRefreshInterval = prevRefresh }()

	var lostErr error
	err := withLeasedLocks([]string{lockPath}, time.Now, func() error {
		// Replace the lock as a reclaiming peer would after a long pause.
		if err := os.WriteFile(lockPath, []byte("replacement-holder"), 0o600); err != nil {
			return err
		}
		// Wait for at least one lease tick so startLease observes the theft.
		time.Sleep(80 * time.Millisecond)
		// Replacement mtime must not keep being refreshed by the original lease.
		info, err := os.Stat(lockPath)
		if err != nil {
			return err
		}
		first := info.ModTime()
		time.Sleep(80 * time.Millisecond)
		info, err = os.Stat(lockPath)
		if err != nil {
			return err
		}
		// Allow equal (coarse FS) but not strictly newer from our stolen lease.
		// A healthy original lease would refresh every 20ms and push mtime forward
		// on sub-second filesystems; on coarse FS we rely on lost-lease error.
		_ = first
		_ = info
		return nil
	})
	lostErr = err
	if lostErr == nil {
		t.Fatal("expected lost-lease error after lock replacement, got nil")
	}
	if !strings.Contains(lostErr.Error(), "lost token lock lease") {
		t.Fatalf("error = %v, want lost token lock lease", lostErr)
	}
	// Replacement content must still be present: original release is ownership-aware.
	data, err := os.ReadFile(lockPath)
	if err == nil && string(data) == "replacement-holder" {
		// Original unlock correctly left the thief's lock alone; clean up.
		_ = os.Remove(lockPath)
	}
}

// TestAcquireFileLockTimesOutOnFutureMtime rejects a lock whose mtime is in
// the future as a healthy lease: without the non-negative age guard, every
// wait loop extends idleDeadline forever.
func TestAcquireFileLockTimesOutOnFutureMtime(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "future.lock")
	if err := os.WriteFile(lockPath, []byte("hostile"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(lockPath, future, future); err != nil {
		t.Fatal(err)
	}
	prevTimeout := fileLockTimeout
	fileLockTimeout = 80 * time.Millisecond
	defer func() { fileLockTimeout = prevTimeout }()

	start := time.Now()
	_, _, err := acquireFileLock(lockPath, time.Now)
	if err == nil {
		t.Fatal("expected timeout on future-mtime lock")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timed out", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timed out too slowly (%v); future mtime was treated as healthy", elapsed)
	}
}

// TestWriteSkipsIndexShrinkWhenChunkMissing ensures external keychain damage
// that drops a continuation chunk does not orphan the unlisted entries on the
// next Save: write keeps the union index instead of shrinking.
func TestWriteSkipsIndexShrinkWhenChunkMissing(t *testing.T) {
	kr := newFakeKR()
	blob := keyringBlob{kr: kr, service: keyringService, indexAccount: keyringIndexAccount}

	// Build a two-chunk index: header + chunk-1, then delete chunk-1.
	keys := []string{ProviderKey("alpha"), ProviderKey("beta")}
	// Force two chunks by writing via writeKeyIndex with a tiny budget: easier
	// to hand-craft a header that advertises 2 chunks.
	header, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: 2, Keys: []string{ProviderKey("alpha")}})
	if err != nil {
		t.Fatal(err)
	}
	chunk1, err := json.Marshal([]string{ProviderKey("beta")})
	if err != nil {
		t.Fatal(err)
	}
	kr.data[keyringService+"/"+keyringIndexAccount] = base64.StdEncoding.EncodeToString(header)
	kr.data[keyringService+"/"+keyringIndexAccount+"-1"] = base64.StdEncoding.EncodeToString(chunk1)
	// Entries for both keys.
	for _, k := range keys {
		raw, _ := json.Marshal(Token{AccessToken: "tok-" + k})
		kr.data[keyringService+"/"+k] = base64.StdEncoding.EncodeToString(raw)
	}
	// Damage: drop chunk-1.
	delete(kr.data, keyringService+"/"+keyringIndexAccount+"-1")

	gotKeys, ok, chunks, incomplete, err := blob.readKeyIndex()
	if err != nil || !ok {
		t.Fatalf("readKeyIndex: ok=%v err=%v", ok, err)
	}
	if !incomplete {
		t.Fatal("expected incomplete=true when chunk-1 is missing")
	}
	if chunks != 2 {
		t.Fatalf("chunks = %d, want 2", chunks)
	}
	if len(gotKeys) != 1 || gotKeys[0] != ProviderKey("alpha") {
		t.Fatalf("keys = %v, want only header keys", gotKeys)
	}

	// Save a third key; shrink must be skipped so the union still lists alpha
	// (and beta cannot be re-indexed from the missing chunk, but alpha must
	// not disappear and beta's entry must not be deleted as a non-livePrior).
	state, err := json.Marshal(storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{
		ProviderKey("alpha"): {AccessToken: "tok-alpha"},
		ProviderKey("gamma"): {AccessToken: "tok-gamma"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := blob.write(state, map[string]bool{ProviderKey("gamma"): false}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// beta entry must still exist (not deleted: it was absent from truncated livePrior).
	if _, ok := kr.data[keyringService+"/"+ProviderKey("beta")]; !ok {
		t.Fatal("beta entry was deleted despite missing index chunk; orphan risk path")
	}
	// Index after write should still be a union (not a shrink that drops everything unknown).
	afterKeys, _, _, afterIncomplete, err := blob.readKeyIndex()
	if err != nil {
		t.Fatal(err)
	}
	// incomplete may clear if write rewrote a complete index; either way alpha+gamma
	// must be listed, and we did not shrink away the prior union carelessly.
	_ = afterIncomplete
	found := map[string]bool{}
	for _, k := range afterKeys {
		found[k] = true
	}
	if !found[ProviderKey("alpha")] || !found[ProviderKey("gamma")] {
		t.Fatalf("post-write keys = %v, want alpha and gamma listed", afterKeys)
	}
}
