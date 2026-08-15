package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

var currentOSUser = user.Current

// lookupUserID resolves an OS account by uid; a var so the keyring-lock
// fallback can be tested hermetically without touching the real user database.
var lookupUserID = user.LookupId

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
//
// Coexistence with pre-per-key binaries: the legacy combined entry is a
// read-only discovery source for new code. New writers never overwrite it
// (they cannot share a lock with old writers on other config roots, so any
// snapshot-then-Set would clobber unobserved updates or truncate oversized
// Linux keyring maps). Indexed per-key entries are the sole writable
// representation for new binaries. Durable deletion markers (tombstones)
// prevent an uncoordinated old writer from resurrecting a logout via the
// legacy blob.
const (
	keyringService = "zero"
	// keyringLegacyAccount is the combined-blob entry used by pre-per-key
	// binaries. New code reads it for migration and for legacy-only logins,
	// but never writes or deletes it: dual-write cannot be made safe across
	// config roots that do not share legacyKeyringLockPath.
	keyringLegacyAccount = "oauth-tokens"
	// keyringIndexAccount holds a JSON array of the token keys that currently
	// have their own keyring entry, since KeyringClient has no "list" operation.
	keyringIndexAccount = "oauth-tokens-index"
	// keyringTombstoneAccount holds the set of keys deliberately deleted by a
	// new binary. Old writers cannot see this entry; new readers and writers
	// honor it so a stale legacy rewrite cannot resurrect a logout.
	keyringTombstoneAccount = "oauth-tokens-tombstones"
	// keyringLegacyOriginAccount holds the set of keys ever observed in the
	// legacy combined entry. It lets write() tell "a pre-migration binary
	// removed this key" apart from "this key was never in the legacy blob to
	// begin with" — both look identical as a single absence, but only the
	// first is a logout that must propagate as one.
	keyringLegacyOriginAccount = "oauth-tokens-legacy-origin"
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
		home, err := resolveHomeDir(env)
		if err != nil {
			return "", err
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

// resolveHomeDir returns the user's home directory, honoring HOME/USERPROFILE
// hermetically (via env) before falling back to os.UserHomeDir(). Used by
// ResolveStorePath's config-root fallback. Unlike ResolveStorePath,
// keyringLockPath anchors directly on OS identity (currentOSUser /
// keyringFallbackLockDir) so the keyring lock never varies with per-process
// environment overrides.
func resolveHomeDir(env map[string]string) (string, error) {
	if home := strings.TrimSpace(firstNonEmpty(envValue(env, "HOME"), envValue(env, "USERPROFILE"))); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("oauth: resolve user home: %w", err)
	}
	return home, nil
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
		// lockPath serializes this binary's own keyring read-modify-write across
		// processes, keyed off the keyring identity itself (service + index
		// account) and anchored on the user's home directory (see
		// keyringLockPath), never off a per-process cache/temp/config override:
		// two processes with different roots but pointed at the SAME OS keyring
		// entry (the service/account is fixed per binary, not per config root)
		// must still serialize against each other, or they can race a
		// read-modify-write on the shared keyring index and silently drop one
		// process's token write. legacyLockPath additionally coordinates with a
		// still-running pre-PR binary during the supported mixed-version window
		// (see legacyKeyringLockPath).
		lockPath, err := keyringLockPath(keyringService, keyringIndexAccount)
		if err != nil {
			return nil, err
		}
		legacyLockPath := legacyKeyringLockPath(options.Env)
		return &Store{blob: keyringBlob{kr: kr, service: keyringService, legacyAccount: keyringLegacyAccount, indexAccount: keyringIndexAccount, lockPath: lockPath, legacyLockPath: legacyLockPath}, now: now}, nil
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

// keyringLockPath returns the cross-process lock file location for the
// keyring backend's read-modify-write, derived from the keyring identity
// itself (service + index account) and anchored on the user's OS home directory
// (via user.Current()) rather than caller-controlled environment overrides
// like HOME, XDG_CACHE_HOME, or TMPDIR: those pick different paths per process
// (sandboxes, CI harnesses, launcher profiles), so two processes for the same
// OS user would take different lock files while writing to the same OS keychain.
// When the OS user lookup fails, the fallback is a private UID-scoped directory
// under the process temp root (validated 0700, owned by us); a co-tenant DoS
// of the shared /tmp name is rejected rather than accepted as the lock path.
func keyringLockPath(service, account string) (string, error) {
	name := keyringLockFileName(service, account)
	if u, err := currentOSUser(); err == nil && strings.TrimSpace(u.HomeDir) != "" {
		dir := filepath.Join(u.HomeDir, ".cache", "zero")
		if err := validateOAuthLockDir(dir); err == nil {
			return filepath.Join(dir, name), nil
		}
	}
	// Do not fall back to os.UserHomeDir: it reads ambient HOME/USERPROFILE, so
	// two same-user processes can choose different locks for one keyring.
	dir, err := keyringFallbackLockDir()
	if err != nil {
		return "", fmt.Errorf("oauth: keyring lock fallback dir: %w", err)
	}
	return filepath.Join(dir, name), nil
}

// validateOAuthLockDir creates dir with 0700 permissions if needed, rejects
// symlinks and non-directories, tightens permissions to 0700, and validates
// ownership by the current user.
func validateOAuthLockDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("oauth lock fallback %s is not a plain directory", dir)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("tighten oauth lock fallback permissions: %w", err)
		}
	}
	if err := checkOAuthLockDirOwner(info); err != nil {
		return err
	}
	return nil
}

// keyringFallbackLockDir returns a private directory for last-resort keyring
// locks when user.Current cannot resolve a home. It first tries the OS user
// database by uid (identity-stable, independent of HOME/XDG/TMPDIR env) so the
// fallback lands on the SAME home-anchored cache dir as the primary branch,
// giving every process of the user one lock file. If even that lookup fails,
// it falls back to a uid-scoped 0700 directory under the FIXED /tmp root —
// never os.TempDir(), which honors the per-process TMPDIR override: two
// same-user processes with different TMPDIR values would otherwise compute
// different lock directories while writing the same keyring index and race it.
// Ownership and mode of the /tmp directory are validated so a co-tenant cannot
// pre-create it and deny OAuth. Windows temp is already per-user.
func keyringFallbackLockDir() (string, error) {
	if runtime.GOOS == "windows" {
		// Not os.TempDir(): see identityLockRoot for why %TMP%/%TEMP% cannot
		// anchor a lock two processes of the same user must agree on.
		return identityLockRoot()
	}
	var homeErr error
	if uid := os.Getuid(); uid >= 0 {
		if u, err := lookupUserID(strconv.Itoa(uid)); err == nil && strings.TrimSpace(u.HomeDir) != "" {
			dir := filepath.Join(u.HomeDir, ".cache", "zero")
			if err := validateOAuthLockDir(dir); err == nil {
				return dir, nil
			} else {
				homeErr = err
			}
		} else if err != nil {
			homeErr = err
		}
	}
	name := "zero-oauth-locks"
	if uid := os.Getuid(); uid >= 0 {
		name = fmt.Sprintf("zero-oauth-locks-%d", uid)
	}
	dir := filepath.Join("/tmp", name)
	if err := validateOAuthLockDir(dir); err != nil {
		if homeErr != nil {
			return "", fmt.Errorf("oauth lock fallback /tmp: %w; prior home lookup error: %v", err, homeErr)
		}
		return "", err
	}
	return dir, nil
}

// legacyKeyringLockPath returns the lock file a pre-PR binary acquires around
// its own read-modify-write of the single combined keyring entry, beside
// wherever ResolveStorePath resolves the file-backend location for that
// process's env. A new binary must take this SAME lock (not just its own
// keyringLockPath) around any write that reconciles or dual-writes the legacy
// entry when the old binary shares this config root. Old binaries on other
// roots cannot share this lock; dual-write-without-delete is the safety net
// for that case. Best-effort: "" when the file-backend location can't be
// resolved at all, matching the legacy code's own best-effort fallback.
func legacyKeyringLockPath(env map[string]string) string {
	// Use ResolveStorePath so the legacy lock lives beside whatever the
	// legacy binary actually stores to (honoring ZERO_OAUTH_TOKENS_PATH
	// and XDG_CONFIG_HOME), matching the old binary's own lock path.
	storePath, err := ResolveStorePath(env)
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(storePath), "oauth-keyring.lockfile")
}

// keyringLockFileName names the lock file after the keyring identity it
// guards, so distinct (service, account) pairs never share a lock and the
// same pair always resolves to the same lock regardless of caller config.
func keyringLockFileName(service, account string) string {
	return fmt.Sprintf("oauth-keyring-%s-%s.lockfile", sanitizeLockComponent(url.QueryEscape(service)), sanitizeLockComponent(url.QueryEscape(account)))
}

// lockComponentSafe keeps a service/account string safe as one path segment:
// alphanumerics, dot, underscore, and hyphen pass through; anything else
// (a path separator, especially) is replaced so a crafted identity can never
// escape the lock directory.
var lockComponentSafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeLockComponent(s string) string {
	return lockComponentSafe.ReplaceAllString(s, "_")
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
	return s.blob.withLock(s.now, func(check leaseCheck) error {
		state, err := s.readState()
		if err != nil {
			return err
		}
		state.Tokens[key] = token
		return s.writeState(state, map[string]bool{key: false}, check)
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
	err := s.blob.withReadLock(s.now, func(check leaseCheck) error {
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
	err := s.blob.withLock(s.now, func(check leaseCheck) error {
		state, err := s.readState()
		if err != nil {
			return err
		}
		if _, ok := state.Tokens[key]; ok {
			delete(state.Tokens, key)
			removed = true
			return s.writeState(state, map[string]bool{key: true}, check)
		}
		// readState no longer exposes the key (a tombstone hides it, or an
		// interrupted delete already finished the logical logout) but a keyring
		// entry for it survives. Reconciling that residue still deletes a
		// credential-bearing entry, so report it as removed: `zero auth logout`
		// surfaces this boolean directly and "nothing removed" would be wrong
		// while per-key deletes and an index shrink are running.
		if kb, ok := s.blob.(keyringBlob); ok {
			resident, rerr := kb.hasResidentEntry(key)
			if rerr != nil {
				return rerr
			}
			if resident {
				removed = true
				return s.writeState(state, map[string]bool{key: true}, check)
			}
		}
		return nil
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
	err := s.blob.withReadLock(s.now, func(check leaseCheck) error {
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

// writeState persists state. mutations identifies explicitly saved (false) and
// deleted (true) keys. The keyring backend uses it to order durable tombstone
// transitions; file and encrypted-file backends ignore it.
func (s *Store) writeState(state storeFile, mutations map[string]bool, checkLease leaseCheck) error {
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
	return s.blob.write(payload, mutations, checkLease)
}

func emptyStoreFile() storeFile {
	return storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{}}
}

// blobStore abstracts the persistence of the whole token blob behind the Store,
// so the same store logic backs either a 0600 file or the OS keyring.
type blobStore interface {
	// read returns the stored blob; ok is false when nothing is stored yet.
	read() (data []byte, ok bool, err error)
	// write replaces the stored blob. mutations is keyring-only and identifies
	// explicit saves (false) and deletes (true) for durable tombstone ordering.
	// File backends ignore it. checkLease must be consulted immediately before
	// each externally visible mutation write() performs; see leaseCheck.
	write(data []byte, mutations map[string]bool, checkLease leaseCheck) error
	// withLock runs fn under whatever cross-process exclusion the backend offers
	// (a lock file for the file backend; leased lock files for the keyring backend;
	// Store.mu provides in-process serialization).
	// fn receives a leaseCheck to consult before mutating; see its doc comment.
	withLock(now func() time.Time, fn func(check leaseCheck) error) error
	// withReadLock guards a read-only pass. The file backend's writes are
	// atomic renames, so its reads stay lock-free: a crashed writer's fresh
	// lock file must not turn into ~30s of read failures when the last
	// complete file is perfectly readable. The keyring backend's read is
	// several separate Get calls (index, then each entry), not one atomic
	// snapshot, so it takes the same cross-process lock as its writes.
	withReadLock(now func() time.Time, fn func(check leaseCheck) error) error
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

func (b fileBlob) write(data []byte, _ map[string]bool, _ leaseCheck) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	temp, tempPath, err := createPublicationFile(b.path)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, b.path)
}

// PublicationDirSuffix names the per-store directory a token store publishes new
// contents through. Sandbox profiles deny it by name (see
// internal/sandbox.credentialPublicationDir), which is why the directory name is
// derived from the store path while the file inside it is randomly named: the
// deterministic part is what a deny rule can reference, and the random part is
// what stops a same-user process from waiting for the plaintext to appear at a
// path it can open or rename away.
const PublicationDirSuffix = ".publish"

// PublicationDir returns the publication directory for a store path.
func PublicationDir(path string) string { return path + PublicationDirSuffix }

// createPublicationFile creates the randomly-named 0600 file that path's next
// contents are written to before being renamed into place. It lives in
// PublicationDir(path) — same filesystem as path, so the rename stays atomic —
// and the directory is created 0700 if it does not exist yet.
func createPublicationFile(path string) (*os.File, string, error) {
	dir := PublicationDir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	temp, err := os.CreateTemp(dir, "publish-*")
	if err != nil {
		return nil, "", err
	}
	if err := temp.Chmod(0o600); err != nil {
		name := temp.Name()
		_ = temp.Close()
		_ = os.Remove(name)
		return nil, "", err
	}
	return temp, temp.Name(), nil
}

// noLeaseLoss is the fileBlob checkLease: acquireFileLock's O_EXCL lock has no
// reclaim path (unlike the keyring's mtime-based staleness reclaim), so there
// is no ownership loss for fn to observe mid-critical-section.
func noLeaseLoss() error { return nil }

func (b fileBlob) withLock(now func() time.Time, fn func(check leaseCheck) error) error {
	unlock, _, err := acquireFileLock(b.path+".lockfile", now)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	fnErr := fn(noLeaseLoss)
	if uerr := unlock(); uerr != nil && fnErr == nil {
		return uerr
	}
	return fnErr
}

// withReadLock is deliberately lock-free: write() replaces the file with an
// atomic rename, so a reader always sees a complete file, and a crashed
// writer's leftover lock file must not turn readable state into ~30 seconds
// of Load/Status failures while the stale threshold runs out.
func (b fileBlob) withReadLock(now func() time.Time, fn func(check leaseCheck) error) error {
	return fn(noLeaseLoss)
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
	// tokens saved by older versions and legacy-only logins from old binaries.
	// New code never writes this account (see package comment on coexistence).
	legacyAccount string
	indexAccount  string
	// lockPath, when set, is a cross-process lock file serializing the keyring's
	// read-modify-write so concurrent processes don't clobber each other's tokens.
	lockPath string
	// legacyLockPath, when set, is the lock file a pre-PR binary acquires around
	// its own read-modify-write of the legacy combined entry (see
	// legacyKeyringLockPath). write() still holds it when the old binary shares
	// this config root so concurrent legacy mutations serialize with our
	// reconcile-and-index pass. Cross-root old writers cannot share that lock;
	// safety there comes from never overwriting the legacy blob and from
	// durable tombstones, not from dual-write.
	legacyLockPath string
	// maxIndexKeys overrides the live credential cap for bounded metadata
	// indexes, such as tombstones, that do not fan out into per-key reads.
	maxIndexKeys int
}

// hasResidentEntry reports whether key is still named in the key index or
// has an individual entry in the OS keyring. Used during Store.Delete to
// finish reconciling interrupted deletes whose tombstones already hide the key
// from readState().
func (b keyringBlob) hasResidentEntry(key string) (bool, error) {
	keys, ok, _, _, _, err := b.readKeyIndex()
	if err != nil {
		return false, err
	}
	if ok {
		for _, k := range keys {
			if k == key {
				return true, nil
			}
		}
	}
	_, exists, gerr := b.kr.Get(b.service, key)
	if gerr != nil {
		return false, gerr
	}
	return exists, nil
}

func (b keyringBlob) read() ([]byte, bool, error) {
	keys, ok, _, _, _, err := b.readKeyIndex()
	if err != nil {
		return nil, false, err
	}
	// Tombstones are authoritative even before the first index commits. A
	// delete can persist its marker and then be interrupted while publishing the
	// initial index; returning the untouched legacy blob here would resurrect it.
	tombstones, err := b.readTombstones()
	if err != nil {
		return nil, false, err
	}
	if !ok {
		data, legacyOK, err := b.readLegacy()
		if err != nil || !legacyOK || len(tombstones) == 0 {
			return data, legacyOK, err
		}
		var state storeFile
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, false, fmt.Errorf("oauth: invalid legacy keyring token blob: %w", err)
		}
		for key := range tombstones {
			delete(state.Tokens, key)
		}
		filtered, err := json.Marshal(state)
		return filtered, true, err
	}
	// Tombstones block resurrection of deliberately deleted keys from the
	// legacy combined entry (an old binary may rewrite a stale snapshot into
	// that account after logout). Fail closed on a corrupt tombstone set so a
	// damaged marker cannot silently re-expose logged-out credentials.
	// The legacy combined entry is consulted when an indexed key's own entry is
	// missing (torn write / migration) and for keys only present there (an old
	// binary logged into a provider this process has never indexed). Indexed
	// entries always win over legacy for the same key: expiry and token material
	// are not a causal version vector, so preferring "fresher-looking" legacy
	// can overwrite an explicit new-binary Save with an older account's token.
	var legacyTokens map[string]Token
	legacyLoaded := false
	loadLegacy := func() {
		if legacyLoaded {
			return
		}
		// Best-effort on read: a transient failure must not fail Load/Status,
		// only skip legacy recovery for this pass. write() still requires a
		// successful legacy read before reconciling so it never mistakes a
		// transient error for an empty blob.
		if lt, lerr := b.readLegacyTokens(); lerr == nil {
			legacyTokens = lt
		}
		legacyLoaded = true
	}
	tokens := make(map[string]Token, len(keys))
	for _, key := range keys {
		if tombstones[key] {
			continue
		}
		enc, ok, err := b.kr.Get(b.service, key)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			// The index lists this key but its own entry is missing. Recover it
			// from the legacy blob when present and not tombstoned; otherwise
			// skip rather than fail the whole read (the next Save/Delete prunes
			// the phantom index key so it cannot permanently consume capacity).
			loadLegacy()
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

	// Keep legacy-only keys visible through the compatibility window:
	// an old binary may have logged into a provider after the index was created.
	// Tombstones suppress keys the user already logged out of.
	loadLegacy()
	for key, legacyToken := range legacyTokens {
		if ValidateKey(key) != nil {
			continue
		}
		if tombstones[key] {
			continue
		}
		if _, has := tokens[key]; !has {
			tokens[key] = legacyToken
		}
	}
	// A running pre-migration binary can only log out through the legacy
	// entry, and that logout is invisible here as anything but the key's
	// absence from legacyTokens — indistinguishable, on its own, from a key
	// this process's index just happens to also hold that legacy never knew
	// about. legacyOrigin (populated by write(), the only place with a
	// durable record to consult) disambiguates the two: only a key ever
	// actually observed in legacy is subject to this exclusion, so a
	// purely-new-format credential is never hidden the moment it is absent
	// from an entry it was never written to in the first place. Applied here
	// transiently (not persisted): the durable version happens in write(),
	// consistent with every other best-effort check in this function.
	legacyOrigin, lerr := b.readLegacyOrigin()
	if lerr == nil && len(legacyOrigin) > 0 {
		for key := range tokens {
			if !legacyOrigin[key] {
				continue
			}
			if legacyTokens != nil {
				if _, stillInLegacy := legacyTokens[key]; stillInLegacy {
					continue
				}
			}
			delete(tokens, key)
		}
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

// readLegacyTokens returns the tokens held in the legacy combined entry. A
// nil map with a nil error means the entry genuinely does not exist (readLegacy
// returned ok=false, err=nil) — the one case callers may treat as "no tokens"
// and proceed. Any other failure (a transient keyring read error, undecodable
// base64, invalid JSON) is returned as err and must NOT be collapsed into "no
// tokens": write() merges legacy-only keys into the indexed representation,
// and mistaking a transient read failure for an empty blob would skip
// credentials that still live only in that account.
func (b keyringBlob) readLegacyTokens() (map[string]Token, error) {
	data, ok, err := b.readLegacy()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var legacyState storeFile
	if err := json.Unmarshal(data, &legacyState); err != nil {
		return nil, fmt.Errorf("oauth: invalid legacy keyring token blob: %w", err)
	}
	return legacyState.Tokens, nil
}

// write replaces the keyring's token entries with state, ordered so that
// every interruption boundary leaves a recoverable store. The invariant is
// that any token entry existing in the keyring at any instant is listed in
// the published index: the union index is published before entries are
// written, entries are deleted before the index shrinks, and the index
// header is only updated after the chunks it references exist. A crash at
// any step therefore leaves either an index over-listing keys whose entries
// are missing (read() recovers those from the legacy blob unless tombstoned,
// or skips them; the next write prunes phantom index keys so they cannot
// permanently consume capacity) or entries that a later read/write can still
// see and reconcile, never an invisible credential stranded in the OS keychain.
//
// The legacy combined entry is never written or deleted here; it stays frozen
// as a discovery source for unindexed old-binary logins. Logout is authoritative
// for new binaries only: a tombstone durably suppresses the legacy copy on every
// new-binary read, but a pre-per-key binary reading `oauth-tokens` directly can
// still use the credential until it is upgraded. Scrubbing the key out of the
// legacy blob would require a snapshot-then-Set that cannot be locked against
// old writers on other config roots, so it would trade a bounded downgrade
// window for unbounded loss of concurrent old-binary logins.
func (b keyringBlob) write(data []byte, mutations map[string]bool, checkLease leaseCheck) error {
	var state storeFile
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("oauth: encode keyring token blob: %w", err)
	}
	priorKeys, indexExisted, priorChunks, priorGeneration, missingChunks, err := b.readKeyIndex()
	if err != nil {
		return err
	}
	indexIncomplete := len(missingChunks) > 0
	prior := make(map[string]bool, len(priorKeys))
	for _, key := range priorKeys {
		prior[key] = true
	}

	tombstones, err := b.readTombstones()
	if err != nil {
		return err
	}
	// Record durable deletion markers before mutating entries so a crash
	// mid-write cannot leave a logged-out key importable from legacy alone.
	for key, deleted := range mutations {
		if deleted {
			tombstones[key] = true
		}
	}

	// An older binary running alongside this one still reads and writes only the
	// legacy combined entry. Merge keys that entry holds which the indexed
	// schema has never seen (fresh old-binary logins), unless tombstoned or
	// omitted by this operation. Never overwrite a key already present in
	// state: expiry and token strings are not causal order. Keys in the prior
	// index but absent from this write were deliberately removed (logout) and
	// must not be resurrected.
	//
	// Unlike read()'s best-effort fallback, a failure here must abort the whole
	// write rather than proceed as though the legacy blob were empty: skipping
	// a still-live legacy-only credential would leave it unindexed until the
	// next successful reconcile, and a concurrent old-writer update is only
	// discoverable through this read.
	legacyTokens, err := b.readLegacyTokens()
	if err != nil {
		return fmt.Errorf("oauth: read legacy keyring token blob for reconciliation: %w", err)
	}
	if indexExisted {
		for key, legacyToken := range legacyTokens {
			if ValidateKey(key) != nil {
				continue
			}
			if mutations[key] || tombstones[key] {
				continue
			}
			if _, exists := state.Tokens[key]; exists {
				continue
			}
			if prior[key] {
				continue
			}
			state.Tokens[key] = legacyToken
		}
	}

	// A running pre-migration binary only ever rewrites the legacy combined
	// entry: it has no concept of the index or tombstones, so its own logout
	// is indistinguishable, in isolation, from a key this process simply never
	// indexed. legacyOrigin remembers which keys were ever actually observed in
	// that entry, so a later absence can be read as "removed" rather than
	// "never was there" — the distinction the merge above cannot make on its
	// own, since a key created purely through this binary's Save also never
	// appears in legacyTokens.
	legacyOrigin, err := b.readLegacyOrigin()
	if err != nil {
		return err
	}
	legacyOriginChanged := false
	for key := range legacyTokens {
		if ValidateKey(key) != nil {
			continue
		}
		if !legacyOrigin[key] {
			if _, mutated := mutations[key]; !mutated {
				legacyOrigin[key] = true
				legacyOriginChanged = true
			}
		}
	}
	for key := range mutations {
		if legacyOrigin[key] {
			delete(legacyOrigin, key)
			legacyOriginChanged = true
		}
	}
	if len(legacyOrigin) > 0 {
		for key := range state.Tokens {
			if _, mutated := mutations[key]; mutated || !legacyOrigin[key] {
				continue
			}
			if legacyTokens != nil {
				if _, stillInLegacy := legacyTokens[key]; stillInLegacy {
					continue
				}
			}
			delete(state.Tokens, key)
			if !tombstones[key] {
				tombstones[key] = true
			}
			delete(legacyOrigin, key)
			legacyOriginChanged = true
		}
	}

	keys := make([]string, 0, len(state.Tokens))
	for key := range state.Tokens {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Preflight: marshal and size-check every desired token BEFORE publishing
	// any index key. Publishing first left rejected oversized Saves as permanent
	// index phantoms that could exhaust maxKeyringIndexKeys and brick the store.
	encoded := make(map[string]string, len(keys))
	for _, key := range keys {
		raw, err := json.Marshal(state.Tokens[key])
		if err != nil {
			return err
		}
		enc := base64.StdEncoding.EncodeToString(raw)
		if len(enc) > maxKeyringSingleEntryBytes {
			return fmt.Errorf("oauth: token payload for %q (%d bytes) exceeds single keyring entry bound (%d bytes); use file or encrypted-file storage", key, len(enc), maxKeyringSingleEntryBytes)
		}
		encoded[key] = enc
	}

	// Drop prior index keys that have neither a live entry nor a place in the
	// desired set (phantoms from an interrupted Set after a previous union
	// publish). Including them in the next union would permanently consume
	// index capacity after enough failed writes.
	livePrior := make([]string, 0, len(priorKeys))
	for _, key := range priorKeys {
		if _, ok := state.Tokens[key]; ok {
			livePrior = append(livePrior, key)
			continue
		}
		_, exists, err := b.kr.Get(b.service, key)
		if err != nil {
			return err
		}
		if exists {
			livePrior = append(livePrior, key)
		}
	}

	// Each numbered step below is gated on checkLease immediately before it
	// mutates the keyring: this write can span several round-trips (a chunked
	// index plus one Set per token), and a process that stalls partway through
	// after another process reclaimed its lock as stale must not let the rest
	// of the sequence land. Checking only once at entry would let every step
	// after the stall complete, racing the reclaiming process's own
	// read-modify-write instead of aborting before touching shared state.

	// 1. Persist tombstones before removing entries so logout survives a crash
	// between entry delete and a later reconcile (and survives an old binary
	// rewriting the legacy blob with the deleted key still present).
	if err := checkLease(); err != nil {
		return err
	}
	if err := b.writeTombstones(tombstones, checkLease); err != nil {
		return err
	}
	// Persist legacyOrigin membership changes alongside the tombstones they
	// justify: a key observed here for the first time, or one just tombstoned
	// for having disappeared from legacy, must survive a crash the same way
	// the tombstone itself does, or a retried write would repeat the same
	// detection from a colder cache and could reorder relative to a concurrent
	// old-binary rewrite.
	if legacyOriginChanged {
		if err := checkLease(); err != nil {
			return err
		}
		if err := b.writeLegacyOrigin(legacyOrigin, checkLease); err != nil {
			return err
		}
	}
	// 2. Publish the union of the live prior and new key sets first, so every
	// entry that exists at any point during this update is indexed.
	//
	// When a referenced continuation chunk was missing, livePrior is truncated
	// and cannot name the unlisted keys. Keep advertising the prior chunk count
	// (and never delete those chunk accounts) so a later-restored chunk can
	// still be reconciled; a complete rewrite to only the known keys would
	// permanently orphan their OS keychain entries.
	union := keys
	if len(livePrior) > 0 {
		merged := make(map[string]bool, len(keys)+len(livePrior))
		for _, key := range append(append([]string{}, keys...), livePrior...) {
			merged[key] = true
		}
		union = make([]string, 0, len(merged))
		for key := range merged {
			union = append(union, key)
		}
		sort.Strings(union)
	}
	if err := checkLease(); err != nil {
		return err
	}
	unionChunks, unionGeneration, err := b.writeKeyIndex(union, priorChunks, priorGeneration, missingChunks, checkLease)
	if err != nil {
		return err
	}
	// 3. Write each token entry (encodings preflighted above).
	for _, key := range keys {
		if err := checkLease(); err != nil {
			return err
		}
		if err := b.kr.Set(b.service, key, encoded[key]); err != nil {
			return err
		}
	}
	// 4. Delete removed entries while the union index still lists them, so a
	// failed Delete leaves a visible (re-deletable) entry, never an orphan.
	// Only walk livePrior (keys we could see): entries named only in a missing
	// chunk stay put so a restored chunk can still find them.
	for _, key := range livePrior {
		if _, ok := state.Tokens[key]; !ok {
			if err := checkLease(); err != nil {
				return err
			}
			if _, err := b.kr.Delete(b.service, key); err != nil {
				return err
			}
		}
	}
	// 5. Shrink the index to the exact new key set. Legacy is left untouched.
	// Skip shrink when the prior index was incomplete: a shrink to `keys` would
	// drop the preserved chunk advertisements and strand unlisted entries.
	if !indexIncomplete {
		if err := checkLease(); err != nil {
			return err
		}
		if _, _, err := b.writeKeyIndex(keys, unionChunks, unionGeneration, nil, checkLease); err != nil {
			return err
		}
	}
	// A re-login clears its tombstone only after the replacement entry and exact
	// index are durable. If any earlier step fails, legacy fallback remains
	// suppressed instead of restoring the revoked credential.
	tombstonesChanged := false
	for key, deleted := range mutations {
		if !deleted && tombstones[key] {
			delete(tombstones, key)
			tombstonesChanged = true
		}
	}
	if tombstonesChanged {
		if err := checkLease(); err != nil {
			return err
		}
		if err := b.writeTombstones(tombstones, checkLease); err != nil {
			return err
		}
	}
	return nil
}

// tombstoneBlob returns a keyringBlob that reuses the chunked index codec for
// the durable deletion set. Tombstones can grow to the same key/chunk caps as
// the live index (max-length keys after many logouts), so a single entry is
// not enough under the macOS line bound.
func (b keyringBlob) tombstoneBlob() keyringBlob {
	return keyringBlob{kr: b.kr, service: b.service, indexAccount: keyringTombstoneAccount, maxIndexKeys: maxKeyringTombstoneKeys}
}

func (b keyringBlob) readMarkerSet(mb keyringBlob, label string) (map[string]bool, error) {
	keys, ok, _, _, _, err := mb.readKeyIndex()
	if err != nil {
		return nil, fmt.Errorf("oauth: read keyring %s: %w", label, err)
	}
	if !ok {
		return map[string]bool{}, nil
	}
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out, nil
}

func (b keyringBlob) writeMarkerSet(mb keyringBlob, set map[string]bool, label string, limit int, overflowMsg string, checkLease leaseCheck) error {
	_, existed, priorChunks, priorGeneration, missingChunks, err := mb.readKeyIndex()
	if err != nil {
		return fmt.Errorf("oauth: read keyring %s: %w", label, err)
	}
	if len(set) == 0 {
		if !existed {
			return nil
		}
		if len(missingChunks) > 0 {
			if _, _, err := mb.writeKeyIndex(nil, priorChunks, priorGeneration, missingChunks, checkLease); err != nil {
				return fmt.Errorf("oauth: write keyring %s: %w", label, err)
			}
			return nil
		}
		if err := checkLease(); err != nil {
			return err
		}
		if _, err := mb.kr.Delete(mb.service, mb.indexAccount); err != nil {
			return err
		}
		for i := 1; i < priorChunks; i++ {
			if err := checkLease(); err != nil {
				return err
			}
			if _, err := mb.kr.Delete(mb.service, mb.chunkAccount(priorGeneration, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if len(set) > limit {
		if overflowMsg != "" {
			return fmt.Errorf("oauth: keyring %s list %d %s", label, len(set), overflowMsg)
		}
		return fmt.Errorf("oauth: keyring %s list %d keys, over the %d-key writable bound", label, len(set), limit)
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		if ValidateKey(key) != nil {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if _, _, err := mb.writeKeyIndex(keys, priorChunks, priorGeneration, missingChunks, checkLease); err != nil {
		return fmt.Errorf("oauth: write keyring %s: %w", label, err)
	}
	return nil
}

// readTombstones returns the durable set of keys deleted by a new binary.
// Missing account => empty set. Corrupt payloads fail closed.
func (b keyringBlob) readTombstones() (map[string]bool, error) {
	return b.readMarkerSet(b.tombstoneBlob(), "token tombstones")
}

// writeTombstones persists the durable deletion set. An empty set removes every
// tombstone account/chunk so a fully clean store does not leave leftover
// markers. Errors from that cleanup are surfaced so interruption tests and
// real keyring failures cannot be swallowed.
func (b keyringBlob) writeTombstones(tombstones map[string]bool, checkLease leaseCheck) error {
	overflow := fmt.Sprintf("logged-out keys, over the %d-key writable bound; re-login to a retired provider to free a marker slot", maxKeyringTombstoneKeys)
	return b.writeMarkerSet(b.tombstoneBlob(), tombstones, "token tombstones", maxKeyringTombstoneKeys, overflow, checkLease)
}

func (b keyringBlob) legacyOriginBlob() keyringBlob {
	return keyringBlob{kr: b.kr, service: b.service, indexAccount: keyringLegacyOriginAccount, maxIndexKeys: maxKeyringTombstoneKeys}
}

// readLegacyOrigin returns the durable set of keys ever observed in the
// legacy combined entry. Missing account => empty set (no old binary has
// ever touched this store). Corrupt payloads fail closed, the same as
// readTombstones: silently treating a damaged marker set as empty would
// un-track every key it recorded and let the very absences it exists to
// interpret look like fresh, ordinary keys again.
func (b keyringBlob) readLegacyOrigin() (map[string]bool, error) {
	return b.readMarkerSet(b.legacyOriginBlob(), "legacy-origin markers")
}

// writeLegacyOrigin persists the legacy-origin set, mirroring writeTombstones:
// an empty set removes the account/chunks entirely rather than leaving a
// zero-key index behind.
func (b keyringBlob) writeLegacyOrigin(origin map[string]bool, checkLease leaseCheck) error {
	return b.writeMarkerSet(b.legacyOriginBlob(), origin, "legacy-origin markers", maxKeyringTombstoneKeys, "", checkLease)
}

// maxKeyringSingleEntryBytes bounds a single base64-encoded token secret so
// that the line passed to macOS `security -i` stays comfortably under the
// 4095-byte command line cap (see internal/keyring).
const maxKeyringSingleEntryBytes = 3800

// maxKeyringIndexChunkBytes bounds one index chunk's raw JSON payload so its
// base64 encoding plus command framing stays well under the macOS
// `security -i` 4095-byte line cap (see internal/keyring): 2700 raw bytes
// expand to 3600 base64 bytes, leaving ~490 bytes for the add-generic-password
// syntax, service, and account. The old single-entry index hit that cap at
// roughly 22 maximum-length keys even when every token was tiny.
const maxKeyringIndexChunkBytes = 2700

// maxKeyringIndexEncodedBytes bounds one index header/chunk's base64 string
// before DecodeString or json.Unmarshal. Writers never emit more than
// maxKeyringIndexChunkBytes of raw JSON per chunk (header wraps one chunk of
// keys plus a few metadata fields), so anything larger is damaged or hostile
// and must be rejected without allocating unbounded decode buffers on the
// hot path that holds the store lock.
const maxKeyringIndexEncodedBytes = 4096

// maxKeyringIndexChunks caps how many chunk entries a stored index header may
// claim before readKeyIndex issues one OS-keyring lookup per chunk. Each chunk
// holds up to maxKeyringIndexChunkBytes of keys (dozens to ~150 keys), so this
// bound admits far more logins than any real install while refusing to fan a
// corrupt header (e.g. {"v":1,"chunks":1000000000}) out into a billion blocking
// lookups that would wedge every OAuth operation under the store lock.
const maxKeyringIndexChunks = 128

// maxKeyringIndexKeys bounds how many keys readKeyIndex will ever return, across
// the header and every chunk (and the legacy bare-array format), before read()
// and write() fan them out into one kr.Get per key while holding the store
// lock. maxKeyringIndexChunks only bounds the number of chunk entries fetched;
// it does not bound how many keys a single chunk's JSON can claim, so a
// corrupted index with an oversized keys array (or many chunks each stuffed
// with keys) could still drive an unbounded number of blocking lookups. The
// bound here is generous relative to what chunkIndexKeys ever legitimately
// produces (short namespaced keys cost at least ~18 bytes each, so one
// maxKeyringIndexChunkBytes chunk holds on the order of a hundred, times
// maxKeyringIndexChunks) while still rejecting a damaged index promptly.
const maxKeyringIndexKeys = 512

// maxKeyringKeyBytes is the longest ValidateKey-shaped key: "provider:" (or
// "mcp:") plus a leading alphanumeric plus up to 127 safe characters.
const maxKeyringKeyBytes = 137

// maxKeyringTombstoneKeys bounds the durable logout-marker set to what the
// chunked index writer can ALWAYS serialize, even with maximum-length keys:
// each key costs len+8 = 145 bytes of a 2700-byte chunk, so one chunk holds 18
// and the 128-chunk reader cap holds 2304. The previous bound of 16,384 was
// only reachable with tiny keys; a tombstone set using long provider keys
// exhausted the chunk budget first, and once writeKeyIndex rejected it every
// later Save/Delete failed (including the re-login that could otherwise clear
// the marker), permanently disabling keyring-backed persistence. With the
// writable bound, the logout that would overflow the set fails with a clear
// error instead of bricking the store, and re-login retires a marker to free
// a slot.
const maxKeyringTombstoneKeys = maxKeyringIndexChunks * (maxKeyringIndexChunkBytes / (maxKeyringKeyBytes + 8))

// maxRawKeyringIndexKeys bounds the raw decoded element count before
// deduplication or map preallocation, guarding against DoS from duplicate keys.
const maxRawKeyringIndexKeys = 16384

// errKeyringIndexTooManyKeys is returned when a decoded index (or one of its
// chunks) claims more keys than maxKeyringIndexKeys.
func errKeyringIndexTooManyKeys(count, limit int) error {
	return fmt.Errorf("oauth: keyring token index lists %d keys, over the %d-key cap", count, limit)
}

// keyIndexHeader is chunk 0 of the key index. Chunks 1..Chunks-1 live under
// "<indexAccount>-<n>" as plain JSON string arrays. The pre-chunking format
// (a bare JSON array at indexAccount) is still read transparently.
type keyIndexHeader struct {
	Version int      `json:"v"`
	Chunks  int      `json:"chunks"`
	Keys    []string `json:"keys"`
	// Generation selects which physical chunk-account namespace Chunks refers
	// to; see chunkAccount. Omitted (0) both for an index that predates this
	// field and for generation 0 itself, which are the same namespace by
	// construction: chunkAccount(0, i) reproduces the original, ungenerationed
	// naming, so an old header decodes exactly as it always did.
	Generation int `json:"gen,omitempty"`
}

func (b keyringBlob) indexKeyLimit() int {
	if b.maxIndexKeys > 0 {
		return b.maxIndexKeys
	}
	return maxKeyringIndexKeys
}

// chunkAccount names the continuation-chunk account for index within
// generation. generation 0 reproduces the original naming an ungenerationed
// index already used, so every index written before generations existed
// keeps working with no migration step. A later generation gets its own
// disjoint namespace, so a writer publishing generation N+1 can stage every
// chunk it needs under names generation N's reader (or a concurrent one still
// using the old header) will never look at, and only touch generation N's
// accounts afterward, as cleanup.
func (b keyringBlob) chunkAccount(generation, index int) string {
	if generation <= 0 {
		return fmt.Sprintf("%s-%d", b.indexAccount, index)
	}
	return fmt.Sprintf("%s-g%d-%d", b.indexAccount, generation, index)
}

// decodeKeyringIndexPayload bounds and decodes one index header/chunk value
// before json.Unmarshal. The element-count cap alone does not bound the size of
// a single JSON string inside a damaged payload.
func decodeKeyringIndexPayload(enc string, what string) ([]byte, error) {
	enc = strings.TrimSpace(enc)
	if len(enc) > maxKeyringIndexEncodedBytes {
		return nil, fmt.Errorf("oauth: %s is %d bytes encoded, over the %d-byte bound", what, len(enc), maxKeyringIndexEncodedBytes)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("oauth: decode %s: %w", what, err)
	}
	// Header wraps one chunk of keys plus a few metadata fields; reject anything
	// well beyond the writer-side raw chunk budget before Unmarshal.
	if len(raw) > maxKeyringIndexChunkBytes+256 {
		return nil, fmt.Errorf("oauth: %s decodes to %d bytes, over the %d-byte raw bound", what, len(raw), maxKeyringIndexChunkBytes+256)
	}
	return raw, nil
}

// readKeyIndex returns the indexed keys, whether an index exists at all,
// how many chunk entries it currently occupies, and the indexes of any
// advertised continuation chunks that are missing. A missing chunk (external
// keychain damage or a torn write outside this code's write order) is skipped
// so reads stay available, but its index is remembered so write() can refuse
// to shrink the index (stranding the unlisted entries as undeletable orphans)
// and can refuse to overwrite that account with unrelated new chunk data.
func (b keyringBlob) readKeyIndex() (keys []string, ok bool, chunks int, generation int, missing []int, err error) {
	enc, ok, err := b.kr.Get(b.service, b.indexAccount)
	if err != nil {
		return nil, false, 0, 0, nil, err
	}
	if !ok {
		return nil, false, 0, 0, nil, nil
	}
	raw, err := decodeKeyringIndexPayload(enc, "keyring token index")
	if err != nil {
		return nil, false, 0, 0, nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var rawKeys []string
		if err := json.Unmarshal(raw, &rawKeys); err != nil {
			return nil, false, 0, 0, nil, fmt.Errorf("oauth: decode keyring token index: %w", err)
		}
		if len(rawKeys) > maxRawKeyringIndexKeys {
			return nil, false, 0, 0, nil, errKeyringIndexTooManyKeys(len(rawKeys), maxRawKeyringIndexKeys)
		}
		keys := dedupeValidKeys(rawKeys)
		if len(keys) > b.indexKeyLimit() {
			return nil, false, 0, 0, nil, errKeyringIndexTooManyKeys(len(keys), b.indexKeyLimit())
		}
		return keys, true, 1, 0, nil, nil
	}
	var header keyIndexHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, false, 0, 0, nil, fmt.Errorf("oauth: decode keyring token index: %w", err)
	}
	// Reject an unsupported or corrupt header before looping: an out-of-range
	// Chunks would otherwise drive up to that many blocking keyring lookups
	// (each up to the 10s command timeout) while the store lock is held, wedging
	// every Load/Status/Save/Delete instead of failing promptly.
	if header.Version != 1 {
		return nil, false, 0, 0, nil, fmt.Errorf("oauth: unsupported keyring token index version %d", header.Version)
	}
	if header.Chunks < 1 || header.Chunks > maxKeyringIndexChunks {
		return nil, false, 0, 0, nil, fmt.Errorf("oauth: keyring token index advertises %d chunks (want 1..%d)", header.Chunks, maxKeyringIndexChunks)
	}
	if header.Generation < 0 || header.Generation >= math.MaxInt {
		return nil, false, 0, 0, nil, fmt.Errorf("oauth: keyring token index advertises invalid generation %d", header.Generation)
	}
	rawKeys := header.Keys
	if len(rawKeys) > maxRawKeyringIndexKeys {
		return nil, false, 0, 0, nil, errKeyringIndexTooManyKeys(len(rawKeys), maxRawKeyringIndexKeys)
	}
	for i := 1; i < header.Chunks; i++ {
		chunkEnc, chunkOK, err := b.kr.Get(b.service, b.chunkAccount(header.Generation, i))
		if err != nil {
			return nil, false, 0, 0, nil, err
		}
		if !chunkOK {
			// Skip so Load/Status stay available, but remember which account is
			// damaged so write() neither shrinks away the unlisted keys' entries
			// nor overwrites this account with unrelated new chunk data.
			missing = append(missing, i)
			continue
		}
		chunkRaw, err := decodeKeyringIndexPayload(chunkEnc, fmt.Sprintf("keyring token index chunk %d", i))
		if err != nil {
			return nil, false, 0, 0, nil, err
		}
		var more []string
		if err := json.Unmarshal(chunkRaw, &more); err != nil {
			return nil, false, 0, 0, nil, fmt.Errorf("oauth: decode keyring token index chunk %d: %w", i, err)
		}
		if len(rawKeys)+len(more) > maxRawKeyringIndexKeys {
			return nil, false, 0, 0, nil, errKeyringIndexTooManyKeys(len(rawKeys)+len(more), maxRawKeyringIndexKeys)
		}
		rawKeys = append(rawKeys, more...)
	}
	keys = dedupeValidKeys(rawKeys)
	if len(keys) > b.indexKeyLimit() {
		return nil, false, 0, 0, nil, errKeyringIndexTooManyKeys(len(keys), b.indexKeyLimit())
	}
	return keys, true, header.Chunks, header.Generation, missing, nil
}

// dedupeValidKeys drops duplicates and malformed entries from a decoded
// index's key list before it is fanned out into one keyring lookup per key by
// read()/write() (via Load/Status/Save/Delete). maxKeyringIndexKeys already
// bounds the raw decode, but that bound does nothing against a corrupted or
// adversarially crafted index that packs its budget with repeats of the same
// key (or garbage that was never a real ValidateKey-shaped entry): every
// duplicate or malformed key would otherwise still cost its own blocking
// keyring lookup (up to the 10s command timeout) while the store lock is
// held, reintroducing the fan-out DoS the index cap was meant to close.
// Order is preserved (first occurrence wins) so callers that sort or display
// keys see stable results.
func dedupeValidKeys(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if seen[key] {
			continue
		}
		if ValidateKey(key) != nil {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

// writeKeyIndex persists keys as a chunked index and reports how many chunk
// entries the published header advertises, and under which generation (see
// chunkAccount) it wrote them.
//
// missingChunks lists continuation-chunk accounts readKeyIndex could not read
// from priorGeneration: their keys are unknown, so advancing past them would
// silently orphan whatever credentials they listed, with no record of what
// they were left to recover from. When missingChunks is empty, every key this
// index has ever listed is accounted for in keys, so it is safe to stage the
// complete new layout under a fresh generation, publish a header that adopts
// it atomically, and only then clean up priorGeneration's now-unreferenced
// chunk accounts: nothing this function does before the header Set can ever
// leave a half-written layout reachable, because nothing reads a generation
// the header does not name. When missingChunks is non-empty, this instead
// falls back to the narrower in-place update the fresh-generation path exists
// to avoid elsewhere: new continuation chunks are remapped around the
// protected (missing) slots within priorGeneration itself, the header keeps
// advertising at least priorChunks so a later-restored chunk stays reachable,
// and protected accounts are never deleted. That path accepts the same
// in-place-overwrite exposure this function's own doc used to describe
// unconditionally; it is now scoped to only the case where advancing the
// generation is not safe to begin with.
func (b keyringBlob) writeKeyIndex(keys []string, priorChunks, priorGeneration int, missingChunks []int, checkLease leaseCheck) (chunks int, generation int, err error) {
	if priorGeneration < 0 || priorGeneration >= math.MaxInt {
		return 0, 0, fmt.Errorf("oauth: keyring key index generation overflow: %d", priorGeneration)
	}
	// Refuse to publish an index the reader would reject: readKeyIndex caps both
	// total keys and chunk count, and a header beyond either would make every
	// later Load/Status/Save/Delete fail before it could recover. Check the key
	// count before chunking so a large set of short keys that still fit under
	// maxKeyringIndexChunks cannot strand the store unreadable.
	if len(keys) > b.indexKeyLimit() {
		return 0, 0, errKeyringIndexTooManyKeys(len(keys), b.indexKeyLimit())
	}
	chunkList := chunkIndexKeys(keys)
	if len(chunkList) > maxKeyringIndexChunks {
		return 0, 0, fmt.Errorf("oauth: keyring key index needs %d chunks, over the %d-chunk cap readers accept; too many stored credentials", len(chunkList), maxKeyringIndexChunks)
	}
	if len(missingChunks) == 0 {
		return b.writeKeyIndexNewGeneration(chunkList, priorChunks, priorGeneration, checkLease)
	}
	return b.writeKeyIndexInPlace(chunkList, priorChunks, priorGeneration, missingChunks, checkLease)
}

func (b keyringBlob) discardStagedChunks(generation, count int) {
	for i := 1; i < count; i++ {
		_, _ = b.kr.Delete(b.service, b.chunkAccount(generation, i))
	}
}

// writeKeyIndexNewGeneration is writeKeyIndex's normal-case path: prior state
// is fully known (missingChunks is empty), so it is safe to stage the entire
// new layout under a never-before-used generation and adopt it with a single
// header write. Every chunk write below therefore targets an account nothing
// currently reads; the header Set is the only step any concurrent or crashed
// reader can observe as a change at all.
func (b keyringBlob) writeKeyIndexNewGeneration(chunkList [][]string, priorChunks, priorGeneration int, checkLease leaseCheck) (chunks int, generation int, err error) {
	if priorGeneration < 0 || priorGeneration >= math.MaxInt {
		return 0, 0, fmt.Errorf("oauth: keyring key index generation overflow: %d", priorGeneration)
	}
	newGeneration := priorGeneration + 1
	advertised := len(chunkList)
	if advertised > maxKeyringIndexChunks {
		return 0, 0, fmt.Errorf("oauth: keyring key index needs %d chunks, over the %d-chunk cap readers accept; too many stored credentials", advertised, maxKeyringIndexChunks)
	}
	staged := 0
	for i := 1; i < len(chunkList); i++ {
		chunkData, err := json.Marshal(chunkList[i])
		if err != nil {
			b.discardStagedChunks(newGeneration, staged+1)
			return 0, 0, err
		}
		if err := checkLease(); err != nil {
			b.discardStagedChunks(newGeneration, staged+1)
			return 0, 0, err
		}
		if err := b.kr.Set(b.service, b.chunkAccount(newGeneration, i), base64.StdEncoding.EncodeToString(chunkData)); err != nil {
			b.discardStagedChunks(newGeneration, staged+1)
			return 0, 0, err
		}
		staged = i
	}
	headerData, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: advertised, Generation: newGeneration, Keys: chunkList[0]})
	if err != nil {
		b.discardStagedChunks(newGeneration, len(chunkList))
		return 0, 0, err
	}
	if err := checkLease(); err != nil {
		b.discardStagedChunks(newGeneration, len(chunkList))
		return 0, 0, err
	}
	if priorGeneration > 0 {
		_, ok, _, curGen, _, err := b.readKeyIndex()
		if err == nil && ok && curGen != priorGeneration {
			b.discardStagedChunks(newGeneration, len(chunkList))
			return 0, 0, fmt.Errorf("oauth: keyring key index generation conflict: expected %d, found %d", priorGeneration, curGen)
		}
	}
	if err := b.kr.Set(b.service, b.indexAccount, base64.StdEncoding.EncodeToString(headerData)); err != nil {
		b.discardStagedChunks(newGeneration, len(chunkList))
		return 0, 0, err
	}
	// The new generation is durable and exclusively authoritative from this
	// point on. Reclaiming the old one is pure cleanup: best-effort, and safe
	// to abandon partway, since nothing will ever read priorGeneration again
	// regardless of how much of it gets deleted here.
	if priorGeneration != newGeneration {
		for i := 1; i < priorChunks; i++ {
			if checkLease() != nil {
				break
			}
			_, _ = b.kr.Delete(b.service, b.chunkAccount(priorGeneration, i))
		}
	}
	return advertised, newGeneration, nil
}

// writeKeyIndexInPlace is writeKeyIndex's degraded-case fallback, used only
// when missingChunks is non-empty: some prior chunk's keys are unknown, so
// staging a fresh generation would publish a layout that silently drops them
// with no remaining record of what they were. It stays within priorGeneration
// and mirrors the original single-namespace protocol exactly: new
// continuation chunks are remapped around protected (missing) slots, and
// capacity is still planned before any chunk is written (a rejected write
// here still leaves every existing chunk account untouched), but a slot that
// is not protected is still overwritten in place, since there is no
// generation boundary available to defer that behind here.
func (b keyringBlob) writeKeyIndexInPlace(chunkList [][]string, priorChunks, priorGeneration int, missingChunks []int, checkLease leaseCheck) (chunks int, generation int, err error) {
	if priorGeneration < 0 || priorGeneration >= math.MaxInt {
		return 0, 0, fmt.Errorf("oauth: keyring key index generation overflow: %d", priorGeneration)
	}
	protected := make(map[int]bool, len(missingChunks))
	maxProtected := 0
	for _, c := range missingChunks {
		protected[c] = true
		if c > maxProtected {
			maxProtected = c
		}
	}
	type plannedChunk struct {
		slot  int
		index int
	}
	var planned []plannedChunk
	slot := 1
	advertised := len(chunkList)
	for i := 1; i < len(chunkList); i++ {
		for protected[slot] {
			slot++
		}
		planned = append(planned, plannedChunk{slot: slot, index: i})
		if slot+1 > advertised {
			advertised = slot + 1
		}
		slot++
	}
	// Keep advertising protected slots (a restored chunk is read by walking
	// every account below the advertised count) and the prior chunk count.
	if maxProtected+1 > advertised {
		advertised = maxProtected + 1
	}
	if priorChunks > advertised {
		advertised = priorChunks
	}
	if advertised > maxKeyringIndexChunks {
		return 0, 0, fmt.Errorf("oauth: keyring key index needs %d chunks, over the %d-chunk cap readers accept; too many stored credentials", advertised, maxKeyringIndexChunks)
	}
	for _, p := range planned {
		chunkData, err := json.Marshal(chunkList[p.index])
		if err != nil {
			return 0, 0, err
		}
		if err := checkLease(); err != nil {
			return 0, 0, err
		}
		if err := b.kr.Set(b.service, b.chunkAccount(priorGeneration, p.slot), base64.StdEncoding.EncodeToString(chunkData)); err != nil {
			return 0, 0, err
		}
	}
	plannedSlots := make(map[int]bool, len(planned))
	for _, p := range planned {
		plannedSlots[p.slot] = true
	}
	for s := 1; s < advertised; s++ {
		if protected[s] || plannedSlots[s] {
			continue
		}
		emptyData, err := json.Marshal([]string{})
		if err != nil {
			return 0, 0, err
		}
		if err := checkLease(); err != nil {
			return 0, 0, err
		}
		if err := b.kr.Set(b.service, b.chunkAccount(priorGeneration, s), base64.StdEncoding.EncodeToString(emptyData)); err != nil {
			return 0, 0, err
		}
	}
	headerData, err := json.Marshal(keyIndexHeader{Version: 1, Chunks: advertised, Generation: priorGeneration, Keys: chunkList[0]})
	if err != nil {
		return 0, 0, err
	}
	if err := checkLease(); err != nil {
		return 0, 0, err
	}
	if priorGeneration > 0 {
		_, ok, _, curGen, _, err := b.readKeyIndex()
		if err == nil && ok && curGen != priorGeneration {
			return 0, 0, fmt.Errorf("oauth: keyring key index generation conflict: expected %d, found %d", priorGeneration, curGen)
		}
	}
	if err := b.kr.Set(b.service, b.indexAccount, base64.StdEncoding.EncodeToString(headerData)); err != nil {
		return 0, 0, err
	}
	// No shrink-cleanup here: protected slots are always present in this path
	// (that is why it was taken), and deleting a protected account would
	// destroy the last chance to recover the keys it listed.
	return advertised, priorGeneration, nil
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

// chtimesLockFile stamps a lock file's mtime for lease refresh. A var so tests
// can inject renewal failures (fencing regression): a failed Chtimes leaves
// the lock token intact but stops its mtime advancing, so a peer can reclaim
// it as stale and start a conflicting critical section.
var chtimesLockFile = os.Chtimes

// leasedPath is one acquired lock whose mtime is refreshed until stop is
// closed. Lease ownership starts at acquisition, not after every path is
// held: withLock acquires lockPath then may block on legacyLockPath, and a
// peer must not be able to reclaim the first lock as stale during that wait.
// Refresh is ownership-aware: if a peer reclaims and replaces the lock while
// this holder is paused, Chtimes is skipped and lost is set so the critical
// section can fail closed instead of keeping the thief's lock forever-fresh.
type leasedPath struct {
	path   string
	token  string
	unlock func() error
	stop   chan struct{}
	done   chan struct{}
	lost   atomic.Bool
}

func startLease(path, token string, unlock func() error) *leasedPath {
	l := &leasedPath{
		path:   path,
		token:  token,
		unlock: unlock,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(fileLockRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-l.stop:
				return
			case <-ticker.C:
				// Lease with wall-clock time, never the injectable now: acquireFileLock
				// judges staleness with real time.Since(mtime), so a fixed or stale
				// StoreOptions.Now would stamp a live lock with an old mtime that
				// another process would immediately reclaim, reviving the token-loss
				// race these locks prevent. Only refresh while we still own the
				// token: a post-stale reclaim can replace the file, and Chtimes on
				// the replacement would keep both holders inside the critical section.
				// Re-check ownership after Chtimes as well: between the pre-check and
				// the stamp a peer can swap the file, and a successful Chtimes on the
				// thief's lock would keep both critical sections alive.
				if !ownLockFile(path, token) {
					l.lost.Store(true)
					return
				}
				at := time.Now()
				if err := chtimesLockFile(path, at, at); err != nil {
					// A failed renewal stops advancing the lock's mtime, so the
					// lock will look stale and a peer can reclaim it
					// mid-critical-section. Treat it as an immediate fencing
					// failure: stop refreshing so withLeasedLocks aborts before
					// (or surfaces the error after) the keyring mutation.
					l.lost.Store(true)
					return
				}
				if !ownLockFile(path, token) {
					l.lost.Store(true)
					return
				}
			}
		}
	}()
	return l
}

func (l *leasedPath) release() error {
	close(l.stop)
	<-l.done
	if l.unlock != nil {
		return l.unlock()
	}
	return nil
}

// withLeasedLocks acquires every non-empty path in order. Each lock's mtime
// lease starts immediately on acquisition (and keeps refreshing while later
// paths are still being acquired and while fn runs), so a multi-lock wait
// cannot leave an earlier lock looking abandoned. Locks are released in
// reverse order once fn returns — including when fn panics — so a recovered
// panic cannot leave a forever-refreshed lock that wedges every later waiter.
// If a lease is replaced under us mid-critical-section, the operation fails
// closed after fn returns (or with fn's error) rather than treating a dual-
// entry window as success.
// leaseCheck reports whether every lock backing the current critical section
// is still owned, at the instant it is called. fn must call it immediately
// before each externally visible mutation (a keyring Set/Delete, not a read),
// not only rely on the pre/post checks around fn as a whole: a process can
// pass the pre-check, stall past the stale interval, and have another process
// reclaim the lock while it is paused. Checking only before and after fn lets
// every mutation fn performs while stalled complete before the loss is
// reported, so it races the reclaiming process's read-modify-write rather
// than aborting before touching shared state. A mid-fn check closes that
// window to whatever fn's own step granularity is.
type leaseCheck func() error

func withLeasedLocks(paths []string, now func() time.Time, fn func(check leaseCheck) error) error {
	var leases []*leasedPath
	released := false
	releaseAll := func() error {
		if released {
			return nil
		}
		released = true
		var firstErr error
		for i := len(leases) - 1; i >= 0; i-- {
			if err := leases[i].release(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		unlock, token, err := acquireFileLock(p, now)
		if err != nil {
			_ = releaseAll()
			return err
		}
		// Start refreshing this lock before blocking on the next path.
		leases = append(leases, startLease(p, token, unlock))
	}
	check := func() error {
		for _, l := range leases {
			if l.lost.Load() {
				return fmt.Errorf("oauth: lost token lock lease on %s", filepath.Base(l.path))
			}
			if !ownLockFile(l.path, l.token) {
				l.lost.Store(true)
				return fmt.Errorf("oauth: lost token lock lease on %s", filepath.Base(l.path))
			}
		}
		return nil
	}
	if len(leases) == 0 {
		return fn(check)
	}
	defer func() { _ = releaseAll() }()
	// Fencing: if any lease was already lost while later locks were still
	// being acquired (e.g. the first, global index lock reclaimed as stale
	// while the caller blocks on legacyLockPath), do not enter the critical
	// section at all.
	if err := check(); err != nil {
		return err
	}
	err := fn(check)
	if lostErr := check(); lostErr != nil {
		_ = releaseAll()
		if err != nil {
			return err
		}
		return lostErr
	}
	if relErr := releaseAll(); relErr != nil {
		if err != nil {
			return err
		}
		return relErr
	}
	return err
}

// withLock serializes the keyring's read-modify-write. Store.mu covers the
// in-process case; lockPath adds cross-process exclusion between this
// binary's own instances so two of them can't both read the blob, modify,
// and write — dropping a token. legacyLockPath is held for the same duration
// so a live pre-PR binary that shares this config root (see
// legacyKeyringLockPath) serializes with our reconcile-and-index pass.
// Cross-root old writers cannot share that lock; never overwriting the
// legacy blob and durable tombstones are the remaining safety net for them.
func (b keyringBlob) withLock(now func() time.Time, fn func(check leaseCheck) error) error {
	return withLeasedLocks([]string{b.lockPath, b.legacyLockPath}, now, fn)
}

// withReadLock only takes lockPath: a pre-PR binary never locks for a read
// (see legacyKeyringLockPath), so a read here has nothing to coordinate with
// on the legacy side.
func (b keyringBlob) withReadLock(now func() time.Time, fn func(check leaseCheck) error) error {
	return withLeasedLocks([]string{b.lockPath}, now, fn)
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
