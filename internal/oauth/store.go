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
		// file beside where the file backend would live. Best-effort: if no config
		// location resolves, fall back to in-process serialization only.
		lockPath := ""
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
	// Through blob.withLock, like Save/Delete: the keyring backend's read is
	// several separate Get calls (index, then each entry), not one atomic
	// snapshot, so an unlocked Load can run concurrently with another
	// process's Save/Delete mid write and observe a torn state (e.g. an index
	// already updated but an entry not yet written). The lock keeps this read
	// from overlapping any other process's read-modify-write cycle.
	var state storeFile
	err := s.blob.withLock(s.now, func() error {
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
	// Same reasoning as Load: run the read under blob.withLock so it can't
	// observe another process's Save/Delete mid write.
	var state storeFile
	err := s.blob.withLock(s.now, func() error {
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
	indexEnc, ok, err := b.kr.Get(b.service, b.indexAccount)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return b.readLegacy()
	}
	keys, err := decodeKeyIndex(indexEnc)
	if err != nil {
		return nil, false, fmt.Errorf("oauth: decode keyring token index: %w", err)
	}
	tokens := make(map[string]Token, len(keys))
	for _, key := range keys {
		enc, ok, err := b.kr.Get(b.service, key)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			// Index and entries fell out of sync (e.g. a killed process between
			// writing an entry and updating the index); skip rather than fail the
			// whole read, since the next Save/Delete will reconcile the index.
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

func (b keyringBlob) write(data []byte) error {
	var state storeFile
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("oauth: encode keyring token blob: %w", err)
	}
	priorKeys, err := b.indexedKeys()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(state.Tokens))
	for key, token := range state.Tokens {
		raw, err := json.Marshal(token)
		if err != nil {
			return err
		}
		if err := b.kr.Set(b.service, key, base64.StdEncoding.EncodeToString(raw)); err != nil {
			return err
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	indexData, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if err := b.kr.Set(b.service, b.indexAccount, base64.StdEncoding.EncodeToString(indexData)); err != nil {
		return err
	}
	for _, key := range priorKeys {
		if _, ok := state.Tokens[key]; !ok {
			if _, err := b.kr.Delete(b.service, key); err != nil {
				return err
			}
		}
	}
	// The index now exists and is authoritative; drop the legacy entry so a
	// future read never falls back to it.
	_, _ = b.kr.Delete(b.service, b.legacyAccount)
	return nil
}

// indexedKeys returns the keys currently listed in the index, or nil if there
// is no index yet (first write, or still on the legacy format).
func (b keyringBlob) indexedKeys() ([]string, error) {
	enc, ok, err := b.kr.Get(b.service, b.indexAccount)
	if err != nil || !ok {
		return nil, err
	}
	keys, err := decodeKeyIndex(enc)
	if err != nil {
		return nil, fmt.Errorf("oauth: decode keyring token index: %w", err)
	}
	return keys, nil
}

func decodeKeyIndex(enc string) ([]string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// withLock serializes the keyring's read-modify-write. Store.mu covers the
// in-process case; lockPath (when set) adds cross-process exclusion so two
// processes can't both read the blob, modify, and write — dropping a token.
func (b keyringBlob) withLock(now func() time.Time, fn func() error) error {
	if b.lockPath == "" {
		return fn()
	}
	unlock, err := acquireFileLock(b.lockPath, now)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
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
