package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
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
	Runtime    *SandboxRuntime  `json:"runtime,omitempty"`
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
	Mode NetworkMode `json:"mode"`
}

// protectedMetadataNames marks control-plane directories where the app-level
// auto-allow gate (see relativePathTouchesProtectedMetadata in engine.go)
// always requires a prompt for direct file-tool writes (write_file, edit_file,
// apply_patch): hand-editing git's objects/refs/index or Zero's own state
// bypasses git's and Zero's own consistency checks, regardless of subpath.
var protectedMetadataNames = []string{".git", ".zero", ".agents"}

// sandboxFullyProtectedMetadataNames are the metadata directories the OS-level
// sandbox write-denies in full for shell-executed commands. .git is
// deliberately excluded here: git subprocesses (fetch, commit, add, merge,
// pull, stash, ...) need to write objects, refs, the index, and FETCH_HEAD,
// and those writes go through git's own invariants, unlike a raw file-tool
// write. Only .git/hooks (auto-executing scripts) and .git/config (remote
// URLs, credential.helper, core.hooksPath) stay write-denied, via
// gitMetadataWriteCarveouts below.
var sandboxFullyProtectedMetadataNames = []string{".zero", ".agents"}

// gitMetadataWriteCarveouts returns the .git subpaths that stay write-denied
// under the OS-level sandbox even though the rest of .git is writable to git
// subprocesses. Nonexistent paths are harmless no-ops in every backend's
// enforcement (seatbelt regex, bwrap ro-bind, Windows ACL deny entry).
func gitMetadataWriteCarveouts(root string) []string {
	return []string{
		filepath.Join(root, ".git", "hooks"),
		filepath.Join(root, ".git", "config"),
	}
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
	readRoots := permissionProfileReadRoots(workspaceRoot, policy, scope, roots)
	writeRoots := make([]WritableRoot, 0, len(roots))
	for _, root := range roots {
		writeRoots = append(writeRoots, WritableRoot{
			Root:                   root,
			ReadOnlySubpaths:       gitMetadataWriteCarveouts(root),
			ProtectedMetadataNames: append([]string{}, sandboxFullyProtectedMetadataNames...),
		})
	}
	return PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            readRoots,
			WriteRoots:           writeRoots,
			DenyRead:             dedupeStrings(append(normalizeProfilePaths(policy.DenyRead), credentialDenyReadPaths(policy)...)),
			DenyWrite:            normalizeProfilePaths(policy.DenyWrite),
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NormalizeNetworkMode(policy.Network)},
	}
}

func (profile PermissionProfile) RequiresPlatformSandbox() bool {
	if profile.FileSystem.Kind == FileSystemRestricted {
		return true
	}
	return NormalizeNetworkMode(profile.Network.Mode) == NetworkDeny
}

func permissionProfileRoots(workspaceRoot string, scope *Scope) []string {
	if scope != nil {
		return scope.Roots()
	}
	var roots []string
	if root := normalizeProfilePath(workspaceRoot); root != "" {
		roots = append(roots, root)
	}
	roots = append(roots, defaultTempWriteRoots()...)
	return dedupeStrings(roots)
}

func permissionProfileReadRoots(workspaceRoot string, policy Policy, scope *Scope, writeRoots []string) []string {
	// Workspace-write follows the upstream sandbox model: full disk is readable,
	// while writes are narrowed to workspace/extra roots below. This is a
	// deliberate read-all/write-jail posture; callers that must hide secrets use
	// DenyRead to carve them out.
	readRoots := []string{profileRootPath()}
	readRoots = append(readRoots, writeRoots...)
	if scope != nil {
		readRoots = dedupeStrings(append(readRoots, scope.ReadRoots()...))
	} else if root := normalizeProfilePath(workspaceRoot); root != "" {
		readRoots = dedupeStrings(append(readRoots, root))
	}
	if extra := normalizeProfileDirs(policy.AllowRead); len(extra) > 0 {
		readRoots = dedupeStrings(append(readRoots, extra...))
	}
	return dedupeStrings(readRoots)
}

// credentialDenyReadPaths returns default deny-read entries for well-known
// credential stores so sandboxed commands cannot read secrets under the
// read-all workspace posture. This includes tool configuration files that can
// carry credentials and are now discoverable through the preserved caller
// environment. Two deliberate limits:
//
//   - Windows is skipped: a non-empty profile DenyRead switches the Windows
//     runner onto the capability-SID/ACL deny path and away from the
//     WRITE_RESTRICTED token, which the unelevated tier depends on. Revisit
//     once the Windows deny-read model is settled.
//   - A candidate nested under a user-configured AllowRead entry is dropped,
//     so `allowRead: ["~/.aws"]` remains an explicit opt-out.
//
// These are profile-level rules only; they are intentionally NOT merged into
// Policy.DenyRead, whose emptiness gates escalated (unsandboxed) execution and
// must keep reflecting user configuration alone.
func credentialDenyReadPaths(policy Policy) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	// A failed home lookup only drops the home-based candidates; the
	// GOOGLE_APPLICATION_CREDENTIALS target must be protected regardless.
	home, _ := os.UserHomeDir()
	return credentialDenyReadPathsForEnvironment(credentialPathEnvironment{
		Home:              home,
		ConfigHome:        os.Getenv("XDG_CONFIG_HOME"),
		CloudSDKConfig:    os.Getenv("CLOUDSDK_CONFIG"),
		GoogleCredentials: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		NPMUserConfig:     firstNonEmpty(os.Getenv("NPM_CONFIG_USERCONFIG"), os.Getenv("npm_config_userconfig")),
		GHConfigDir:       os.Getenv("GH_CONFIG_DIR"),
		Netrc:             os.Getenv("NETRC"),
		DockerConfigDir:   os.Getenv("DOCKER_CONFIG"),
		KubeConfig:        os.Getenv("KUBECONFIG"),
	}, policy.AllowRead)
}

// credentialDenyReadPathsIn is the pure core of credentialDenyReadPaths,
// separated so tests can exercise it against a synthetic home directory.
func credentialDenyReadPathsIn(home string, googleCredentials string, allowRead []string) []string {
	return credentialDenyReadPathsForEnvironment(credentialPathEnvironment{
		Home:              home,
		GoogleCredentials: googleCredentials,
	}, allowRead)
}

type credentialPathEnvironment struct {
	Home              string
	ConfigHome        string
	CloudSDKConfig    string
	GoogleCredentials string
	NPMUserConfig     string
	GHConfigDir       string
	Netrc             string
	DockerConfigDir   string
	KubeConfig        string
}

func credentialDenyReadPathsForEnvironment(env credentialPathEnvironment, allowRead []string) []string {
	var candidates []string
	home := strings.TrimSpace(env.Home)
	configHome := strings.TrimSpace(env.ConfigHome)
	if configHome == "" && home != "" {
		configHome = filepath.Join(home, ".config")
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".azure"),
		)
	}
	npmUserConfig := strings.TrimSpace(env.NPMUserConfig)
	if npmUserConfig == "" && home != "" {
		npmUserConfig = filepath.Join(home, ".npmrc")
	}
	netrc := strings.TrimSpace(env.Netrc)
	if netrc == "" && home != "" {
		netrc = filepath.Join(home, ".netrc")
	}
	dockerConfigDir := strings.TrimSpace(env.DockerConfigDir)
	if dockerConfigDir == "" && home != "" {
		dockerConfigDir = filepath.Join(home, ".docker")
	}
	candidates = append(candidates, npmUserConfig, netrc)
	if dockerConfigDir != "" {
		candidates = append(candidates, filepath.Join(dockerConfigDir, "config.json"))
	}
	if configHome != "" {
		candidates = append(candidates,
			filepath.Join(configHome, "zero"),
		)
	}
	cloudSDKConfig := strings.TrimSpace(env.CloudSDKConfig)
	if cloudSDKConfig == "" && configHome != "" {
		cloudSDKConfig = filepath.Join(configHome, "gcloud")
	}
	if cloudSDKConfig != "" {
		candidates = append(candidates, cloudSDKConfig)
	}
	ghConfigDir := strings.TrimSpace(env.GHConfigDir)
	if ghConfigDir == "" && configHome != "" {
		ghConfigDir = filepath.Join(configHome, "gh")
	}
	if ghConfigDir != "" {
		candidates = append(candidates, filepath.Join(ghConfigDir, "hosts.yml"))
	}
	if target := strings.TrimSpace(env.GoogleCredentials); target != "" {
		candidates = append(candidates, target)
	}
	kubeConfig := strings.TrimSpace(env.KubeConfig)
	if kubeConfig == "" && home != "" {
		kubeConfig = filepath.Join(home, ".kube", "config")
	}
	for _, path := range filepath.SplitList(kubeConfig) {
		if path = strings.TrimSpace(path); path != "" {
			candidates = append(candidates, path)
		}
	}
	allowRoots := normalizeProfilePaths(allowRead)
	out := make([]string, 0, len(candidates))
	for _, path := range normalizeProfilePaths(candidates) {
		// Only stores that actually exist on this host need a deny rule.
		if _, err := os.Stat(path); err != nil {
			continue
		}
		reincluded := false
		for _, allow := range allowRoots {
			if pathWithinRoot(allow, path) {
				reincluded = true
				break
			}
		}
		if !reincluded {
			out = append(out, path)
		}
	}
	return out
}

// userGitConfigReadPaths returns the user's global git config FILES so a
// sandboxed git can read identity and config (user.name/email, aliases) instead
// of failing with "unable to access ~/.gitconfig". On macOS, where reads are
// allow-listed, granting only these files avoids exposing the surrounding
// configuration directory. Linux uses a read-all profile with explicit
// credential deny rules. The paths are granted at the macOS seatbelt read rule
// rather than the cross-platform PermissionProfile so HOME-dependent paths
// don't leak into the platform-agnostic policy snapshot.
func userGitConfigReadPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".gitconfig"),
		filepath.Join(home, ".config", "git", "config"),
	}
}

func profileRootPath() string {
	return filepath.Clean(string(filepath.Separator))
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
