package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

type FileSystemPolicyKind string

const (
	FileSystemRestricted   FileSystemPolicyKind = "restricted"
	FileSystemUnrestricted FileSystemPolicyKind = "unrestricted"
	FileSystemExternal     FileSystemPolicyKind = "external"
)

type PermissionProfile struct {
	FileSystem FileSystemPolicy `json:"fileSystem"`
	Network    NetworkPolicy    `json:"network"`
}

type FileSystemPolicy struct {
	Kind                 FileSystemPolicyKind `json:"kind"`
	ReadRoots            []string             `json:"readRoots,omitempty"`
	WriteRoots           []WritableRoot       `json:"writeRoots,omitempty"`
	DenyRead             []string             `json:"denyRead,omitempty"`
	DenyWrite            []string             `json:"denyWrite,omitempty"`
	IncludePlatformRoots bool                 `json:"includePlatformRoots,omitempty"`
	AllowTemp            bool                 `json:"allowTemp,omitempty"`
}

type WritableRoot struct {
	Root                   string   `json:"root"`
	ReadOnlySubpaths       []string `json:"readOnlySubpaths,omitempty"`
	ProtectedMetadataNames []string `json:"protectedMetadataNames,omitempty"`
}

type NetworkPolicy struct {
	Mode           NetworkMode `json:"mode"`
	AllowedDomains []string    `json:"allowedDomains,omitempty"`
	DeniedDomains  []string    `json:"deniedDomains,omitempty"`
	ProxyRequired  bool        `json:"proxyRequired,omitempty"`
}

var protectedMetadataNames = []string{".git", ".zero", ".agents"}

func DefaultPermissionProfile(workspaceRoot string) PermissionProfile {
	return PermissionProfileFromPolicy(workspaceRoot, DefaultPolicy(), nil)
}

func PermissionProfileFromPolicy(workspaceRoot string, policy Policy, scope *Scope) PermissionProfile {
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	if policy.Mode == ModeDisabled {
		return PermissionProfile{
			FileSystem: FileSystemPolicy{Kind: FileSystemUnrestricted, IncludePlatformRoots: true, AllowTemp: true},
			Network:    NetworkPolicy{Mode: NetworkAllow},
		}
	}

	roots := permissionProfileRoots(workspaceRoot, scope)
	if extra := normalizeProfileDirs(policy.AllowWrite); len(extra) > 0 {
		roots = dedupeStrings(append(roots, extra...))
	}
	writeRoots := make([]WritableRoot, 0, len(roots))
	for _, root := range roots {
		writeRoots = append(writeRoots, WritableRoot{
			Root:                   root,
			ProtectedMetadataNames: append([]string{}, protectedMetadataNames...),
		})
	}
	return PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            append([]string{}, roots...),
			WriteRoots:           writeRoots,
			DenyRead:             normalizeProfilePaths(policy.DenyRead),
			DenyWrite:            normalizeProfilePaths(policy.DenyWrite),
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{
			Mode:           effectiveNetwork(policy),
			AllowedDomains: normalizeDomains(policy.AllowedDomains),
			DeniedDomains:  normalizeDomains(policy.DeniedDomains),
			ProxyRequired:  effectiveNetwork(policy) == NetworkScoped,
		},
	}
}

func (profile PermissionProfile) RequiresPlatformSandbox() bool {
	if profile.FileSystem.Kind == FileSystemRestricted {
		return true
	}
	if profile.Network.ProxyRequired {
		return true
	}
	return profile.Network.Mode == NetworkDeny || profile.Network.Mode == NetworkScoped
}

func permissionProfileRoots(workspaceRoot string, scope *Scope) []string {
	if scope != nil {
		return scope.Roots()
	}
	if root := normalizeProfilePath(workspaceRoot); root != "" {
		return []string{root}
	}
	return nil
}

func normalizeProfileDirs(entries []string) []string {
	paths := normalizeProfilePaths(entries)
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() && filepath.Dir(path) != path {
			out = append(out, path)
		}
	}
	return out
}

func normalizeProfilePaths(entries []string) []string {
	if len(entries) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		path := normalizeProfilePath(entry)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func normalizeProfilePath(entry string) string {
	trimmed := strings.TrimSpace(entry)
	if trimmed == "" {
		return ""
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		trimmed = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(trimmed[1:], "/"), string(filepath.Separator)))
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return filepath.Clean(absolute)
}
