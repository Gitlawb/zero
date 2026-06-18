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

const grantSchemaVersion = 2

type Grant struct {
	ToolName    string        `json:"toolName"`
	Scope       string        `json:"scope,omitempty"`     // absolute path, host, or "" for tool-wide
	ScopeKind   ScopeKind     `json:"scopeKind,omitempty"` // file | dir | host | "" (tool-wide)
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
	// Scope is the raw path or host the grant covers; ScopeKind classifies it.
	// engine.Grant resolves path scopes to absolute paths and normalizes host
	// scopes before persisting. Both empty means a tool-wide grant.
	Scope     string
	ScopeKind ScopeKind
}

type GrantLookup struct {
	Matched bool  `json:"matched"`
	Grant   Grant `json:"grant,omitempty"`
}

type grantFile struct {
	SchemaVersion int                `json:"schemaVersion"`
	Grants        map[string][]Grant `json:"grants"`
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
	bucket := state.Grants[grant.ToolName]
	replaced := false
	for i := range bucket {
		// A re-grant of the same (scope, kind) updates the existing record rather
		// than accumulating duplicates.
		if bucket[i].Scope == grant.Scope && bucket[i].ScopeKind == grant.ScopeKind {
			bucket[i] = grant
			replaced = true
			break
		}
	}
	if !replaced {
		bucket = append(bucket, grant)
	}
	state.Grants[grant.ToolName] = bucket
	if err := store.writeState(state); err != nil {
		return Grant{}, err
	}
	return grant, nil
}

// Lookup returns the grant that governs a tool call whose normalized scope is
// reqScope (empty for a tool-wide request, e.g. a shell command with no cwd).
// Among the tool's grants that cover the request, a covering deny wins outright
// (regardless of autonomy); otherwise the most-specific covering allow whose
// recorded MaxAutonomy admits the requested autonomy is returned.
func (store *GrantStore) Lookup(toolName string, reqScope string, requestedAutonomy Autonomy) (GrantLookup, error) {
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
	bucket := state.Grants[strings.TrimSpace(toolName)]
	var bestAllow *Grant
	for i := range bucket {
		grant := bucket[i]
		if !grantCovers(grant, reqScope) {
			continue
		}
		if grant.Decision == GrantDeny {
			covering := grant
			return GrantLookup{Matched: true, Grant: covering}, nil
		}
		if !autonomyAllowed(requested, grant.MaxAutonomy) {
			continue
		}
		if bestAllow == nil || moreSpecific(grant, *bestAllow) {
			covering := grant
			bestAllow = &covering
		}
	}
	if bestAllow != nil {
		return GrantLookup{Matched: true, Grant: *bestAllow}, nil
	}
	return GrantLookup{}, nil
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
		bucket := append([]Grant(nil), state.Grants[name]...)
		sort.Slice(bucket, func(i, j int) bool {
			if bucket[i].Scope != bucket[j].Scope {
				return bucket[i].Scope < bucket[j].Scope
			}
			return bucket[i].ScopeKind < bucket[j].ScopeKind
		})
		grants = append(grants, bucket...)
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
	key := strings.TrimSpace(toolName)
	bucket, ok := state.Grants[key]
	if !ok || len(bucket) == 0 {
		return 0, nil
	}
	count := len(bucket)
	delete(state.Grants, key)
	if err := store.writeState(state); err != nil {
		return 0, err
	}
	return count, nil
}

// RevokePath revokes only the grants for toolName whose scope matches scopePath
// (a file or directory grant), leaving tool-wide and other-path grants intact.
// scopePath is canonicalized to an absolute, cleaned path the same way stored
// scopes are, so the caller can pass either an absolute path or one relative to
// the working directory (or copy it from `grants list`). Returns the number of
// grants removed.
func (store *GrantStore) RevokePath(toolName string, scopePath string) (int, error) {
	if err := ValidateToolName(toolName); err != nil {
		return 0, err
	}
	target := resolveScopeAbs(scopePath, "")
	if target == "" {
		return 0, fmt.Errorf("a non-empty --path is required to revoke a single grant")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.readState()
	if err != nil {
		return 0, err
	}
	key := strings.TrimSpace(toolName)
	bucket, ok := state.Grants[key]
	if !ok || len(bucket) == 0 {
		return 0, nil
	}
	kept := make([]Grant, 0, len(bucket))
	removed := 0
	for _, grant := range bucket {
		if grant.Scope == target {
			removed++
			continue
		}
		kept = append(kept, grant)
	}
	if removed == 0 {
		return 0, nil
	}
	if len(kept) == 0 {
		delete(state.Grants, key)
	} else {
		state.Grants[key] = kept
	}
	if err := store.writeState(state); err != nil {
		return 0, err
	}
	return removed, nil
}

func (store *GrantStore) Clear() (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.readState()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, bucket := range state.Grants {
		count += len(bucket)
	}
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
		scope := grant.Scope
		if scope == "" {
			scope = "*" // tool-wide
		}
		line := fmt.Sprintf("  %s (%s) [%s/%s] approved %s", grant.ToolName, scope, grant.Decision, grant.MaxAutonomy, grant.ApprovedAt)
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
	kind, err := normalizeScopeKind(input.ScopeKind)
	if err != nil {
		return Grant{}, err
	}
	scope := strings.TrimSpace(input.Scope)
	scope, kind = reconcileScope(scope, kind)
	return Grant{
		ToolName:    toolName,
		Scope:       scope,
		ScopeKind:   kind,
		Decision:    decision,
		MaxAutonomy: autonomy,
		ApprovedAt:  now().UTC().Format(time.RFC3339),
		Reason:      redaction.RedactString(strings.TrimSpace(input.Reason), redaction.Options{}),
	}, nil
}

// normalizeScopeKind validates and lower-cases a scope kind. An empty kind is the
// tool-wide grant.
func normalizeScopeKind(kind ScopeKind) (ScopeKind, error) {
	switch ScopeKind(strings.ToLower(strings.TrimSpace(string(kind)))) {
	case ScopeToolWide:
		return ScopeToolWide, nil
	case ScopeFile:
		return ScopeFile, nil
	case ScopeDir:
		return ScopeDir, nil
	case ScopeHost:
		return ScopeHost, nil
	default:
		return "", fmt.Errorf("invalid sandbox scope kind %q. Expected file, dir, host, or empty", kind)
	}
}

// reconcileScope keeps scope and kind consistent: a tool-wide kind carries no
// path, and a file/dir kind with no path degrades to tool-wide (a scoped grant
// with nothing to scope is meaningless).
func reconcileScope(scope string, kind ScopeKind) (string, ScopeKind) {
	if kind == ScopeToolWide || scope == "" {
		return "", ScopeToolWide
	}
	if kind == ScopeHost {
		host := normalizeHostScope(scope)
		if host == "" {
			return "", ScopeToolWide
		}
		return host, ScopeHost
	}
	return scope, kind
}

func (store *GrantStore) readState() (grantFile, error) {
	data, err := os.ReadFile(store.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyGrantState(), nil
		}
		return grantFile{}, err
	}
	// Peek at the schema version so a legacy (v1) file — whose grants are keyed
	// directly to a single Grant — can be decoded and migrated separately from the
	// current (v2) map[tool][]Grant shape.
	var head struct {
		SchemaVersion int             `json:"schemaVersion"`
		Grants        json.RawMessage `json:"grants"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return grantFile{}, store.invalidGrantFile(err)
	}
	buckets := map[string][]Grant{}
	switch head.SchemaVersion {
	case 1:
		var legacy map[string]Grant
		if len(head.Grants) > 0 {
			if err := json.Unmarshal(head.Grants, &legacy); err != nil {
				return grantFile{}, store.invalidGrantFile(err)
			}
		}
		for name, grant := range legacy {
			// A v1 grant was tool-wide; carry it forward as such.
			grant.Scope = ""
			grant.ScopeKind = ScopeToolWide
			buckets[name] = append(buckets[name], grant)
		}
	case grantSchemaVersion:
		if len(head.Grants) > 0 {
			if err := json.Unmarshal(head.Grants, &buckets); err != nil {
				return grantFile{}, store.invalidGrantFile(err)
			}
		}
	default:
		return grantFile{}, fmt.Errorf("invalid sandbox grants file at %s: unsupported schemaVersion", store.filePath)
	}
	// Validate and normalize every grant, canonicalizing tool keys (trimmed) so a
	// whitespace-padded key in the file still matches at lookup time.
	normalized := map[string][]Grant{}
	for name, bucket := range buckets {
		key := strings.TrimSpace(name)
		if err := ValidateToolName(key); err != nil {
			return grantFile{}, store.invalidGrantFile(err)
		}
		for _, grant := range bucket {
			ng, err := normalizeStoredGrant(key, grant)
			if err != nil {
				return grantFile{}, store.invalidGrantFile(err)
			}
			normalized[key] = append(normalized[key], ng)
		}
	}
	return grantFile{SchemaVersion: grantSchemaVersion, Grants: normalized}, nil
}

func (store *GrantStore) invalidGrantFile(err error) error {
	return fmt.Errorf("invalid sandbox grants file at %s: %w", store.filePath, err)
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
	if strings.TrimSpace(grant.ToolName) != name {
		return Grant{}, fmt.Errorf("grant key %q does not match toolName %q", name, grant.ToolName)
	}
	grant.ToolName = name
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
	kind, err := normalizeScopeKind(grant.ScopeKind)
	if err != nil {
		return Grant{}, err
	}
	grant.Decision = decision
	grant.MaxAutonomy = autonomy
	grant.Scope, grant.ScopeKind = reconcileScope(strings.TrimSpace(grant.Scope), kind)
	grant.ApprovedAt = strings.TrimSpace(grant.ApprovedAt)
	grant.Reason = redaction.RedactString(strings.TrimSpace(grant.Reason), redaction.Options{})
	return grant, nil
}

func emptyGrantState() grantFile {
	return grantFile{
		SchemaVersion: grantSchemaVersion,
		Grants:        map[string][]Grant{},
	}
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}
