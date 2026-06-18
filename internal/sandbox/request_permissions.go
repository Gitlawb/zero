package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type PermissionGrantScope string

const (
	PermissionGrantScopeTurn    PermissionGrantScope = "turn"
	PermissionGrantScopeSession PermissionGrantScope = "session"
)

type RequestPermissionProfile struct {
	Network    *NetworkPermissions    `json:"network,omitempty"`
	FileSystem *FileSystemPermissions `json:"file_system,omitempty"`
}

type RequestPermissionsResponse struct {
	Permissions      RequestPermissionProfile `json:"permissions"`
	Scope            PermissionGrantScope     `json:"scope"`
	StrictAutoReview bool                     `json:"strict_auto_review,omitempty"`
}

type NetworkPermissions struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type FileSystemPermissions struct {
	Read             []string                 `json:"read,omitempty"`
	Write            []string                 `json:"write,omitempty"`
	DenyRead         []string                 `json:"deny_read,omitempty"`
	GlobScanMaxDepth *int                     `json:"glob_scan_max_depth,omitempty"`
	Entries          []FileSystemSandboxEntry `json:"entries,omitempty"`
}

type FileSystemSandboxEntry struct {
	Path   FileSystemPath       `json:"path"`
	Access FileSystemAccessMode `json:"access"`
}

type FileSystemAccessMode string

const (
	FileSystemAccessRead  FileSystemAccessMode = "read"
	FileSystemAccessWrite FileSystemAccessMode = "write"
	FileSystemAccessDeny  FileSystemAccessMode = "deny"
)

type FileSystemPath struct {
	Kind    string `json:"kind,omitempty"`
	Type    string `json:"type,omitempty"`
	Path    string `json:"path,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

func (path *FileSystemPath) UnmarshalJSON(data []byte) error {
	var rawString string
	if err := json.Unmarshal(data, &rawString); err == nil {
		path.Kind = "path"
		path.Type = "path"
		path.Path = rawString
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	_ = json.Unmarshal(raw["kind"], &path.Kind)
	_ = json.Unmarshal(raw["type"], &path.Type)
	_ = json.Unmarshal(raw["path"], &path.Path)
	_ = json.Unmarshal(raw["pattern"], &path.Pattern)
	return nil
}

func (profile RequestPermissionProfile) Empty() bool {
	networkEmpty := profile.Network == nil || profile.Network.Enabled == nil || !*profile.Network.Enabled
	fs := profile.FileSystem
	fileSystemEmpty := fs == nil ||
		(len(fs.Read) == 0 && len(fs.Write) == 0 && len(fs.DenyRead) == 0 && len(fs.Entries) == 0)
	return networkEmpty && fileSystemEmpty
}

func NormalizeRequestPermissionProfile(profile RequestPermissionProfile, basePath string) (RequestPermissionProfile, error) {
	var normalized RequestPermissionProfile
	if profile.Network != nil && profile.Network.Enabled != nil && *profile.Network.Enabled {
		enabled := true
		normalized.Network = &NetworkPermissions{Enabled: &enabled}
	}
	if profile.FileSystem != nil {
		fs, err := normalizeFileSystemPermissions(*profile.FileSystem, basePath)
		if err != nil {
			return RequestPermissionProfile{}, err
		}
		if len(fs.Read) > 0 || len(fs.Write) > 0 || len(fs.DenyRead) > 0 {
			normalized.FileSystem = &fs
		}
	}
	return normalized, nil
}

// RequestPermissionGrantProfile returns the effective grant profile the engine
// can enforce. File-like read/write requests become their normalized parent
// directory roots, matching GrantRequestPermissions and native sandbox profiles.
func RequestPermissionGrantProfile(profile RequestPermissionProfile) (RequestPermissionProfile, error) {
	var grant RequestPermissionProfile
	if profile.Network != nil && profile.Network.Enabled != nil && *profile.Network.Enabled {
		enabled := true
		grant.Network = &NetworkPermissions{Enabled: &enabled}
	}
	if profile.FileSystem != nil {
		read, err := grantPermissionRoots(profile.FileSystem.Read)
		if err != nil {
			return RequestPermissionProfile{}, err
		}
		write, err := grantPermissionRoots(profile.FileSystem.Write)
		if err != nil {
			return RequestPermissionProfile{}, err
		}
		fs := FileSystemPermissions{
			Read:     read,
			Write:    write,
			DenyRead: append([]string{}, profile.FileSystem.DenyRead...),
		}
		if len(fs.Read) > 0 || len(fs.Write) > 0 || len(fs.DenyRead) > 0 {
			grant.FileSystem = &fs
		}
	}
	return grant, nil
}

func grantPermissionRoots(paths []string) ([]string, error) {
	roots := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		root, err := normalizeScopeRoot(permissionRoot(path))
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return dedupeStrings(roots), nil
}

func normalizeFileSystemPermissions(fs FileSystemPermissions, basePath string) (FileSystemPermissions, error) {
	var out FileSystemPermissions
	var err error
	if out.Read, err = normalizePermissionPaths(fs.Read, basePath); err != nil {
		return FileSystemPermissions{}, err
	}
	if out.Write, err = normalizePermissionPaths(fs.Write, basePath); err != nil {
		return FileSystemPermissions{}, err
	}
	if out.DenyRead, err = normalizePermissionPaths(fs.DenyRead, basePath); err != nil {
		return FileSystemPermissions{}, err
	}
	for _, entry := range fs.Entries {
		path, ok := entry.Path.pathString()
		if !ok {
			continue
		}
		normalized, err := normalizePermissionPath(path, basePath)
		if err != nil {
			return FileSystemPermissions{}, err
		}
		switch normalizeFileSystemAccess(entry.Access) {
		case FileSystemAccessRead:
			out.Read = append(out.Read, normalized)
		case FileSystemAccessWrite:
			out.Write = append(out.Write, normalized)
		case FileSystemAccessDeny:
			out.DenyRead = append(out.DenyRead, normalized)
		}
	}
	out.Read = dedupeStrings(out.Read)
	out.Write = dedupeStrings(out.Write)
	out.DenyRead = dedupeStrings(out.DenyRead)
	return out, nil
}

func (path FileSystemPath) pathString() (string, bool) {
	kind := strings.TrimSpace(firstNonEmpty(path.Type, path.Kind))
	if kind == "" || kind == "path" {
		value := strings.TrimSpace(path.Path)
		return value, value != ""
	}
	return "", false
}

func normalizeFileSystemAccess(access FileSystemAccessMode) FileSystemAccessMode {
	switch strings.ToLower(strings.TrimSpace(string(access))) {
	case "read":
		return FileSystemAccessRead
	case "write":
		return FileSystemAccessWrite
	case "deny", "none":
		return FileSystemAccessDeny
	default:
		return ""
	}
}

func normalizePermissionPaths(paths []string, basePath string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		normalized, err := normalizePermissionPath(path, basePath)
		if err != nil {
			return nil, err
		}
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return dedupeStrings(out), nil
}

func normalizePermissionPath(path string, basePath string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	if !filepath.IsAbs(trimmed) {
		base := strings.TrimSpace(basePath)
		if base == "" {
			var err error
			base, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		trimmed = filepath.Join(base, trimmed)
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return filepath.Clean(resolved), nil
	}
	return filepath.Clean(absolute), nil
}

func (engine *Engine) GrantRequestPermissions(profile RequestPermissionProfile, scope PermissionGrantScope) (func(), error) {
	if engine == nil {
		return nil, errors.New("sandbox engine is not configured")
	}
	if scope == "" {
		scope = PermissionGrantScopeTurn
	}
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
	if engine.scope != nil && profile.FileSystem != nil {
		for _, path := range profile.FileSystem.Read {
			root := permissionRoot(path)
			var undo func()
			var err error
			if scope == PermissionGrantScopeSession {
				_, err = engine.scope.AddRead(root)
			} else {
				_, undo, err = engine.scope.AddTemporaryRead(root)
				if undo != nil {
					cleanups = append(cleanups, undo)
				}
			}
			if err != nil {
				cleanup()
				return nil, fmt.Errorf("grant read permission %s: %w", path, err)
			}
		}
		for _, path := range profile.FileSystem.Write {
			root := permissionRoot(path)
			var undo func()
			var err error
			if scope == PermissionGrantScopeSession {
				_, err = engine.scope.Add(root)
			} else {
				_, undo, err = engine.scope.AddTemporaryWrite(root)
				if undo != nil {
					cleanups = append(cleanups, undo)
				}
			}
			if err != nil {
				cleanup()
				return nil, fmt.Errorf("grant write permission %s: %w", path, err)
			}
		}
	}
	switch scope {
	case PermissionGrantScopeSession:
		engine.sessionProfiles.add(profile)
		return func() {}, nil
	case PermissionGrantScopeTurn:
		remove := engine.turnProfiles.add(profile)
		cleanups = append(cleanups, remove)
		return cleanup, nil
	default:
		cleanup()
		return nil, fmt.Errorf("unsupported permission grant scope %q", scope)
	}
}

func permissionRoot(path string) string {
	clean := filepath.Clean(path)
	if info, err := os.Stat(clean); err == nil && info.IsDir() {
		return clean
	}
	return filepath.Dir(clean)
}

func (engine *Engine) effectivePolicy(policy Policy) Policy {
	if engine == nil {
		return policy
	}
	for _, profile := range engine.sessionProfiles.list() {
		policy = applyRequestPermissionProfile(policy, profile)
	}
	for _, profile := range engine.turnProfiles.list() {
		policy = applyRequestPermissionProfile(policy, profile)
	}
	return policy
}

func applyRequestPermissionProfile(policy Policy, profile RequestPermissionProfile) Policy {
	if profile.Network != nil && profile.Network.Enabled != nil && *profile.Network.Enabled {
		policy.Network = NetworkAllow
		policy.AllowedDomains = nil
		policy.DeniedDomains = nil
	}
	if profile.FileSystem != nil {
		policy.AllowRead = dedupeStrings(append(policy.AllowRead, profile.FileSystem.Read...))
		policy.AllowWrite = dedupeStrings(append(policy.AllowWrite, profile.FileSystem.Write...))
		policy.DenyRead = dedupeStrings(append(policy.DenyRead, profile.FileSystem.DenyRead...))
	}
	return policy
}

type memoryGrantSet struct {
	mu     sync.Mutex
	grants map[string][]Grant
}

func newMemoryGrantSet() *memoryGrantSet {
	return &memoryGrantSet{grants: map[string][]Grant{}}
}

func (set *memoryGrantSet) add(grant Grant) {
	if set == nil {
		return
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	bucket := set.grants[grant.ToolName]
	for index := range bucket {
		if bucket[index].Scope == grant.Scope && bucket[index].ScopeKind == grant.ScopeKind {
			bucket[index] = grant
			set.grants[grant.ToolName] = bucket
			return
		}
	}
	set.grants[grant.ToolName] = append(bucket, grant)
}

func (set *memoryGrantSet) lookup(toolName string, reqScope string, requested Autonomy) GrantLookup {
	if set == nil {
		return GrantLookup{}
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	return lookupGrantBucket(set.grants[strings.TrimSpace(toolName)], reqScope, requested)
}

type permissionProfileGrantSet struct {
	mu       sync.Mutex
	nextID   int
	profiles map[int]RequestPermissionProfile
}

func newPermissionProfileGrantSet() *permissionProfileGrantSet {
	return &permissionProfileGrantSet{profiles: map[int]RequestPermissionProfile{}}
}

func (set *permissionProfileGrantSet) add(profile RequestPermissionProfile) func() {
	if set == nil {
		return func() {}
	}
	set.mu.Lock()
	set.nextID++
	id := set.nextID
	set.profiles[id] = profile
	set.mu.Unlock()
	return func() {
		set.mu.Lock()
		delete(set.profiles, id)
		set.mu.Unlock()
	}
}

func (set *permissionProfileGrantSet) list() []RequestPermissionProfile {
	if set == nil {
		return nil
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	out := make([]RequestPermissionProfile, 0, len(set.profiles))
	for _, profile := range set.profiles {
		out = append(out, profile)
	}
	return out
}
