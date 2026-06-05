package mcp

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

type PermissionScope string
type PermissionAutonomy string

const (
	ScopeServer PermissionScope = "server"
	ScopeTool   PermissionScope = "tool"
)

const (
	AutonomyLow    PermissionAutonomy = "low"
	AutonomyMedium PermissionAutonomy = "medium"
	AutonomyHigh   PermissionAutonomy = "high"
)

const (
	permissionSchemaVersion = 1
	permissionLockTimeout   = 5 * time.Second
	permissionLockRetry     = 10 * time.Millisecond
)

type PermissionGrant struct {
	Scope          PermissionScope    `json:"scope"`
	ServerName     string             `json:"serverName"`
	ServerIdentity string             `json:"serverIdentity"`
	ToolName       string             `json:"toolName,omitempty"`
	MaxAutonomy    PermissionAutonomy `json:"maxAutonomy"`
	ApprovedAt     string             `json:"approvedAt"`
}

type StoreOptions struct {
	FilePath string
	Now      func() time.Time
	Env      map[string]string
}

type GrantServerInput struct {
	ServerName     string
	ServerIdentity string
	MaxAutonomy    PermissionAutonomy
}

type GrantToolInput struct {
	ServerName     string
	ServerIdentity string
	ToolName       string
	MaxAutonomy    PermissionAutonomy
}

type CheckToolInput struct {
	ServerName        string
	ServerIdentity    string
	ToolName          string
	RequestedAutonomy PermissionAutonomy
}

type storedGrant struct {
	ServerIdentity string             `json:"serverIdentity"`
	MaxAutonomy    PermissionAutonomy `json:"maxAutonomy"`
	ApprovedAt     string             `json:"approvedAt"`
}

type permissionFile struct {
	SchemaVersion int                               `json:"schemaVersion"`
	Servers       map[string]storedGrant            `json:"servers"`
	Tools         map[string]map[string]storedGrant `json:"tools"`
}

type PermissionStore struct {
	filePath string
	now      func() time.Time
	mu       sync.Mutex
}

var (
	serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	autonomyRank      = map[PermissionAutonomy]int{
		AutonomyLow:    0,
		AutonomyMedium: 1,
		AutonomyHigh:   2,
	}
)

func ResolvePermissionPath(env map[string]string) (string, error) {
	override := strings.TrimSpace(envValue(env, "ZERO_MCP_PERMISSIONS_PATH"))
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
	return filepath.Join(configHome, "zero", "mcp-permissions.json"), nil
}

func NewPermissionStore(options StoreOptions) (*PermissionStore, error) {
	filePath := options.FilePath
	var err error
	if strings.TrimSpace(filePath) == "" {
		filePath, err = ResolvePermissionPath(options.Env)
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
	return &PermissionStore{filePath: filepath.Clean(filePath), now: now}, nil
}

func (store *PermissionStore) FilePath() string {
	return store.filePath
}

func (store *PermissionStore) GrantServer(input GrantServerInput) (PermissionGrant, error) {
	grant, err := createStoredGrant(input.ServerIdentity, input.MaxAutonomy, store.now)
	if err != nil {
		return PermissionGrant{}, err
	}
	if err := ValidateServerName(input.ServerName); err != nil {
		return PermissionGrant{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.lockStateFile()
	if err != nil {
		return PermissionGrant{}, err
	}
	defer unlock()

	state, err := store.readState()
	if err != nil {
		return PermissionGrant{}, err
	}
	state.Servers[input.ServerName] = grant
	if err := store.writeState(state); err != nil {
		return PermissionGrant{}, err
	}
	return toServerGrant(input.ServerName, grant), nil
}

func (store *PermissionStore) GrantTool(input GrantToolInput) (PermissionGrant, error) {
	grant, err := createStoredGrant(input.ServerIdentity, input.MaxAutonomy, store.now)
	if err != nil {
		return PermissionGrant{}, err
	}
	if err := ValidateServerName(input.ServerName); err != nil {
		return PermissionGrant{}, err
	}
	if err := validateToolName(input.ToolName); err != nil {
		return PermissionGrant{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.lockStateFile()
	if err != nil {
		return PermissionGrant{}, err
	}
	defer unlock()

	state, err := store.readState()
	if err != nil {
		return PermissionGrant{}, err
	}
	if state.Tools[input.ServerName] == nil {
		state.Tools[input.ServerName] = map[string]storedGrant{}
	}
	state.Tools[input.ServerName][input.ToolName] = grant
	if err := store.writeState(state); err != nil {
		return PermissionGrant{}, err
	}
	return toToolGrant(input.ServerName, input.ToolName, grant), nil
}

func (store *PermissionStore) IsToolPersistentlyApproved(input CheckToolInput) (bool, error) {
	if err := ValidateServerName(input.ServerName); err != nil {
		return false, err
	}
	if err := validateToolName(input.ToolName); err != nil {
		return false, err
	}
	requested, err := normalizeAutonomy(defaultAutonomy(input.RequestedAutonomy))
	if err != nil {
		return false, err
	}

	state, err := store.readState()
	if err != nil {
		return false, err
	}
	if isGrantAllowed(state.Tools[input.ServerName][input.ToolName], input.ServerIdentity, requested) {
		return true, nil
	}
	return isGrantAllowed(state.Servers[input.ServerName], input.ServerIdentity, requested), nil
}

func (store *PermissionStore) List() ([]PermissionGrant, error) {
	state, err := store.readState()
	if err != nil {
		return nil, err
	}
	grants := make([]PermissionGrant, 0, countGrants(state))
	serverNames := make([]string, 0, len(state.Servers))
	for serverName := range state.Servers {
		serverNames = append(serverNames, serverName)
	}
	sort.Strings(serverNames)
	for _, serverName := range serverNames {
		grants = append(grants, toServerGrant(serverName, state.Servers[serverName]))
	}

	toolServers := make([]string, 0, len(state.Tools))
	for serverName := range state.Tools {
		toolServers = append(toolServers, serverName)
	}
	sort.Strings(toolServers)
	for _, serverName := range toolServers {
		toolNames := make([]string, 0, len(state.Tools[serverName]))
		for toolName := range state.Tools[serverName] {
			toolNames = append(toolNames, toolName)
		}
		sort.Strings(toolNames)
		for _, toolName := range toolNames {
			grants = append(grants, toToolGrant(serverName, toolName, state.Tools[serverName][toolName]))
		}
	}
	return grants, nil
}

func (store *PermissionStore) RevokeTool(serverName string, toolName string) (int, error) {
	if err := ValidateServerName(serverName); err != nil {
		return 0, err
	}
	if err := validateToolName(toolName); err != nil {
		return 0, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.lockStateFile()
	if err != nil {
		return 0, err
	}
	defer unlock()

	state, err := store.readState()
	if err != nil {
		return 0, err
	}
	serverTools := state.Tools[serverName]
	if serverTools == nil {
		return 0, nil
	}
	if _, ok := serverTools[toolName]; !ok {
		return 0, nil
	}
	delete(serverTools, toolName)
	if len(serverTools) == 0 {
		delete(state.Tools, serverName)
	}
	if err := store.writeState(state); err != nil {
		return 0, err
	}
	return 1, nil
}

func (store *PermissionStore) RevokeServer(serverName string) (int, error) {
	if err := ValidateServerName(serverName); err != nil {
		return 0, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.lockStateFile()
	if err != nil {
		return 0, err
	}
	defer unlock()

	state, err := store.readState()
	if err != nil {
		return 0, err
	}
	revoked := 0
	if _, ok := state.Servers[serverName]; ok {
		revoked++
		delete(state.Servers, serverName)
	}
	if serverTools := state.Tools[serverName]; serverTools != nil {
		revoked += len(serverTools)
		delete(state.Tools, serverName)
	}
	if revoked > 0 {
		if err := store.writeState(state); err != nil {
			return 0, err
		}
	}
	return revoked, nil
}

func (store *PermissionStore) Clear() (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.lockStateFile()
	if err != nil {
		return 0, err
	}
	defer unlock()

	state, err := store.readState()
	if err != nil {
		return 0, err
	}
	count := countGrants(state)
	if count > 0 {
		if err := store.writeState(emptyState()); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func FormatPermissionList(permissions []PermissionGrant) string {
	if len(permissions) == 0 {
		return "No persistent MCP permissions granted."
	}
	lines := []string{"MCP Permissions:"}
	for _, permission := range permissions {
		target := permission.ServerName + "/*"
		if permission.Scope == ScopeTool {
			target = permission.ServerName + "/" + permission.ToolName
		}
		lines = append(lines, fmt.Sprintf("  %s [%s] %s approved %s", target, permission.MaxAutonomy, permission.ServerIdentity, permission.ApprovedAt))
	}
	return strings.Join(lines, "\n")
}

func ValidateServerName(name string) error {
	if !serverNamePattern.MatchString(strings.TrimSpace(name)) {
		return fmt.Errorf("invalid MCP server name %q. Use letters, numbers, dots, dashes, or underscores", name)
	}
	return nil
}

func (store *PermissionStore) readState() (permissionFile, error) {
	data, err := os.ReadFile(store.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyState(), nil
		}
		return permissionFile{}, err
	}
	var state permissionFile
	if err := json.Unmarshal(data, &state); err != nil {
		return permissionFile{}, fmt.Errorf("invalid MCP permissions file at %s: %w", store.filePath, err)
	}
	if state.SchemaVersion != permissionSchemaVersion {
		return permissionFile{}, fmt.Errorf("invalid MCP permissions file at %s: unsupported schemaVersion", store.filePath)
	}
	if state.Servers == nil {
		state.Servers = map[string]storedGrant{}
	}
	if state.Tools == nil {
		state.Tools = map[string]map[string]storedGrant{}
	}
	for serverName, grant := range state.Servers {
		if err := ValidateServerName(serverName); err != nil {
			return permissionFile{}, fmt.Errorf("invalid MCP permissions file at %s: %w", store.filePath, err)
		}
		normalized, err := normalizeStoredGrant(grant, "servers."+serverName)
		if err != nil {
			return permissionFile{}, fmt.Errorf("invalid MCP permissions file at %s: %w", store.filePath, err)
		}
		state.Servers[serverName] = normalized
	}
	for serverName, serverTools := range state.Tools {
		if err := ValidateServerName(serverName); err != nil {
			return permissionFile{}, fmt.Errorf("invalid MCP permissions file at %s: %w", store.filePath, err)
		}
		if serverTools == nil {
			state.Tools[serverName] = map[string]storedGrant{}
			continue
		}
		for toolName, grant := range serverTools {
			if err := validateToolName(toolName); err != nil {
				return permissionFile{}, fmt.Errorf("invalid MCP permissions file at %s: %w", store.filePath, err)
			}
			normalized, err := normalizeStoredGrant(grant, "tools."+serverName+"."+toolName)
			if err != nil {
				return permissionFile{}, fmt.Errorf("invalid MCP permissions file at %s: %w", store.filePath, err)
			}
			serverTools[toolName] = normalized
		}
	}
	return state, nil
}

func (store *PermissionStore) writeState(state permissionFile) error {
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

func (store *PermissionStore) lockStateFile() (func(), error) {
	lockPath := store.filePath + ".lockfile"
	if err := os.MkdirAll(filepath.Dir(store.filePath), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(permissionLockTimeout)
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
			return nil, fmt.Errorf("timed out waiting for MCP permissions lock at %s", lockPath)
		}
		time.Sleep(permissionLockRetry)
	}
}

func emptyState() permissionFile {
	return permissionFile{
		SchemaVersion: permissionSchemaVersion,
		Servers:       map[string]storedGrant{},
		Tools:         map[string]map[string]storedGrant{},
	}
}

func createStoredGrant(serverIdentity string, maxAutonomy PermissionAutonomy, now func() time.Time) (storedGrant, error) {
	identity := strings.TrimSpace(serverIdentity)
	if identity == "" {
		return storedGrant{}, errors.New("MCP server identity is required")
	}
	autonomy, err := normalizeAutonomy(defaultAutonomy(maxAutonomy))
	if err != nil {
		return storedGrant{}, err
	}
	return storedGrant{
		ServerIdentity: identity,
		MaxAutonomy:    autonomy,
		ApprovedAt:     now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func normalizeStoredGrant(grant storedGrant, label string) (storedGrant, error) {
	if strings.TrimSpace(grant.ServerIdentity) == "" {
		return storedGrant{}, fmt.Errorf("expected %s.serverIdentity to be a non-empty string", label)
	}
	if strings.TrimSpace(grant.ApprovedAt) == "" {
		return storedGrant{}, fmt.Errorf("expected %s.approvedAt to be a non-empty string", label)
	}
	autonomy, err := normalizeAutonomy(grant.MaxAutonomy)
	if err != nil {
		return storedGrant{}, err
	}
	grant.ServerIdentity = strings.TrimSpace(grant.ServerIdentity)
	grant.ApprovedAt = strings.TrimSpace(grant.ApprovedAt)
	grant.MaxAutonomy = autonomy
	return grant, nil
}

func normalizeAutonomy(value PermissionAutonomy) (PermissionAutonomy, error) {
	normalized := PermissionAutonomy(strings.ToLower(strings.TrimSpace(string(value))))
	switch normalized {
	case AutonomyLow, AutonomyMedium, AutonomyHigh:
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid MCP permission autonomy %q. Expected low, medium, or high", value)
	}
}

func defaultAutonomy(value PermissionAutonomy) PermissionAutonomy {
	if strings.TrimSpace(string(value)) == "" {
		return AutonomyLow
	}
	return value
}

func validateToolName(toolName string) error {
	if strings.TrimSpace(toolName) == "" {
		return errors.New("MCP tool name is required")
	}
	return nil
}

func isGrantAllowed(grant storedGrant, serverIdentity string, requestedAutonomy PermissionAutonomy) bool {
	if grant.ServerIdentity == "" {
		return false
	}
	if grant.ServerIdentity != serverIdentity {
		return false
	}
	return autonomyRank[requestedAutonomy] <= autonomyRank[grant.MaxAutonomy]
}

func toServerGrant(serverName string, grant storedGrant) PermissionGrant {
	return PermissionGrant{
		Scope:          ScopeServer,
		ServerName:     serverName,
		ServerIdentity: grant.ServerIdentity,
		MaxAutonomy:    grant.MaxAutonomy,
		ApprovedAt:     grant.ApprovedAt,
	}
}

func toToolGrant(serverName string, toolName string, grant storedGrant) PermissionGrant {
	return PermissionGrant{
		Scope:          ScopeTool,
		ServerName:     serverName,
		ServerIdentity: grant.ServerIdentity,
		ToolName:       toolName,
		MaxAutonomy:    grant.MaxAutonomy,
		ApprovedAt:     grant.ApprovedAt,
	}
}

func countGrants(state permissionFile) int {
	count := len(state.Servers)
	for _, serverTools := range state.Tools {
		count += len(serverTools)
	}
	return count
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
