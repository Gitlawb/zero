package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/keyring"
)

const (
	storeSchemaVersion = 1
	// KeyPrefixProvider namespaces provider-login tokens; MCP server tokens live
	// under KeyPrefixMCP in the same format (so a future MCP migration is a key
	// rename, not a format change).
	KeyPrefixProvider = "provider:"
	KeyPrefixMCP      = "mcp:"
)

// keyPattern bounds a token key to a safe, single-segment namespaced identifier
// so a key can never traverse or collide with store internals.
var keyPattern = regexp.MustCompile(`^(provider|mcp):[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidateKey reports whether key is a well-formed namespaced token key.
func ValidateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("oauth: invalid token key %q (want \"provider:<name>\" or \"mcp:<name>\")", key)
	}
	return nil
}

// ProviderKey builds the store key for a provider login, normalizing the name
// to lower case. Every write (Manager.Login, the ChatGPT flow) and every
// lookup (FirstStored, GetFresh, logout, status filters) funnels through here,
// so normalizing at this one choke point keeps them symmetric: without it,
// `zero auth login xAI` stored "provider:xAI" while the profile scaffolded for
// it looked up "provider:xai" case-sensitively — a fresh, successful login
// that was invisible to the runtime.
func ProviderKey(name string) string {
	return KeyPrefixProvider + strings.ToLower(strings.TrimSpace(name))
}

// FirstStored returns the token and its ProviderKey for the FIRST candidate name
// that has a token in the store, with ok=false when none do. Callers pass
// ProviderProfile.OAuthLoginCandidates() so that everything derived from a login
// — the bearer token AND any header claim like chatgpt-account-id — comes from
// the SAME login; selecting independently per consumer could otherwise pair a
// bearer from one login with an account header from another. A load error on a
// candidate is treated as a miss (skip to the next), never a hard failure.
func FirstStored(store *Store, candidates []string) (Token, string, bool) {
	if store == nil {
		return Token{}, "", false
	}
	for _, name := range candidates {
		key := ProviderKey(name)
		if token, ok, err := store.Load(key); err == nil && ok {
			return token, key, true
		}
	}
	return Token{}, "", false
}

// Status is a redaction-safe summary of a stored token (no secret material).
type Status struct {
	Key             string    `json:"key"`
	HasToken        bool      `json:"hasToken"`
	HasRefreshToken bool      `json:"hasRefreshToken"`
	TokenType       string    `json:"tokenType,omitempty"`
	Account         string    `json:"account,omitempty"`
	Scopes          []string  `json:"scopes,omitempty"`
	ExpiresAt       time.Time `json:"expiresAt,omitempty"`
	Expired         bool      `json:"expired"`
}

// StoreOptions configures where provider OAuth tokens are persisted.
type StoreOptions struct {
	FilePath string
	Env      map[string]string
	Now      func() time.Time
	// Storage selects the backend: "" / "file" => a 0600 JSON file (default);
	// "encrypted-file" => an AES-256-GCM encrypted file; "keyring" => the OS
	// keyring. When empty it falls back to ZERO_OAUTH_STORAGE.
	Storage string
	// Encrypted is a legacy alias for Storage=="encrypted-file" (AES-256-GCM at
	// rest). Ignored when Storage is set.
	Encrypted bool
	// Keyring is the client used when Storage=="keyring"; nil => keyring.New().
	// Injected by tests to avoid touching a real keychain.
	Keyring KeyringClient
}

// KeyringClient is the minimal OS-keyring surface the store needs. *keyring.Keyring
// satisfies it; tests inject a fake.
type KeyringClient interface {
	Get(service, account string) (string, bool, error)
	Set(service, account, secret string) error
	Delete(service, account string) (bool, error)
}

// Keyring storage splits the token blob into one keyring entry per token key,
// plus a small index entry listing which keys exist. A single combined entry
// (the original design) grows with every additional provider/MCP login and,
// on macOS, add-generic-password now goes through `security -i`'s line-based
// command parser (see internal/keyring), which caps a single write at 4095
// bytes; three or more logged-in providers routinely exceeds that. Splitting
// by key bounds each write to one token, which stays well under the cap
// regardless of how many providers are logged in.
const (
	keyringService = "zero"
	// keyringLegacyAccount held the whole blob as one entry in the original
	// design. New writes never use it; it is only read once, to migrate
	// existing installs into the per-key format.
	keyringLegacyAccount = "oauth-tokens"
	// keyringIndexAccount holds a JSON array of the token keys that currently
	// have their own keyring entry, since KeyringClient has no "list" operation.
	keyringIndexAccount = "oauth-tokens-index"
)

// Store persists OAuth tokens (provider + MCP namespaces) as one JSON blob,
// written atomically through a pluggable backend (a 0600 file guarded by a
// cross-process lock, or the OS keyring). When crypter is non-nil the file blob
// is AES-256-GCM ciphertext at rest.
type Store struct {
	blob    blobStore
	crypter *aesGCMCrypter // nil => plaintext blob
	now     func() time.Time
	mu      sync.Mutex
}

type storeFile struct {
	SchemaVersion int              `json:"schemaVersion"`
	Tokens        map[string]Token `json:"tokens"`
}

// ResolveStorePath determines the on-disk location for provider OAuth tokens,
// honoring ZERO_OAUTH_TOKENS_PATH, then XDG_CONFIG_HOME, then the home dir.
func ResolveStorePath(env map[string]string) (string, error) {
	if override := strings.TrimSpace(envValue(env, "ZERO_OAUTH_TOKENS_PATH")); override != "" {
		if filepath.IsAbs(override) {
			return filepath.Clean(override), nil
		}
		return filepath.Abs(override)
	}
	configHome := strings.TrimSpace(envValue(env, "XDG_CONFIG_HOME"))
	if configHome == "" {
		home := strings.TrimSpace(firstNonEmpty(envValue(env, "HOME"), envValue(env, "USERPROFILE")))
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("oauth: resolve user home: %w", err)
			}
		}
		configHome = filepath.Join(home, ".config")
	} else if !filepath.IsAbs(configHome) {
		resolved, err := filepath.Abs(configHome)
		if err != nil {
			return "", err
		}
		configHome = resolved
	}
	return filepath.Join(configHome, "zero", "oauth-tokens.json"), nil
}

// NewStore builds a token store with the configured backend (file by default,
// or the OS keyring when Storage/ZERO_OAUTH_STORAGE selects it).
func NewStore(options StoreOptions) (*Store, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	storage := strings.TrimSpace(options.Storage)
	if storage == "" {
		storage = strings.TrimSpace(envValue(options.Env, "ZERO_OAUTH_STORAGE"))
	}
	if storage == "" && options.Encrypted {
		storage = "encrypted-file" // legacy alias
	}
	switch storage {
	case "", "file":
		path, err := resolveStoreFilePath(options)
		if err != nil {
			return nil, err
		}
		return &Store{blob: fileBlob{path: path}, now: now}, nil
	case "encrypted-file":
		path, err := resolveStoreFilePath(options)
		if err != nil {
			return nil, err
		}
		// The file blob holds AES-256-GCM ciphertext; the per-user secret lives in
		// a sibling ".secret" file (see encrypt.go).
		return &Store{blob: fileBlob{path: path}, crypter: newAESGCMCrypter(path + ".secret"), now: now}, nil
	case "keyring":
		kr := options.Keyring
		if kr == nil {
			osKeyring := keyring.New()
			if !osKeyring.Available() {
				return nil, fmt.Errorf("oauth: keyring storage requested but not available on %s; use file storage", runtime.GOOS)
			}
			kr = osKeyring
		}
		// Serialize the keyring's read-modify-write across processes with a lock
		// file beside where the file backend would live. Cross-process exclusion
		// must not silently disappear when no config location resolves (withLock
		// would be a no-op and a concurrent save could delete another process's
		// newly written entry), so fall back to the OS temp directory, which
		// always exists, rather than to in-process serialization only.
		lockPath := filepath.Join(os.TempDir(), "zero-oauth-keyring.lockfile")
		if storePath, perr := ResolveStorePath(options.Env); perr == nil {
			lockPath = filepath.Join(filepath.Dir(storePath), "oauth-keyring.lockfile")
		}
		return &Store{blob: keyringBlob{kr: kr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount, lockPath: lockPath}, now: now}, nil
	default:
		return nil, fmt.Errorf("oauth: unknown storage %q (want \"file\", \"encrypted-file\", or \"keyring\")", storage)
	}
}

// resolveStoreFilePath resolves the absolute file path for the file backend.
func resolveStoreFilePath(options StoreOptions) (string, error) {
	filePath := options.FilePath
	var err error
	if strings.TrimSpace(filePath) == "" {
		filePath, err = ResolveStorePath(options.Env)
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(filePath) {
		filePath, err = filepath.Abs(filePath)
		if err != nil {
			return "", err
		}
	}
	return filepath.Clean(filePath), nil
}

// FilePath returns the resolved token store location (a path for the file
// backend, or a "keyring:..." identifier for the keyring backend).
func (s *Store) FilePath() string { return s.blob.location() }

// Save persists a token under key, replacing any existing entry.
func (s *Store) Save(key string, token Token) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blob.withLock(s.now, func() error {
		state, err := s.readState()
		if err != nil {
			return err
		}
		state.Tokens[key] = token
		return s.writeState(state)
	})
}

// Load returns the token for key; the bool is false when none is stored.
func (s *Store) Load(key string) (Token, bool, error) {
	if err := ValidateKey(key); err != nil {
		return Token{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Through blob.withReadLock: the keyring backend's read is several
	// separate Get calls (index, then each entry), not one atomic snapshot,
	// so an unguarded Load could run concurrently with another process's
	// Save/Delete mid write and observe a torn state. The file backend's
	// withReadLock is a no-op: its writes are atomic renames, so lock-free
	// reads keep their crash tolerance (a crashed writer's fresh lock file
	// must not block reads of the last complete file).
	var state storeFile
	err := s.blob.withReadLock(s.now, func() error {
		var readErr error
		state, readErr = s.readState()
		return readErr
	})
	if err != nil {
		return Token{}, false, err
	}
	token, ok := state.Tokens[key]
	return token, ok, nil
}

// Delete removes the token for key, reporting whether one was present.
func (s *Store) Delete(key string) (bool, error) {
	if err := ValidateKey(key); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var removed bool
	err := s.blob.withLock(s.now, func() error {
		state, err := s.readState()
		if err != nil {
			return err
		}
		if _, ok := state.Tokens[key]; !ok {
			return nil
		}
		delete(state.Tokens, key)
		removed = true
		return s.writeState(state)
	})
	return removed, err
}

// Status returns redaction-safe summaries of every stored token, sorted by key.
// An optional prefix filters to one namespace (e.g. KeyPrefixProvider).
func (s *Store) Status(prefix string) ([]Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Same reasoning as Load: run the read under blob.withReadLock so the
	// keyring's multi-entry read can't observe another process's Save/Delete
	// mid write, while file-backend reads stay lock-free.
	var state storeFile
	err := s.blob.withReadLock(s.now, func() error {
		var readErr error
		state, readErr = s.readState()
		return readErr
	})
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(state.Tokens))
	for k := range state.Tokens {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	now := s.now()
	out := make([]Status, 0, len(keys))
	for _, k := range keys {
		token := state.Tokens[k]
		out = append(out, Status{
			Key:             k,
			HasToken:        strings.TrimSpace(token.AccessToken) != "",
			HasRefreshToken: strings.TrimSpace(token.RefreshToken) != "",
			TokenType:       token.TokenType,
			Account:         token.Account,
			Scopes:          token.Scopes,
			ExpiresAt:       token.ExpiresAt,
			Expired:         token.Expired(now),
		})
	}
	return out, nil
}

func (s *Store) readState() (storeFile, error) {
	data, ok, err := s.blob.read()
	if err != nil {
		return storeFile{}, err
	}
	if !ok {
		return emptyStoreFile(), nil
	}
	if s.crypter != nil {
		// Encrypted backend: the blob is AES-256-GCM ciphertext, not JSON.
		data, err = s.crypter.open(data)
		if err != nil {
			return storeFile{}, fmt.Errorf("oauth: decrypt token store at %s: %w", s.blob.location(), err)
		}
	}
	var state storeFile
	if err := json.Unmarshal(data, &state); err != nil {
		return storeFile{}, fmt.Errorf("oauth: invalid token store at %s: %w", s.blob.location(), err)
	}
	if state.SchemaVersion != storeSchemaVersion {
		return storeFile{}, fmt.Errorf("oauth: invalid token store at %s: unsupported schemaVersion", s.blob.location())
	}
	if state.Tokens == nil {
		state.Tokens = map[string]Token{}
	}
	for key := range state.Tokens {
		if err := ValidateKey(key); err != nil {
			return storeFile{}, fmt.Errorf("oauth: invalid token store at %s: %w", s.blob.location(), err)
		}
	}
	return state, nil
}

func (s *Store) writeState(state storeFile) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	// Plaintext keeps the trailing newline for a tidy file; the encrypted backend
	// writes opaque ciphertext instead.
	payload := append(data, '\n')
	if s.crypter != nil {
		payload, err = s.crypter.seal(data)
		if err != nil {
			return err
		}
	}
	return s.blob.write(payload)
}

func emptyStoreFile() storeFile {
	return storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{}}
}

// blobStore abstracts the persistence of the whole token blob behind the Store,
// so the same store logic backs either a 0600 file or the OS keyring.
type blobStore interface {
	// read returns the stored blob; ok is false when nothing is stored yet.
	read() (data []byte, ok bool, err error)
	// write replaces the stored blob.
	write(data []byte) error
	// withLock runs fn under whatever cross-process exclusion the backend offers
	// (a lock file for the file backend; none for the keyring, which is the
	// authoritative store and is serialized within the process by Store.mu).
	withLock(now func() time.Time, fn func() error) error
	// withReadLock guards a read-only pass. The file backend's writes are
	// atomic renames, so its reads stay lock-free: a crashed writer's fresh
	// lock file must not turn into ~30s of read failures when the last
	// complete file is perfectly readable. The keyring backend's read is
	// several separate Get calls (index, then each entry), not one atomic
	// snapshot, so it takes the same cross-process lock as its writes.
	withReadLock(now func() time.Time, fn func() error) error
	// location is a human-readable identifier for diagnostics/errors.
	location() string
}

// fileBlob persists the blob as a 0600 JSON file, written atomically and guarded
// by a cross-process lock file. Behavior matches the original file store.
type fileBlob struct{ path string }

func (b fileBlob) read() ([]byte, bool, error) {
	data, err := os.ReadFile(b.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

func (b fileBlob) write(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	tempPath := fmt.Sprintf("%s.tmp-%d-%d", b.path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tempPath, b.path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func (b fileBlob) withLock(now func() time.Time, fn func() error) error {
	unlock, err := acquireFileLock(b.path+".lockfile", now)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// withReadLock is deliberately lock-free: write() replaces the file with an
// atomic rename, so a reader always sees a complete file, and a crashed
// writer's leftover lock file must not turn readable state into ~30 seconds
// of Load/Status failures while the stale threshold runs out.
func (b fileBlob) withReadLock(now func() time.Time, fn func() error) error {
	return fn()
}

func (b fileBlob) location() string { return b.path }

// keyringBlob persists tokens in the OS keyring as one base64 entry per token
// key (account = key), plus an index entry listing which keys exist (base64
// keeps every value a single, control-character-free string; see keyringService
// for why a single combined entry doesn't work). read/write still present the
// same whole-blob shape (a marshaled storeFile) that Store expects, fanning it
// out to/in from the individual entries internally.
type keyringBlob struct {
	kr      KeyringClient
	service string
	// legacyAccount is the pre-migration whole-blob entry; read only, to pick up
	// tokens saved by older versions the first time this runs.
	legacyAccount string
	indexAccount  string
	// lockPath, when set, is a cross-process lock file serializing the keyring's
	// read-modify-write so concurrent processes don't clobber each other's tokens.
	lockPath string
}

func (b keyringBlob) read() ([]byte, bool, error) {
	keys, ok, _, err := b.readKeyIndex()
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return b.readLegacy()
	}
	// The legacy combined entry is consulted lazily (below) only when an indexed
	// key's own entry is missing. write() publishes the index before the per-key
	// entries and deletes the legacy blob only after every entry is written, so a
	// crash partway through the initial legacy->indexed migration can leave a
	// pre-existing credential readable solely in the still-present legacy blob.
	// In steady state (all entries present) the legacy blob is never read.
	var legacyTokens map[string]Token
	legacyLoaded := false
	tokens := make(map[string]Token, len(keys))
	for _, key := range keys {
		enc, ok, err := b.kr.Get(b.service, key)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			// The index lists this key but its own entry is missing. Recover it
			// from the legacy blob when a migration is still in flight; otherwise
			// (a steady-state index/entry desync whose legacy blob is already
			// gone) skip rather than fail the whole read, since the next
			// Save/Delete will reconcile the index.
			if !legacyLoaded {
				legacyTokens = b.readLegacyTokens()
				legacyLoaded = true
			}
			if token, has := legacyTokens[key]; has {
				tokens[key] = token
			}
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
		if err != nil {
			return nil, false, fmt.Errorf("oauth: decode keyring token entry %q: %w", key, err)
		}
		var token Token
		if err := json.Unmarshal(raw, &token); err != nil {
			return nil, false, fmt.Errorf("oauth: invalid keyring token entry %q: %w", key, err)
		}
		tokens[key] = token
	}
	data, err := json.Marshal(storeFile{SchemaVersion: storeSchemaVersion, Tokens: tokens})
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// readLegacy reads the pre-migration whole-blob entry, for installs that
// haven't written since upgrading. The next write() migrates them: it writes
// per-key entries and an index, then deletes this entry.
func (b keyringBlob) readLegacy() ([]byte, bool, error) {
	enc, ok, err := b.kr.Get(b.service, b.legacyAccount)
	if err != nil || !ok {
		return nil, ok, err
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		return nil, false, fmt.Errorf("oauth: decode keyring token blob: %w", err)
	}
	return data, true, nil
}

// readLegacyTokens returns the tokens held in the legacy combined entry, or an
// empty map when there is no readable legacy blob. It is a best-effort recovery
// source (read() falls back to it, write() reconciles against it), so a missing
// or malformed legacy entry is reported as "no tokens" rather than a hard error.
func (b keyringBlob) readLegacyTokens() map[string]Token {
	data, ok, err := b.readLegacy()
	if err != nil || !ok {
		return nil
	}
	var legacyState storeFile
	if json.Unmarshal(data, &legacyState) != nil {
		return nil
	}
	return legacyState.Tokens
}

// legacyIsFresher reports whether the legacy copy of an already-indexed key
// should win over the indexed copy. An old binary running alongside the new one
// refreshes tokens only in the legacy combined entry, and a refresh pushes the
// expiry later, so a strictly later, non-zero expiry on the legacy side is the
// signal that it holds a newer credential. A zero (unknown) expiry on either
// side is not evidence of freshness, so the indexed value is kept.
func legacyIsFresher(legacy, current Token) bool {
	return !legacy.ExpiresAt.IsZero() && !current.ExpiresAt.IsZero() && legacy.ExpiresAt.After(current.ExpiresAt)
}

// write replaces the keyring's token entries with state, ordered so that
// every interruption boundary leaves a recoverable store. The invariant is
// that any token entry existing in the keyring at any instant is listed in
// the published index: the union index is published before entries are
// written, entries are deleted before the index shrinks, and the index
// header is only updated after the chunks it references exist. A crash at
// any step therefore leaves either an index over-listing keys whose entries
// are missing (read() recovers those from the legacy blob during a migration,
// or skips them once it is gone) or entries that a later read/write can still
// see and reconcile, never an invisible credential stranded in the OS keychain.
// The legacy combined entry is the durable fallback for the initial migration
// and is deleted only as the final step, after every per-key entry is written.
func (b keyringBlob) write(data []byte) error {
	var state storeFile
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("oauth: encode keyring token blob: %w", err)
	}
	priorKeys, indexExisted, priorChunks, err := b.readKeyIndex()
	if err != nil {
		return err
	}
	prior := make(map[string]bool, len(priorKeys))
	for _, key := range priorKeys {
		prior[key] = true
	}

	// An older binary running alongside this one still reads and writes only the
	// legacy combined entry. If that entry exists even though the index has
	// already been published, an old binary wrote it after migration, so
	// reconcile it into state before it is deleted below rather than blindly
	// overwriting it:
	//   - a key the indexed schema has never seen is a fresh old-binary login;
	//     merge it so it is not lost;
	//   - a key already present in state that the legacy blob refreshed (a
	//     strictly later expiry) takes the legacy value, so a concurrent
	//     old-binary refresh is not discarded in favor of the stale indexed one;
	//   - a key that was in the prior index but is absent from this write was
	//     deliberately removed (a logout); it is left removed, not resurrected.
	if indexExisted {
		for key, legacyToken := range b.readLegacyTokens() {
			if ValidateKey(key) != nil {
				continue
			}
			if current, exists := state.Tokens[key]; exists {
				if legacyIsFresher(legacyToken, current) {
					state.Tokens[key] = legacyToken
				}
				continue
			}
			if prior[key] {
				continue
			}
			state.Tokens[key] = legacyToken
		}
	}

	keys := make([]string, 0, len(state.Tokens))
	for key := range state.Tokens {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// 1. Publish the union of the prior and new key sets first, so every
	// entry that exists at any point during this update is indexed.
	union := keys
	if len(priorKeys) > 0 {
		merged := make(map[string]bool, len(keys)+len(priorKeys))
		for _, key := range append(append([]string{}, keys...), priorKeys...) {
			merged[key] = true
		}
		union = make([]string, 0, len(merged))
		for key := range merged {
			union = append(union, key)
		}
		sort.Strings(union)
	}
	unionChunks, err := b.writeKeyIndex(union, priorChunks)
	if err != nil {
		return err
	}
	// 2. Write each token entry.
	for _, key := range keys {
		raw, err := json.Marshal(state.Tokens[key])
		if err != nil {
			return err
		}
		if err := b.kr.Set(b.service, key, base64.StdEncoding.EncodeToString(raw)); err != nil {
			return err
		}
	}
	// 3. Delete removed entries while the union index still lists them, so a
	// failed Delete leaves a visible (re-deletable) entry, never an orphan.
	for _, key := range priorKeys {
		if _, ok := state.Tokens[key]; !ok {
			if _, err := b.kr.Delete(b.service, key); err != nil {
				return err
			}
		}
	}
	// 4. Shrink the index to the exact new key set.
	if _, err := b.writeKeyIndex(keys, unionChunks); err != nil {
		return err
	}
	// The index now exists and is authoritative; drop the legacy entry so a
	// future read never falls back to it (its fresh writes were merged above).
	_, _ = b.kr.Delete(b.service, b.legacyAccount)
	return nil
}

// maxKeyringIndexChunkBytes bounds one index chunk's raw JSON payload so its
// base64 encoding plus command framing stays well under the macOS
// `security -i` 4095-byte line cap (see internal/keyring): 2700 raw bytes
// expand to 3600 base64 bytes, leaving ~490 bytes for the add-generic-password
// syntax, service, and account. The old single-entry index hit that cap at
// roughly 22 maximum-length keys even when every token was tiny.
const maxKeyringIndexChunkBytes = 2700

// maxKeyringIndexChunks caps how many chunk entries a stored index header may
// claim before readKeyIndex issues one OS-keyring lookup per chunk. Each chunk
// holds up to maxKeyringIndexChunkBytes of keys (dozens to ~150 keys), so this
// bound admits far more logins than any real install while refusing to fan a
// corrupt header (e.g. {"v":1,"chunks":1000000000}) out into a billion blocking
// lookups that would wedge every OAuth operation under the store lock.
const maxKeyringIndexChunks = 128

// keyIndexHeader is chunk 0 of the key index. Chunks 1..Chunks-1 live under
// "<indexAccount>-<n>" as plain JSON string arrays. The pre-chunking format
// (a bare JSON array at indexAccount) is still read transparently.
type keyIndexHeader struct {
	Version int      `json:"v"`
	Chunks  int      `json:"chunks"`
	Keys    []string `json:"keys"`
}

func (b keyringBlob) chunkAccount(index int) string {
	return fmt.Sprintf("%s-%d", b.indexAccount, index)
}

// readKeyIndex returns the indexed keys, whether an index exists at all, and
// how many chunk entries it currently occupies. A chunk listed by the header
// but missing from the keyring (a torn write) is skipped, mirroring how
// read() skips an indexed key whose entry is missing.
func (b keyringBlob) readKeyIndex() ([]string, bool, int, error) {
	enc, ok, err := b.kr.Get(b.service, b.indexAccount)
	if err != nil {
		return nil, false, 0, err
	}
	if !ok {
		return nil, false, 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		return nil, false, 0, fmt.Errorf("oauth: decode keyring token index: %w", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var keys []string
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, false, 0, fmt.Errorf("oauth: decode keyring token index: %w", err)
		}
		return keys, true, 1, nil
	}
	var header keyIndexHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, false, 0, fmt.Errorf("oauth: decode keyring token index: %w", err)
	}
	// Reject an unsupported or corrupt header before looping: an out-of-range
	// Chunks would otherwise drive up to that many blocking keyring lookups
	// (each up to the 10s command timeout) while the store lock is held, wedging
	// every Load/Status/Save/Delete instead of failing promptly.
	if header.Version != 1 {
		return nil, false, 0, fmt.Errorf("oauth: unsupported keyring token index version %d", header.Version)
	}
	if header.Chunks < 1 || header.Chunks > maxKeyringIndexChunks {
		return nil, false, 0, fmt.Errorf("oauth: keyring token index advertises %d chunks (want 1..%d)", header.Chunks, maxKeyringIndexChunks)
	}
	keys := header.Keys
	for i := 1; i < header.Chunks; i++ {
		chunkEnc, ok, err := b.kr.Get(b.service, b.chunkAccount(i))
		if err != nil {
			return nil, false, 0, err
		}
		if !ok {
			continue
		}
		chunkRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(chunkEnc))
		if err != nil {
			return nil, false, 0, fmt.Errorf("oauth: decode keyring token index chunk %d: %w", i, err)
		}
		var more []string
		if err := json.Unmarshal(chunkRaw, &more); err != nil {
			return nil, false, 0, fmt.Errorf("oauth: decode keyring token index chunk %d: %w", i, err)
		}
		keys = append(keys, more...)
	}
	return keys, true, header.Chunks, nil
}

// writeKeyIndex persists keys as a chunked index and reports how many chunk
// entries it used. Continuation chunks are written before the header that
// references them, so the authoritative chunk 0 never advertises a chunk that
// does not exist yet; stale chunks from a previously larger index are removed
// only after the header stops referencing them (best-effort: an unreferenced
// chunk is never read).
func (b keyringBlob) writeKeyIndex(keys []string, priorChunks int) (int, error) {
	chunks := chunkIndexKeys(keys)
	for i := 1; i < len(chunks); i++ {
		chunkData, err := json.Marshal(chunks[i])
		if err != nil {
			return 0, err
		}
		if err := b.kr.Set(b.service, b.chunkAccount(i), base64.StdEncoding.EncodeToString(chunkData)); err != nil {
			return 0, err
		}
	}
	headerData, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: len(chunks), Keys: chunks[0]})
	if err != nil {
		return 0, err
	}
	if err := b.kr.Set(b.service, b.indexAccount, base64.StdEncoding.EncodeToString(headerData)); err != nil {
		return 0, err
	}
	for i := len(chunks); i < priorChunks; i++ {
		_, _ = b.kr.Delete(b.service, b.chunkAccount(i))
	}
	return len(chunks), nil
}

// chunkIndexKeys packs keys into chunks whose marshaled JSON stays under
// maxKeyringIndexChunkBytes. Always returns at least one (possibly empty)
// chunk.
func chunkIndexKeys(keys []string) [][]string {
	chunks := [][]string{{}}
	size := 0
	for _, key := range keys {
		// Per-key JSON cost: quotes, comma, and headroom for escaping.
		cost := len(key) + 8
		if size+cost > maxKeyringIndexChunkBytes && len(chunks[len(chunks)-1]) > 0 {
			chunks = append(chunks, []string{})
			size = 0
		}
		chunks[len(chunks)-1] = append(chunks[len(chunks)-1], key)
		size += cost
	}
	return chunks
}

// fileLockRefreshInterval is how often a held keyring lock's mtime is
// refreshed while its critical section runs. It must stay comfortably under
// fileLockStaleAfter (30s): one external keyring command may legitimately
// take up to its 10s timeout and a multi-entry pass runs several, so without
// refreshing, a healthy slow holder would look stale and another process
// could reclaim the live lock and resume the token-loss race the lock
// exists to prevent. A var so tests can shorten it.
var fileLockRefreshInterval = 10 * time.Second

// withLock serializes the keyring's read-modify-write. Store.mu covers the
// in-process case; lockPath (when set) adds cross-process exclusion so two
// processes can't both read the blob, modify, and write — dropping a token.
// While fn runs, the lock file's mtime is refreshed so the stale-reclaim
// threshold only ever expires for a genuinely crashed holder.
func (b keyringBlob) withLock(now func() time.Time, fn func() error) error {
	if b.lockPath == "" {
		return fn()
	}
	unlock, err := acquireFileLock(b.lockPath, now)
	if err != nil {
		return err
	}
	defer unlock()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(fileLockRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// Lease with wall-clock time, never the injectable now: acquireFileLock
				// judges staleness with real time.Since(mtime), so a fixed or stale
				// StoreOptions.Now would stamp the live lock with an old mtime that
				// another process would immediately reclaim, reviving the token-loss
				// race this lease prevents.
				at := time.Now()
				_ = os.Chtimes(b.lockPath, at, at)
			}
		}
	}()
	err = fn()
	close(stop)
	<-done
	return err
}

func (b keyringBlob) withReadLock(now func() time.Time, fn func() error) error {
	return b.withLock(now, fn)
}

func (b keyringBlob) location() string { return "keyring:" + b.service + "/" + b.indexAccount }

// FormatStatuses renders a human-readable status table without leaking token
// material.
func FormatStatuses(statuses []Status) string {
	if len(statuses) == 0 {
		return "No OAuth provider logins are stored."
	}
	var b strings.Builder
	for i, st := range statuses {
		if i > 0 {
			b.WriteByte('\n')
		}
		name := strings.TrimPrefix(st.Key, KeyPrefixProvider)
		b.WriteString(name)
		b.WriteString(": ")
		if !st.HasToken {
			b.WriteString("no token")
			continue
		}
		b.WriteString("logged in")
		if st.Account != "" {
			b.WriteString(" as " + st.Account)
		}
		if st.HasRefreshToken {
			b.WriteString(" (refreshable)")
		}
		if !st.ExpiresAt.IsZero() {
			if st.Expired {
				b.WriteString(", expired at ")
			} else {
				b.WriteString(", expires ")
			}
			b.WriteString(st.ExpiresAt.UTC().Format(time.RFC3339))
		}
	}
	return b.String()
}

// envValue reads a variable. A non-nil env map is authoritative (hermetic): a
// missing key returns "" rather than falling back to the process environment, so
// a caller/test that passes a controlled map can never pick up ambient
// ZERO_OAUTH_* / HOME / XDG_CONFIG_HOME values. Only a nil map uses os.Getenv.
func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
