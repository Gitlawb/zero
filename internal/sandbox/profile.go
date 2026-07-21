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
}

type FileSystemPolicy struct {
	Kind       FileSystemPolicyKind `json:"kind"`
	ReadRoots  []string             `json:"readRoots,omitempty"`
	WriteRoots []WritableRoot       `json:"writeRoots,omitempty"`
	DenyRead   []string             `json:"denyRead,omitempty"`
	// DenyReadIfExists contains best-effort baseline paths. Backends with
	// path-based policies can protect future paths; mount-based Linux only
	// masks entries that exist when the namespace is assembled.
	DenyReadIfExists     []string `json:"denyReadIfExists,omitempty"`
	DenyWrite            []string `json:"denyWrite,omitempty"`
	IncludePlatformRoots bool     `json:"includePlatformRoots,omitempty"`
	AllowTemp            bool     `json:"allowTemp,omitempty"`
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
// subprocesses. Backends enforce paths that exist when the sandbox starts;
// mount-based Linux cannot protect an absent child beneath a writable bind
// without either creating it on the host or making its parent read-only, so an
// absent baseline remains an acknowledged backend limitation rather than
// preventing ordinary non-Git workspaces from launching.
func gitMetadataWriteCarveouts(root string) []string {
	return []string{
		filepath.Join(root, ".git", "hooks"),
		filepath.Join(root, ".git", "config"),
	}
}

func DefaultPermissionProfile(workspaceRoot string) PermissionProfile {
	return PermissionProfileFromPolicy(workspaceRoot, DefaultPolicy(), nil)
}

func PermissionProfileFromPolicy(workspaceRoot string, policy Policy, scope *Scope) PermissionProfile {
	baseDir, _ := os.Getwd()
	return permissionProfileFromPolicy(workspaceRoot, policy, scope, baseDir, nil)
}

func permissionProfileFromPolicy(workspaceRoot string, policy Policy, scope *Scope, credentialBaseDir string, credentialEnv []string) PermissionProfile {
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
	tempRoots := defaultTempWriteRoots()
	for _, root := range roots {
		writable := WritableRoot{Root: root}
		if !profilePathInList(tempRoots, root) {
			writable.ReadOnlySubpaths = gitMetadataWriteCarveouts(root)
			writable.ProtectedMetadataNames = append([]string{}, sandboxFullyProtectedMetadataNames...)
		}
		writeRoots = append(writeRoots, writable)
	}
	return PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            readRoots,
			WriteRoots:           writeRoots,
			DenyRead:             normalizeProfilePaths(policy.DenyRead),
			DenyReadIfExists:     credentialDenyReadPaths(policy, credentialBaseDir, credentialEnv),
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
// cloud credential stores, the file GOOGLE_APPLICATION_CREDENTIALS points to,
// and Zero's own config/credential/token directory so sandboxed commands
// cannot read secrets under the read-all workspace posture. Three deliberate
// limits:
//
//   - Windows is skipped: a non-empty profile DenyRead switches the Windows
//     runner onto the capability-SID/ACL deny path and away from the
//     WRITE_RESTRICTED token, which the unelevated tier depends on. Revisit
//     once the Windows deny-read model is settled.
//   - A candidate nested under a user-configured AllowRead entry is dropped,
//     so `allowRead: ["~/.aws"]` remains an explicit opt-out.
//   - Candidates are emitted whether or not they currently exist on disk.
//     Pathname-policy backends such as Seatbelt can enforce future paths;
//     mount-based Linux masks only baseline paths that exist when the namespace
//     is assembled so fresh homes remain usable.
//
// These are profile-level rules only; they are intentionally NOT merged into
// Policy.DenyRead, whose emptiness gates escalated (unsandboxed) execution and
// must keep reflecting user configuration alone.
func credentialDenyReadPaths(policy Policy, baseDir string, commandEnv []string) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	options := credentialPathOptionsFromEnvironment(baseDir, commandEnv)
	return credentialDenyReadPathsIn(options, policy.AllowRead)
}

func profilePathInList(paths []string, want string) bool {
	want = filepath.Clean(want)
	for _, path := range paths {
		if filepath.Clean(path) == want {
			return true
		}
	}
	return false
}

func credentialPathOptionsFromEnvironment(baseDir string, commandEnv []string) credentialPathOptions {
	env := commandEnv
	if env == nil {
		env = os.Environ()
	}
	home := strings.TrimSpace(credentialEnvValue(env, "HOME"))
	if home == "" {
		home = strings.TrimSpace(credentialEnvValue(env, "USERPROFILE"))
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	home = resolveCredentialOverridePath(home, baseDir)
	configDir := strings.TrimSpace(credentialEnvValue(env, "XDG_CONFIG_HOME"))
	if configDir == "" && home != "" {
		configDir = filepath.Join(home, ".config")
	} else {
		configDir = resolveCredentialOverridePath(configDir, baseDir)
	}
	return credentialPathOptions{
		Home:              home,
		GoogleCredentials: resolveCredentialOverridePath(credentialEnvValue(env, "GOOGLE_APPLICATION_CREDENTIALS"), baseDir),
		ZeroConfigDir:     configDir,
		OAuthTokens:       resolveCredentialOverridePath(credentialEnvValue(env, "ZERO_OAUTH_TOKENS_PATH"), baseDir),
		MCPOAuthTokens:    resolveCredentialOverridePath(credentialEnvValue(env, "ZERO_MCP_OAUTH_TOKENS_PATH"), baseDir),
	}
}

func credentialEnvValue(env []string, key string) string {
	value := ""
	for _, entry := range env {
		name, candidate, ok := strings.Cut(entry, "=")
		if ok && name == key {
			value = candidate
		}
	}
	return value
}

type credentialPathOptions struct {
	Home              string
	GoogleCredentials string
	ZeroConfigDir     string
	OAuthTokens       string
	MCPOAuthTokens    string
}

// credentialDenyReadPathsIn is the pure core of credentialDenyReadPaths,
// separated so tests can exercise it against a synthetic home directory.
func credentialDenyReadPathsIn(options credentialPathOptions, allowRead []string) []string {
	var candidates []string
	if home := strings.TrimSpace(options.Home); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".config", "gcloud"),
			filepath.Join(home, ".azure"),
		)
	}
	if target := strings.TrimSpace(options.GoogleCredentials); target != "" {
		candidates = append(candidates, target)
	}
	if configDir := strings.TrimSpace(options.ZeroConfigDir); configDir != "" {
		// Deny the whole directory rather than an itemized file list. Zero's
		// credential/token/config stores each publish through a randomly-named
		// sibling before an atomic rename (oauth-tokens.json.tmp-<pid>-<nanos>,
		// credentials.{enc,json}.*.tmp, *.secret.*.tmp, .zero-config-*.tmp), and
		// the legacy MCP token store leaves a mcp-oauth-tokens.json.migrated
		// backup behind after importing it — an itemized list can never keep up
		// with those names. Nothing else has a legitimate reason to live here.
		candidates = append(candidates, filepath.Join(configDir, "zero"))
	}
	if tokenPath := strings.TrimSpace(options.OAuthTokens); tokenPath != "" {
		// OAuth uses fixed same-directory publication paths so exact denies do not
		// have to hide an arbitrary parent such as the workspace or /tmp.
		candidates = append(candidates,
			tokenPath,
			tokenPath+".tmp",
			tokenPath+".lockfile",
			tokenPath+".secret",
			tokenPath+".secret.tmp",
			tokenPath+".secret.lock",
		)
	}
	if tokenPath := strings.TrimSpace(options.MCPOAuthTokens); tokenPath != "" {
		candidates = append(candidates,
			tokenPath,
			tokenPath+".tmp",
			tokenPath+".lockfile",
			tokenPath+".secret",
			tokenPath+".secret.tmp",
			tokenPath+".secret.lock",
			tokenPath+".migrated",
		)
	}
	allowRoots := normalizeProfilePaths(allowRead)
	out := make([]string, 0, len(candidates))
	for _, path := range normalizeProfilePaths(candidates) {
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

// resolveCredentialOverridePath mirrors the token stores' own override
// resolution (oauth.ResolveStorePath, mcp.ResolveTokenStorePath — duplicated
// here rather than imported, the same tradeoff zeroUserConfigDir makes,
// because internal/mcp depends on this package): a relative override is
// resolved literally against the process working directory, NOT tilde-
// expanded the way normalizeProfilePath expands other candidates. Using
// normalizeProfilePath here would derive a deny path that doesn't match
// where the store actually writes — e.g. ZERO_OAUTH_TOKENS_PATH=~/x resolves
// to <cwd>/~/x on disk (the store never expands "~"), but normalizeProfilePath
// would deny $HOME/x instead, leaving the real file unprotected.
func resolveCredentialOverridePath(override string, baseDir string) string {
	override = strings.TrimSpace(override)
	if override == "" {
		return ""
	}
	if filepath.IsAbs(override) {
		return filepath.Clean(override)
	}
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return ""
		}
	}
	return filepath.Clean(filepath.Join(baseDir, override))
}

// userGitConfigReadPaths returns the user's global git config FILES so a
// sandboxed git can read identity and config (user.name/email, aliases) instead
// of failing with "unable to access ~/.gitconfig". It is deliberately the config
// files only — not the ~/.config/git directory, which can hold an XDG credential
// store — so credentials and the rest of HOME stay unreadable. Granted at the
// macOS-seatbelt read rule (not the cross-platform PermissionProfile) so the
// HOME-dependent paths don't leak into the platform-agnostic policy snapshot.
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
