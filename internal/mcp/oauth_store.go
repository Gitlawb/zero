package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	tokenStoreSchemaVersion = 1
	tokenStoreLockTimeout   = 5 * time.Second
	tokenStoreLockRetry     = 10 * time.Millisecond
)

// StoredToken holds the credentials issued by an OAuth 2.0 authorization server
// for a single MCP server. The token fields are sensitive: they are tagged so
// the repo's redaction layer masks them, and they must never be written to logs
// or stream output.
type StoredToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// TokenStatus is a redaction-safe summary of a stored token. It deliberately
// omits the access and refresh token material so it can be printed by the CLI.
type TokenStatus struct {
	ServerName      string    `json:"serverName"`
	HasToken        bool      `json:"hasToken"`
	HasRefreshToken bool      `json:"hasRefreshToken"`
	TokenType       string    `json:"tokenType,omitempty"`
	Scopes          []string  `json:"scopes,omitempty"`
	ExpiresAt       time.Time `json:"expiresAt,omitempty"`
	Expired         bool      `json:"expired"`
}

// TokenStoreOptions configures where OAuth tokens are persisted. When FilePath
// is empty the path is resolved under the user config dir, mirroring how MCP
// permissions are stored.
type TokenStoreOptions struct {
	FilePath string
	Env      map[string]string
	Now      func() time.Time
}

// TokenStore persists OAuth tokens per MCP server in a 0600 JSON file.
type TokenStore struct {
	filePath string
	now      func() time.Time
	mu       sync.Mutex
}

type tokenFile struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Tokens        map[string]StoredToken `json:"tokens"`
}

// ResolveTokenStorePath determines the on-disk location for OAuth tokens,
// honoring an explicit override, XDG_CONFIG_HOME, then the user home dir.
func ResolveTokenStorePath(env map[string]string) (string, error) {
	override := strings.TrimSpace(envValue(env, "ZERO_MCP_OAUTH_TOKENS_PATH"))
	if override != "" {
		if filepath.IsAbs(override) {
			return filepath.Clean(override), nil
		}
		return filepath.Abs(override)
	}

	configHome := strings.TrimSpace(envValue(env, "XDG_CONFIG_HOME"))
	if configHome == "" {
		home := strings.TrimSpace(firstNonEmpty(envValue(env, "HOME"), envValue(env, "USERPROFILE")))
		var err error
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve user home: %w", err)
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
	return filepath.Join(configHome, "zero", "mcp-oauth-tokens.json"), nil
}

// NewTokenStore builds a file-backed token store.
func NewTokenStore(options TokenStoreOptions) (*TokenStore, error) {
	filePath := options.FilePath
	var err error
	if strings.TrimSpace(filePath) == "" {
		filePath, err = ResolveTokenStorePath(options.Env)
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
	return &TokenStore{filePath: filepath.Clean(filePath), now: now}, nil
}

// FilePath returns the resolved token store path.
func (store *TokenStore) FilePath() string {
	return store.filePath
}

// Save persists the token for a server, replacing any existing entry.
func (store *TokenStore) Save(serverName string, token StoredToken) error {
	if err := ValidateServerName(serverName); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.lockStateFile()
	if err != nil {
		return err
	}
	defer unlock()

	state, err := store.readState()
	if err != nil {
		return err
	}
	state.Tokens[serverName] = token
	return store.writeState(state)
}

// Load returns the stored token for a server. The second return value is false
// when no token has been stored for the server.
func (store *TokenStore) Load(serverName string) (StoredToken, bool, error) {
	if err := ValidateServerName(serverName); err != nil {
		return StoredToken{}, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	state, err := store.readState()
	if err != nil {
		return StoredToken{}, false, err
	}
	token, ok := state.Tokens[serverName]
	return token, ok, nil
}

// Delete removes the stored token for a server. It reports whether an entry was
// present before deletion.
func (store *TokenStore) Delete(serverName string) (bool, error) {
	if err := ValidateServerName(serverName); err != nil {
		return false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.lockStateFile()
	if err != nil {
		return false, err
	}
	defer unlock()

	state, err := store.readState()
	if err != nil {
		return false, err
	}
	if _, ok := state.Tokens[serverName]; !ok {
		return false, nil
	}
	delete(state.Tokens, serverName)
	if err := store.writeState(state); err != nil {
		return false, err
	}
	return true, nil
}

// Status returns a redaction-safe summary of every stored token, sorted by
// server name. It never includes the token material.
func (store *TokenStore) Status() ([]TokenStatus, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	state, err := store.readState()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(state.Tokens))
	for name := range state.Tokens {
		names = append(names, name)
	}
	sort.Strings(names)

	now := store.now()
	statuses := make([]TokenStatus, 0, len(names))
	for _, name := range names {
		token := state.Tokens[name]
		status := TokenStatus{
			ServerName:      name,
			HasToken:        strings.TrimSpace(token.AccessToken) != "",
			HasRefreshToken: strings.TrimSpace(token.RefreshToken) != "",
			TokenType:       token.TokenType,
			Scopes:          token.Scopes,
			ExpiresAt:       token.ExpiresAt,
		}
		if !token.ExpiresAt.IsZero() && !token.ExpiresAt.After(now) {
			status.Expired = true
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (store *TokenStore) readState() (tokenFile, error) {
	data, err := os.ReadFile(store.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyTokenFile(), nil
		}
		return tokenFile{}, err
	}
	var state tokenFile
	if err := json.Unmarshal(data, &state); err != nil {
		return tokenFile{}, fmt.Errorf("invalid MCP OAuth token file at %s: %w", store.filePath, err)
	}
	if state.SchemaVersion != tokenStoreSchemaVersion {
		return tokenFile{}, fmt.Errorf("invalid MCP OAuth token file at %s: unsupported schemaVersion", store.filePath)
	}
	if state.Tokens == nil {
		state.Tokens = map[string]StoredToken{}
	}
	for serverName := range state.Tokens {
		if err := ValidateServerName(serverName); err != nil {
			return tokenFile{}, fmt.Errorf("invalid MCP OAuth token file at %s: %w", store.filePath, err)
		}
	}
	return state, nil
}

func (store *TokenStore) writeState(state tokenFile) error {
	if err := os.MkdirAll(filepath.Dir(store.filePath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tempPath := fmt.Sprintf("%s.tmp-%d-%d", store.filePath, os.Getpid(), store.now().UnixNano())
	if err := os.WriteFile(tempPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tempPath, store.filePath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func (store *TokenStore) lockStateFile() (func(), error) {
	lockPath := store.filePath + ".lockfile"
	if err := os.MkdirAll(filepath.Dir(store.filePath), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(tokenStoreLockTimeout)
	for {
		locked, err := tryLockPermissionFile(file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if locked {
			return func() {
				_ = unlockPermissionFile(file)
				_ = file.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("timed out waiting for MCP OAuth token lock at %s", lockPath)
		}
		time.Sleep(tokenStoreLockRetry)
	}
}

func emptyTokenFile() tokenFile {
	return tokenFile{
		SchemaVersion: tokenStoreSchemaVersion,
		Tokens:        map[string]StoredToken{},
	}
}

// FormatTokenStatuses renders a human-readable status table without leaking any
// token material.
func FormatTokenStatuses(statuses []TokenStatus) string {
	if len(statuses) == 0 {
		return "No MCP OAuth tokens are stored."
	}
	var builder strings.Builder
	for index, status := range statuses {
		if index > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(status.ServerName)
		builder.WriteString(": ")
		if !status.HasToken {
			builder.WriteString("no token")
			continue
		}
		builder.WriteString("token present")
		if status.HasRefreshToken {
			builder.WriteString(" (refreshable)")
		}
		if !status.ExpiresAt.IsZero() {
			if status.Expired {
				builder.WriteString(", expired at ")
			} else {
				builder.WriteString(", expires ")
			}
			builder.WriteString(status.ExpiresAt.UTC().Format(time.RFC3339))
		}
	}
	return builder.String()
}
