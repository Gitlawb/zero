package sandbox

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

	"github.com/Gitlawb/zero/internal/redaction"
)

const grantSchemaVersion = 1

type Grant struct {
	ToolName    string        `json:"toolName"`
	Decision    GrantDecision `json:"decision"`
	MaxAutonomy Autonomy      `json:"maxAutonomy"`
	ApprovedAt  string        `json:"approvedAt"`
	Reason      string        `json:"reason,omitempty"`
}

type StoreOptions struct {
	FilePath string
	Now      func() time.Time
	Env      map[string]string
}

type GrantInput struct {
	ToolName    string
	Decision    GrantDecision
	MaxAutonomy Autonomy
	Reason      string
}

type GrantLookup struct {
	Matched bool  `json:"matched"`
	Grant   Grant `json:"grant,omitempty"`
}

type grantFile struct {
	SchemaVersion int              `json:"schemaVersion"`
	Grants        map[string]Grant `json:"grants"`
}

type GrantStore struct {
	filePath string
	now      func() time.Time
	mu       sync.Mutex
}

var toolGrantNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func ResolveGrantPath(env map[string]string) (string, error) {
	override := strings.TrimSpace(envValue(env, "ZERO_SANDBOX_GRANTS_PATH"))
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
	return filepath.Join(configHome, "zero", "sandbox-grants.json"), nil
}

func NewGrantStore(options StoreOptions) (*GrantStore, error) {
	filePath := strings.TrimSpace(options.FilePath)
	var err error
	if filePath == "" {
		filePath, err = ResolveGrantPath(options.Env)
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
	return &GrantStore{filePath: filepath.Clean(filePath), now: now}, nil
}

func (store *GrantStore) FilePath() string {
	return store.filePath
}

func (store *GrantStore) Grant(input GrantInput) (Grant, error) {
	grant, err := createGrant(input, store.now)
	if err != nil {
		return Grant{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.readState()
	if err != nil {
		return Grant{}, err
	}
	state.Grants[grant.ToolName] = grant
	if err := store.writeState(state); err != nil {
		return Grant{}, err
	}
	return grant, nil
}

func (store *GrantStore) Lookup(toolName string, requestedAutonomy Autonomy) (GrantLookup, error) {
	if err := ValidateToolName(toolName); err != nil {
		return GrantLookup{}, err
	}
	requested, err := NormalizeAutonomy(requestedAutonomy)
	if err != nil {
		return GrantLookup{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.readState()
	if err != nil {
		return GrantLookup{}, err
	}
	grant, ok := state.Grants[strings.TrimSpace(toolName)]
	if !ok || !autonomyAllowed(requested, grant.MaxAutonomy) {
		return GrantLookup{}, nil
	}
	return GrantLookup{Matched: true, Grant: grant}, nil
}

func (store *GrantStore) List() ([]Grant, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.readState()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(state.Grants))
	for name := range state.Grants {
		names = append(names, name)
	}
	sort.Strings(names)
	grants := make([]Grant, 0, len(names))
	for _, name := range names {
		grants = append(grants, state.Grants[name])
	}
	return grants, nil
}

func (store *GrantStore) Revoke(toolName string) (int, error) {
	if err := ValidateToolName(toolName); err != nil {
		return 0, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.readState()
	if err != nil {
		return 0, err
	}
	if _, ok := state.Grants[toolName]; !ok {
		return 0, nil
	}
	delete(state.Grants, toolName)
	if err := store.writeState(state); err != nil {
		return 0, err
	}
	return 1, nil
}

func (store *GrantStore) Clear() (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.readState()
	if err != nil {
		return 0, err
	}
	count := len(state.Grants)
	if count == 0 {
		return 0, nil
	}
	if err := store.writeState(emptyGrantState()); err != nil {
		return 0, err
	}
	return count, nil
}

func FormatGrantList(grants []Grant) string {
	if len(grants) == 0 {
		return "No persistent sandbox grants."
	}
	lines := []string{"Sandbox Grants:"}
	for _, grant := range grants {
		line := fmt.Sprintf("  %s [%s/%s] approved %s", grant.ToolName, grant.Decision, grant.MaxAutonomy, grant.ApprovedAt)
		if grant.Reason != "" {
			line += " - " + redaction.RedactString(grant.Reason, redaction.Options{})
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func ValidateToolName(name string) error {
	trimmed := strings.TrimSpace(name)
	if !toolGrantNamePattern.MatchString(trimmed) {
		return fmt.Errorf("invalid sandbox tool name %q. Use letters, numbers, dots, dashes, or underscores", name)
	}
	return nil
}

func createGrant(input GrantInput, now func() time.Time) (Grant, error) {
	toolName := strings.TrimSpace(input.ToolName)
	if err := ValidateToolName(toolName); err != nil {
		return Grant{}, err
	}
	decision, err := NormalizeGrantDecision(input.Decision)
	if err != nil {
		return Grant{}, err
	}
	autonomy, err := NormalizeAutonomy(input.MaxAutonomy)
	if err != nil {
		return Grant{}, err
	}
	return Grant{
		ToolName:    toolName,
		Decision:    decision,
		MaxAutonomy: autonomy,
		ApprovedAt:  now().UTC().Format(time.RFC3339),
		Reason:      redaction.RedactString(strings.TrimSpace(input.Reason), redaction.Options{}),
	}, nil
}

func (store *GrantStore) readState() (grantFile, error) {
	data, err := os.ReadFile(store.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyGrantState(), nil
		}
		return grantFile{}, err
	}
	var state grantFile
	if err := json.Unmarshal(data, &state); err != nil {
		return grantFile{}, fmt.Errorf("invalid sandbox grants file at %s: %w", store.filePath, err)
	}
	if state.SchemaVersion != grantSchemaVersion {
		return grantFile{}, fmt.Errorf("invalid sandbox grants file at %s: unsupported schemaVersion", store.filePath)
	}
	if state.Grants == nil {
		state.Grants = map[string]Grant{}
	}
	for name, grant := range state.Grants {
		if err := ValidateToolName(name); err != nil {
			return grantFile{}, fmt.Errorf("invalid sandbox grants file at %s: %w", store.filePath, err)
		}
		normalized, err := normalizeStoredGrant(name, grant)
		if err != nil {
			return grantFile{}, fmt.Errorf("invalid sandbox grants file at %s: %w", store.filePath, err)
		}
		state.Grants[name] = normalized
	}
	return state, nil
}

func (store *GrantStore) writeState(state grantFile) error {
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

func normalizeStoredGrant(name string, grant Grant) (Grant, error) {
	if strings.TrimSpace(grant.ToolName) == "" {
		grant.ToolName = name
	}
	if grant.ToolName != name {
		return Grant{}, fmt.Errorf("grant key %q does not match toolName %q", name, grant.ToolName)
	}
	if strings.TrimSpace(grant.ApprovedAt) == "" {
		return Grant{}, fmt.Errorf("grant %s approvedAt is required", name)
	}
	decision, err := NormalizeGrantDecision(grant.Decision)
	if err != nil {
		return Grant{}, err
	}
	autonomy, err := NormalizeAutonomy(grant.MaxAutonomy)
	if err != nil {
		return Grant{}, err
	}
	grant.Decision = decision
	grant.MaxAutonomy = autonomy
	grant.ApprovedAt = strings.TrimSpace(grant.ApprovedAt)
	grant.Reason = redaction.RedactString(strings.TrimSpace(grant.Reason), redaction.Options{})
	return grant, nil
}

func emptyGrantState() grantFile {
	return grantFile{
		SchemaVersion: grantSchemaVersion,
		Grants:        map[string]Grant{},
	}
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}
