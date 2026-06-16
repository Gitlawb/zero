package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
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

// ProviderKey builds the store key for a provider login.
func ProviderKey(name string) string { return KeyPrefixProvider + name }

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
	// Encrypted selects the AES-256-GCM encrypted-at-rest backend (a per-user
	// secret is created beside the token file). Default (false) writes the 0600
	// plaintext JSON unchanged.
	Encrypted bool
}

// Store persists OAuth tokens (provider + MCP namespaces) in a 0600 file,
// guarded by a cross-process lock and written atomically. The file is plaintext
// JSON by default, or AES-256-GCM ciphertext when the encrypted backend is on.
type Store struct {
	filePath string
	crypter  *aesGCMCrypter // nil => plaintext backend
	now      func() time.Time
	mu       sync.Mutex
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

// NewStore builds a file-backed token store.
func NewStore(options StoreOptions) (*Store, error) {
	filePath := options.FilePath
	var err error
	if strings.TrimSpace(filePath) == "" {
		filePath, err = ResolveStorePath(options.Env)
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(filePath) {
		filePath, err = filepath.Abs(filePath)
		if err != nil {
			return nil, err
		}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	store := &Store{filePath: filepath.Clean(filePath), now: now}
	if options.Encrypted {
		store.crypter = newAESGCMCrypter(store.filePath + ".secret")
	}
	return store, nil
}

// Save persists a token under key, replacing any existing entry.
func (s *Store) Save(key string, token Token) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := acquireFileLock(s.filePath+".lockfile", s.now)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := s.readState()
	if err != nil {
		return err
	}
	state.Tokens[key] = token
	return s.writeState(state)
}

// Load returns the token for key; the bool is false when none is stored.
func (s *Store) Load(key string) (Token, bool, error) {
	if err := ValidateKey(key); err != nil {
		return Token{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
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
	unlock, err := acquireFileLock(s.filePath+".lockfile", s.now)
	if err != nil {
		return false, err
	}
	defer unlock()
	state, err := s.readState()
	if err != nil {
		return false, err
	}
	if _, ok := state.Tokens[key]; !ok {
		return false, nil
	}
	delete(state.Tokens, key)
	if err := s.writeState(state); err != nil {
		return false, err
	}
	return true, nil
}

// Status returns redaction-safe summaries of every stored token, sorted by key.
// An optional prefix filters to one namespace (e.g. KeyPrefixProvider).
func (s *Store) Status(prefix string) ([]Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
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
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyStoreFile(), nil
		}
		return storeFile{}, err
	}
	if s.crypter != nil {
		data, err = s.crypter.open(data)
		if err != nil {
			return storeFile{}, err
		}
	}
	var state storeFile
	if err := json.Unmarshal(data, &state); err != nil {
		return storeFile{}, fmt.Errorf("oauth: invalid token file at %s: %w", s.filePath, err)
	}
	if state.SchemaVersion != storeSchemaVersion {
		return storeFile{}, fmt.Errorf("oauth: invalid token file at %s: unsupported schemaVersion", s.filePath)
	}
	if state.Tokens == nil {
		state.Tokens = map[string]Token{}
	}
	for key := range state.Tokens {
		if err := ValidateKey(key); err != nil {
			return storeFile{}, fmt.Errorf("oauth: invalid token file at %s: %w", s.filePath, err)
		}
	}
	return state, nil
}

func (s *Store) writeState(state storeFile) error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	payload := append(data, '\n')
	if s.crypter != nil {
		// Encrypted backend: the on-disk file is opaque ciphertext, not JSON.
		payload, err = s.crypter.seal(data)
		if err != nil {
			return err
		}
	}
	tempPath := fmt.Sprintf("%s.tmp-%d-%d", s.filePath, os.Getpid(), s.now().UnixNano())
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tempPath, s.filePath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func emptyStoreFile() storeFile {
	return storeFile{SchemaVersion: storeSchemaVersion, Tokens: map[string]Token{}}
}

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
