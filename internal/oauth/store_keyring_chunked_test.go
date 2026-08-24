package oauth

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// macOSLikeBudget is the real macOS ceiling: `security -i` reads at most 4095
// bytes of command line, and the fake charges the account name against it the
// way the real command line does.
const macOSLikeBudget = 4095

// bigToken builds a credential the size of a real OIDC login (a JWT access
// token, an ID token, and an opaque refresh token), which is what pushes the
// combined blob past a single keychain entry.
func bigToken(seed string) Token {
	return Token{
		AccessToken:  strings.Repeat(seed, 1200),
		IDToken:      strings.Repeat(seed, 900),
		RefreshToken: strings.Repeat(seed, 300),
		TokenType:    "Bearer",
		Scopes:       []string{"openid", "profile", "email", "offline_access"},
		ExpiresAt:    time.Unix(1_800_000_000, 0).UTC(),
		Account:      seed + "@example.com",
	}
}

func newCappedKeyringStore(t *testing.T, kr KeyringClient) *Store {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatalf("NewStore(keyring): %v", err)
	}
	return s
}

func mustSave(t *testing.T, s *Store, name string, token Token) {
	t.Helper()
	if err := s.Save(ProviderKey(name), token); err != nil {
		t.Fatalf("Save(%s): %v", name, err)
	}
}

func mustLoad(t *testing.T, s *Store, name string) Token {
	t.Helper()
	token, ok, err := s.Load(ProviderKey(name))
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	if !ok {
		t.Fatalf("Load(%s): no token stored", name)
	}
	return token
}

func manifestOf(t *testing.T, kr *fakeKR) keyringManifest {
	t.Helper()
	head, ok, _ := kr.Get(keyringService, keyringAccount)
	if !ok {
		t.Fatal("no anchor entry stored")
	}
	if !strings.HasPrefix(head, keyringManifestPrefix) {
		t.Fatalf("anchor entry is a whole blob, not a manifest: %.32s...", head)
	}
	manifest, err := parseKeyringManifest(head)
	if err != nil {
		t.Fatalf("parseKeyringManifest(%q): %v", head, err)
	}
	return manifest
}

// TestStoreKeyringSavesSecondLoginOverEntryLimit is the regression: two OIDC
// logins do not fit one macOS keychain entry, and storing the blob as a single
// entry made the second Save fail outright with nothing persisted.
func TestStoreKeyringSavesSecondLoginOverEntryLimit(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)

	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))

	if got := mustLoad(t, s, "first"); got.AccessToken != bigToken("a").AccessToken {
		t.Error("first login did not survive the second save")
	}
	if got := mustLoad(t, s, "second"); got.AccessToken != bigToken("b").AccessToken {
		t.Error("second login did not round-trip")
	}

	manifest := manifestOf(t, kr)
	if manifest.counts[manifest.live] < 2 {
		t.Fatalf("blob fit in %d chunk(s); the test no longer exercises splitting", manifest.counts[manifest.live])
	}
	for _, account := range kr.chunkAccounts(manifest.live) {
		if got := len(kr.data[keyringService+"/"+account]); got > macOSLikeBudget-len(account) {
			t.Errorf("chunk %s is %d bytes, over its own entry budget", account, got)
		}
	}
}

// TestStoreKeyringChunkSizingSurvivesLongerChunkAccounts pins the reason chunks
// are sized against the highest possible index: on macOS the account shares the
// command line with the secret, so a budget taken from chunk 0 overflows once
// the index grows a digit.
func TestStoreKeyringChunkSizingSurvivesLongerChunkAccounts(t *testing.T) {
	// A tiny budget forces enough chunks that the index reaches two digits.
	kr := newCappedFakeKR(keyringMinChunkLen + len(keyringAccount) + 8)
	s := newCappedKeyringStore(t, kr)

	mustSave(t, s, "first", bigToken("a"))

	manifest := manifestOf(t, kr)
	if manifest.counts[manifest.live] < 10 {
		t.Fatalf("only %d chunks; the test needs a two-digit index", manifest.counts[manifest.live])
	}
	if got := mustLoad(t, s, "first"); got.AccessToken != bigToken("a").AccessToken {
		t.Error("blob did not round-trip across two-digit chunk indices")
	}
}

// TestStoreKeyringChunkedReadRejectsMissingChunk covers a torn read: a chunk the
// manifest names is gone, which must be reported rather than decoded as a
// truncated blob.
func TestStoreKeyringChunkedReadRejectsMissingChunk(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)
	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))

	manifest := manifestOf(t, kr)
	accounts := kr.chunkAccounts(manifest.live)
	delete(kr.data, keyringService+"/"+accounts[len(accounts)-1])

	_, _, err := s.Load(ProviderKey("first"))
	if err == nil || !strings.Contains(err.Error(), "missing chunk") {
		t.Fatalf("Load after losing a chunk: err = %v, want a missing-chunk error", err)
	}
}

// TestStoreKeyringChunkedReadRejectsTruncatedChunk covers the corruption that
// motivated chunking in the first place: `security -i` splits an overlong line
// into two commands instead of refusing it, so a chunk can come back short. The
// digest has to catch that even when the result is still valid base64.
func TestStoreKeyringChunkedReadRejectsTruncatedChunk(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)
	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))

	manifest := manifestOf(t, kr)
	account := kr.chunkAccounts(manifest.live)[0]
	key := keyringService + "/" + account
	// Drop a whole base64 quantum so the concatenation still decodes cleanly.
	kr.data[key] = kr.data[key][:len(kr.data[key])-4]

	_, _, err := s.Load(ProviderKey("first"))
	if err == nil {
		t.Fatal("Load of a truncated chunk succeeded; the digest did not catch it")
	}
	if !strings.Contains(err.Error(), "integrity check") && !strings.Contains(err.Error(), "invalid token store") {
		t.Fatalf("Load of a truncated chunk: err = %v, want an integrity or parse failure", err)
	}
}

// TestStoreKeyringWriteCommitsOnlyAtTheManifest asserts the commit point: a
// write that dies while filling chunks must leave the previous generation
// readable, because the manifest still names it.
func TestStoreKeyringWriteCommitsOnlyAtTheManifest(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)
	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))

	before := manifestOf(t, kr)
	boom := errors.New("keychain is locked")
	// Fail the second chunk of whichever generation the next write targets, so
	// the write dies partway through filling it.
	kr.failSet = func(account string) error {
		if strings.HasSuffix(account, ".1") {
			return boom
		}
		return nil
	}

	if err := s.Save(ProviderKey("third"), bigToken("c")); !errors.Is(err, boom) {
		t.Fatalf("Save with a failing chunk write: err = %v, want %v", err, boom)
	}

	kr.failSet = nil
	if got := mustLoad(t, s, "first"); got.AccessToken != bigToken("a").AccessToken {
		t.Error("a failed write damaged the committed blob")
	}
	if _, ok, _ := s.Load(ProviderKey("third")); ok {
		t.Error("a write that never reached the manifest was visible anyway")
	}
	if after := manifestOf(t, kr); after.live != before.live || after.digest != before.digest {
		t.Errorf("manifest moved on a failed write: %+v -> %+v", before, after)
	}
}

// TestStoreKeyringWriteFailsOnFinalManifestPublication asserts that when the
// final manifest publication fails after all chunk writes succeed, the previous
// manifest and committed blob remain readable, the new login is not visible,
// and the target generation's written accounts remain tracked for cleanup.
func TestStoreKeyringWriteFailsOnFinalManifestPublication(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)
	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))

	before := manifestOf(t, kr)
	boom := errors.New("keychain is locked on manifest publish")

	// Allow reservation and chunk writes to succeed, but fail when publishing
	// the final manifest with next.live.
	anchorSets := 0
	kr.failSet = func(account string) error {
		if account == keyringAccount {
			anchorSets++
			if anchorSets > 1 {
				return boom
			}
		}
		return nil
	}

	if err := s.Save(ProviderKey("third"), bigToken("c")); !errors.Is(err, boom) {
		t.Fatalf("Save with failing final manifest publication: err = %v, want %v", err, boom)
	}
	kr.failSet = nil

	if got := mustLoad(t, s, "first"); got.AccessToken != bigToken("a").AccessToken {
		t.Error("a failed final manifest publication damaged the committed blob")
	}
	if _, ok, _ := s.Load(ProviderKey("third")); ok {
		t.Error("a write that failed final manifest publication was visible anyway")
	}

	after := manifestOf(t, kr)
	if after.live != before.live {
		t.Fatalf("live generation moved on failed final manifest publication: %q -> %q", before.live, after.live)
	}
	assertNoStrayChunks(t, kr, after)

	// A subsequent write successfully cleans up the orphaned generation chunks
	mustSave(t, s, "fourth", bigToken("d"))
	assertNoStrayChunks(t, kr, manifestOf(t, kr))
}

// TestStoreKeyringAlternatesGenerationsAndRetiresTheOld covers the ping-pong:
// each write lands in the generation that is not live, and the retired one is
// removed so a previous login's tokens do not linger in the keychain.
func TestStoreKeyringAlternatesGenerationsAndRetiresTheOld(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)
	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))

	seen := map[string]bool{}
	for round := range 3 {
		mustSave(t, s, "second", bigToken(string(rune('c'+round))))
		manifest := manifestOf(t, kr)
		seen[manifest.live] = true

		retired := keyringChunkFamilyA
		if manifest.live == keyringChunkFamilyA {
			retired = keyringChunkFamilyB
		}
		if left := kr.chunkAccounts(retired); len(left) != 0 {
			t.Errorf("round %d: retired generation %q still holds %v", round, retired, left)
		}
		// The retired count deliberately stays in the manifest. Over-stating is
		// the safe direction, so the invariant to hold is the one-sided one.
		assertNoStrayChunks(t, kr, manifest)
	}
	if len(seen) != 2 {
		t.Errorf("writes stayed in generation(s) %v; they must alternate", seen)
	}
}

// TestStoreKeyringGrowsIntoChunksAndShrinksBack covers both layout transitions,
// including that shrinking below the cap removes the chunks rather than
// stranding a logged-out provider's tokens in the keychain.
func TestStoreKeyringGrowsIntoChunksAndShrinksBack(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)

	mustSave(t, s, "small", Token{AccessToken: "short", RefreshToken: "r"})
	if head, _, _ := kr.Get(keyringService, keyringAccount); strings.HasPrefix(head, keyringManifestPrefix) {
		t.Fatal("a blob that fits one entry was chunked anyway")
	}

	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))
	manifest := manifestOf(t, kr)
	if len(kr.chunkAccounts(manifest.live)) == 0 {
		t.Fatal("blob outgrew one entry but no chunks were written")
	}

	if _, err := s.Delete(ProviderKey("first")); err != nil {
		t.Fatalf("Delete(first): %v", err)
	}
	if _, err := s.Delete(ProviderKey("second")); err != nil {
		t.Fatalf("Delete(second): %v", err)
	}

	head, ok, _ := kr.Get(keyringService, keyringAccount)
	if !ok || strings.HasPrefix(head, keyringManifestPrefix) {
		t.Fatalf("store did not return to a whole entry after shrinking: %.32s...", head)
	}
	for _, family := range []string{keyringChunkFamilyA, keyringChunkFamilyB} {
		if left := kr.chunkAccounts(family); len(left) != 0 {
			t.Errorf("generation %q still holds %v after shrinking", family, left)
		}
	}
	if got := mustLoad(t, s, "small"); got.AccessToken != "short" {
		t.Errorf("small token lost across the layout transitions: %#v", got)
	}
}

// TestStoreKeyringUnboundedBackendNeverChunks pins that a backend without a
// size limit (Linux secret-tool reads the secret from stdin) keeps the original
// single-entry form, so nothing about its stored layout changes.
func TestStoreKeyringUnboundedBackendNeverChunks(t *testing.T) {
	kr := newFakeKR()
	s := newCappedKeyringStore(t, kr)
	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))

	head, ok, _ := kr.Get(keyringService, keyringAccount)
	if !ok || strings.HasPrefix(head, keyringManifestPrefix) {
		t.Fatalf("unbounded backend used the chunked layout: %.32s...", head)
	}
	if got := mustLoad(t, s, "second"); got.AccessToken != bigToken("b").AccessToken {
		t.Error("unbounded backend did not round-trip")
	}
}

// TestStoreKeyringReadsLegacyWholeEntry covers the upgrade path: a blob written
// by a build that only knew the single-entry layout is still read, without a
// migration step.
func TestStoreKeyringReadsLegacyWholeEntry(t *testing.T) {
	kr := newFakeKR()
	legacy := newCappedKeyringStore(t, kr)
	mustSave(t, legacy, "first", Token{AccessToken: "legacy", RefreshToken: "r"})

	kr.budget = macOSLikeBudget
	s := newCappedKeyringStore(t, kr)
	if got := mustLoad(t, s, "first"); got.AccessToken != "legacy" {
		t.Fatalf("legacy entry did not load: %#v", got)
	}
	mustSave(t, s, "second", bigToken("b"))
	if got := mustLoad(t, s, "first"); got.AccessToken != "legacy" {
		t.Error("legacy token lost when the store grew into chunks")
	}
}

func TestParseKeyringManifestRejectsMalformed(t *testing.T) {
	digest := strings.Repeat("0", 64)
	for name, head := range map[string]string{
		"too few fields":     keyringManifestPrefix + "a:1:" + digest,
		"unknown generation": keyringManifestPrefix + "c:1:0:" + digest,
		"negative count":     keyringManifestPrefix + "a:-1:0:" + digest,
		"count over the cap": keyringManifestPrefix + "a:65:0:" + digest,
		"non-numeric count":  keyringManifestPrefix + "a:x:0:" + digest,
		"live has no chunks": keyringManifestPrefix + "a:0:2:" + digest,
		"short digest":       keyringManifestPrefix + "a:1:0:beef",
		"non-hex digest":     keyringManifestPrefix + "a:1:0:" + strings.Repeat("g", 64),
	} {
		if _, err := parseKeyringManifest(head); err == nil {
			t.Errorf("%s: parseKeyringManifest(%q) succeeded, want an error", name, head)
		}
	}
}

// assertNoStrayChunks checks the invariant the whole cleanup design rests on:
// the manifest may over-state how many chunks a generation holds (a later write
// deletes the excess) but must never under-state it, because an uncounted chunk
// is a fragment of a token blob nothing will ever delete.
func assertNoStrayChunks(t *testing.T, kr *fakeKR, manifest keyringManifest) {
	t.Helper()
	for _, family := range []string{keyringChunkFamilyA, keyringChunkFamilyB} {
		count := manifest.counts[family]
		for index := 0; index < keyringMaxChunks; index++ {
			account := keyringAccount + "." + family + "." + strconv.Itoa(index)
			_, exists := kr.data[keyringService+"/"+account]
			if family == manifest.live {
				if index < count && !exists {
					t.Errorf("live generation %q is missing expected chunk index %d", family, index)
				}
				if index >= count && exists {
					t.Errorf("live generation %q has stray chunk at index %d (count %d)", family, index, count)
				}
			} else {
				if index >= count && exists {
					t.Errorf("generation %q has stray chunk at index %d (count %d)", family, index, count)
				}
			}
		}
	}
	if got := len(kr.chunkAccounts(manifest.live)); got != manifest.counts[manifest.live] {
		t.Errorf("live generation %q holds %d chunks, manifest says %d", manifest.live, got, manifest.counts[manifest.live])
	}
}

// TestStoreKeyringReservesChunkRangeBeforeFilling covers the failure path the
// reservation exists for: a write that dies partway through filling a longer
// generation must still leave those chunks counted, so the next write deletes
// them instead of stranding token material in the keychain.
func TestStoreKeyringReservesChunkRangeBeforeFilling(t *testing.T) {
	// A small per-entry budget makes the token blob span many chunks, so a
	// failure can land in the middle of one generation.
	kr := newCappedFakeKR(keyringMinChunkLen + len(keyringAccount) + 40)
	s := newCappedKeyringStore(t, kr)
	mustSave(t, s, "first", bigToken("a"))

	before := manifestOf(t, kr)
	target := keyringChunkFamilyB
	if before.live == keyringChunkFamilyB {
		target = keyringChunkFamilyA
	}

	boom := errors.New("keychain is locked")
	kr.failSet = func(account string) error {
		if account == keyringAccount+"."+target+".5" {
			return boom
		}
		return nil
	}
	if err := s.Save(ProviderKey("second"), bigToken("b")); !errors.Is(err, boom) {
		t.Fatalf("Save with a failing chunk write: err = %v, want %v", err, boom)
	}
	kr.failSet = nil

	interrupted := manifestOf(t, kr)
	if interrupted.live != before.live {
		t.Fatalf("a failed write moved the live generation: %q -> %q", before.live, interrupted.live)
	}
	if len(kr.chunkAccounts(target)) == 0 {
		t.Fatal("the failed write left no chunks; the test no longer exercises the reservation")
	}
	assertNoStrayChunks(t, kr, interrupted)

	// A later, much smaller write into the same generation must reclaim every
	// chunk the interrupted one left behind.
	if _, err := s.Delete(ProviderKey("first")); err != nil {
		t.Fatalf("Delete(first): %v", err)
	}
	mustSave(t, s, "small", Token{AccessToken: strings.Repeat("s", 900)})
	assertNoStrayChunks(t, kr, manifestOf(t, kr))
}

// TestStoreKeyringSweepsStrayChunksOnFirstGrowth covers the one transition the
// reservation cannot protect: while the anchor still holds the whole blob there
// is no manifest to reserve into, because writing one would destroy the only
// copy. Chunks left by an interrupted shrink are swept instead.
func TestStoreKeyringSweepsStrayChunksOnFirstGrowth(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)
	mustSave(t, s, "small", Token{AccessToken: "short"})

	// Stand in for a shrink whose cleanup was interrupted: the anchor holds a
	// whole blob, but a previous chunked generation is still on disk.
	strays := []string{keyringAccount + ".a.3", keyringAccount + ".a.7"}
	for _, account := range strays {
		kr.data[keyringService+"/"+account] = "c3RhbGUtdG9rZW4tbWF0ZXJpYWw="
	}

	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))

	assertNoStrayChunks(t, kr, manifestOf(t, kr))
	for _, account := range strays {
		if _, ok := kr.data[keyringService+"/"+account]; ok {
			t.Errorf("stray chunk %s survived the growth into the chunked layout", account)
		}
	}
}

// TestStoreKeyringReadSerializedWithLockDuringChunkedWrite verifies that readers
// executing concurrently with a chunked writer hold the cross-process lock so they
// do not observe a torn state (such as reading an old manifest after the writer
// has deleted old-generation chunks).
func TestStoreKeyringReadSerializedWithLockDuringChunkedWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	kr := newCappedFakeKR(macOSLikeBudget)
	writer, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatalf("NewStore(writer): %v", err)
	}
	reader, err := NewStore(StoreOptions{Storage: "keyring", Keyring: kr})
	if err != nil {
		t.Fatalf("NewStore(reader): %v", err)
	}

	mustSave(t, writer, "first", bigToken("a"))
	mustSave(t, writer, "second", bigToken("b"))

	// Manually acquire the lock to simulate writer holding the lock across
	// manifest commit and chunk rotation.
	lockPath := writer.blob.(keyringBlob).lockPath
	unlock, err := acquireFileLock(lockPath, time.Now)
	if err != nil {
		t.Fatalf("acquireFileLock: %v", err)
	}

	// While lock is held, Load and Status should block.
	loaded := make(chan Token, 1)
	loadErr := make(chan error, 1)
	go func() {
		tok, _, err := reader.Load(ProviderKey("first"))
		loadErr <- err
		loaded <- tok
	}()

	statused := make(chan []Status, 1)
	statusErr := make(chan error, 1)
	go func() {
		st, err := reader.Status(KeyPrefixProvider)
		statusErr <- err
		statused <- st
	}()

	// Ensure reader is waiting on lock.
	select {
	case <-loaded:
		t.Fatal("reader.Load returned while lock was held")
	case <-statused:
		t.Fatal("reader.Status returned while lock was held")
	case <-time.After(50 * time.Millisecond):
	}

	// Release lock, allowing reader to complete.
	unlock()

	select {
	case err := <-loadErr:
		if err != nil {
			t.Fatalf("reader.Load failed after unlock: %v", err)
		}
		tok := <-loaded
		if tok.AccessToken != bigToken("a").AccessToken {
			t.Errorf("reader.Load returned unexpected token: %v", tok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader.Load timed out waiting for lock release")
	}

	select {
	case err := <-statusErr:
		if err != nil {
			t.Fatalf("reader.Status failed after unlock: %v", err)
		}
		st := <-statused
		if len(st) != 2 {
			t.Errorf("reader.Status returned %d entries, want 2", len(st))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader.Status timed out waiting for lock release")
	}
}

// TestStoreKeyringShrinkResidueIsReclaimedOnRegrowth is the regression for a
// retired generation orphaned permanently. Cleanup is hygiene rather than
// correctness only while a manifest exists to state the counts; writeWhole
// replaces the anchor with the blob and the counts go with it. A shrink whose
// delete of the retired generation fails therefore leaves chunks that the next
// growth would never sweep, because that growth only ever targets family A.
// The token material in them would sit in the keychain for good.
func TestStoreKeyringShrinkResidueIsReclaimedOnRegrowth(t *testing.T) {
	kr := newCappedFakeKR(macOSLikeBudget)
	s := newCappedKeyringStore(t, kr)

	// Grow until family B is the live generation, so a shrink from here is the
	// one that retires it.
	mustSave(t, s, "first", bigToken("a"))
	mustSave(t, s, "second", bigToken("b"))
	mustSave(t, s, "third", bigToken("c"))
	if live := manifestOf(t, kr).live; live != keyringChunkFamilyB {
		t.Fatalf("live generation = %q, want %q", live, keyringChunkFamilyB)
	}

	// Shrink back to a whole entry with a keychain that refuses to remove
	// family B, the generation being retired on the way down.
	kr.failDelete = func(account string) error {
		if strings.HasPrefix(account, keyringAccount+"."+keyringChunkFamilyB+".") {
			return errors.New("keychain busy")
		}
		return nil
	}
	for _, key := range []string{"second", "third"} {
		if _, err := s.Delete(ProviderKey(key)); err != nil && !strings.Contains(err.Error(), "could not be removed") {
			t.Fatalf("Delete(%s): %v", key, err)
		}
	}
	kr.failDelete = nil
	if head, _, _ := kr.Get(keyringService, keyringAccount); strings.HasPrefix(head, keyringManifestPrefix) {
		t.Fatal("store did not shrink back to a whole entry, so the orphaning path was never taken")
	}
	orphans := kr.chunkAccounts(keyringChunkFamilyB)
	if len(orphans) == 0 {
		t.Fatal("setup did not strand any family-B chunks")
	}

	// Grow back into the chunked layout. This is the only sweep that can still
	// reach the stranded generation, because the manifest that named it is gone.
	mustSave(t, s, "fourth", bigToken("d"))

	manifest := manifestOf(t, kr)
	if manifest.live != keyringChunkFamilyA {
		t.Fatalf("live generation after regrowth = %q, want %q", manifest.live, keyringChunkFamilyA)
	}
	if got := kr.chunkAccounts(keyringChunkFamilyB); len(got) != 0 {
		t.Errorf("family B still holds %d unreferenced chunks after regrowth: %v", len(got), got)
	}
	assertNoStrayChunks(t, kr, manifest)
	if got := mustLoad(t, s, "fourth"); got.AccessToken != bigToken("d").AccessToken {
		t.Error("token stored across the regrowth did not survive")
	}
}
