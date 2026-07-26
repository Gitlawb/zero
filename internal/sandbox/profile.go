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
	Kind       FileSystemPolicyKind `json:"kind"`
	ReadRoots  []string             `json:"readRoots,omitempty"`
	WriteRoots []WritableRoot       `json:"writeRoots,omitempty"`
	DenyRead   []string             `json:"denyRead,omitempty"`
	// DenyReadIfExists contains best-effort baseline paths. Backends with
	// path-based policies can protect future paths; mount-based Linux only
	// masks entries that exist when the namespace is assembled.
	DenyReadIfExists []string `json:"denyReadIfExists,omitempty"`
	// DenyReadCarveouts are subtrees that stay readable INSIDE a denied root.
	// They exist so a directory-level credential deny can also cover the files
	// a store publishes (arbitrary temporary names, files created later in the
	// session) without hiding the supported non-secret subtrees that live in the
	// same directory — Zero's user plugin/specialist/command roots, whose
	// commands and scripts are themselves executed through the sandbox.
	DenyReadCarveouts []string `json:"denyReadCarveouts,omitempty"`
	// EnsureDenyReadDirs are directories Zero owns that a mount-based backend
	// may create (0700) so a mask exists for them. bubblewrap cannot mount over
	// a path that is absent when the namespace is assembled, so without this a
	// store created mid-session would be readable by an already-running sandbox.
	EnsureDenyReadDirs   []string `json:"ensureDenyReadDirs,omitempty"`
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
// subprocesses. Nonexistent paths are harmless no-ops in every backend's
// enforcement (seatbelt regex, bwrap ro-bind, Windows ACL deny entry).
func gitMetadataWriteCarveouts(root string) []string {
	return []string{
		filepath.Join(root, ".git", "hooks"),
		filepath.Join(root, ".git", "config"),
	}
}

func PermissionProfileFromPolicy(workspaceRoot string, policy Policy, scope *Scope) PermissionProfile {
	return permissionProfileFromPolicy(workspaceRoot, policy, scope, "", nil)
}

func permissionProfileFromPolicy(workspaceRoot string, policy Policy, scope *Scope, credentialCommandDir string, credentialEnv []string) PermissionProfile {
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
	userDenyRead := normalizeProfilePaths(policy.DenyRead)
	credentials := credentialDenyReadPaths(policy, credentialCommandDir, credentialEnv)
	// A carveout re-includes reads, so it must never reach inside a path the USER
	// denied: their configuration outranks Zero's automatic baseline.
	credentials.Carveouts = pathsOutsideRoots(credentials.Carveouts, userDenyRead)
	return PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            readRoots,
			WriteRoots:           writeRoots,
			DenyRead:             userDenyRead,
			DenyReadIfExists:     credentials.Paths,
			DenyReadCarveouts:    credentials.Carveouts,
			EnsureDenyReadDirs:   credentials.EnsureDirs,
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

// credentialDenyPaths is the credential baseline a profile derives from the
// environment: the paths to deny reads on, the non-secret subtrees that stay
// readable inside them, and the Zero-owned directories a mount-based backend
// may create so its mask actually exists.
type credentialDenyPaths struct {
	Paths      []string
	Carveouts  []string
	EnsureDirs []string
}

// credentialDenyReadPaths returns default deny-read entries for well-known
// cloud credential stores, the file GOOGLE_APPLICATION_CREDENTIALS points to,
// and Zero's own config/credential/token directory so sandboxed commands
// cannot read secrets under the read-all workspace posture. Four deliberate
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
//     mount-based Linux masks a path only if it exists when the namespace is
//     assembled, which is why the directories Zero owns are also reported as
//     EnsureDirs (the backend creates them, so the mask is always present) and
//     why third-party stores such as ~/.aws stay best-effort there.
//   - Zero's own config directory is denied WHOLE, with the supported
//     non-secret subtrees carved back out. Only a directory-level rule covers
//     the temporary names its stores publish through and the files a concurrent
//     login creates mid-session; the carveouts keep the user plugin,
//     specialist, and command roots readable, since those are executed through
//     the sandbox.
//
// These are profile-level rules only; they are intentionally NOT merged into
// Policy.DenyRead, whose emptiness gates escalated (unsandboxed) execution and
// must keep reflecting user configuration alone.
func credentialDenyReadPaths(policy Policy, commandDir string, commandEnv []string) credentialDenyPaths {
	if runtime.GOOS == "windows" {
		return credentialDenyPaths{}
	}
	options := credentialPathOptionsFromEnvironment(commandDir, commandEnv)
	return credentialDenyReadPathsIn(options, policy.AllowRead)
}

// zeroConfigReadCarveoutNames are the supported non-secret subtrees of
// <configDir>/zero. Their contents are extension code and prompts that a
// sandboxed command legitimately executes or reads (a user plugin's tool
// command lives below the plugin root), so the credential deny must not hide
// them. Nothing here holds a secret: credentials, tokens, and config live in
// files directly under <configDir>/zero, not in these subtrees.
var zeroConfigReadCarveoutNames = []string{"plugins", "specialists", "commands"}

// credentialOverrideBaseDirs returns the directories a RELATIVE credential
// override is resolved against, most authoritative first.
//
// The stores themselves call filepath.Abs (oauth.ResolveStorePath,
// mcp.ResolveTokenStorePath), i.e. they resolve against the Zero PROCESS
// working directory, so that is the path that must be denied. The command
// directory is kept as a second candidate because a sandboxed child (including
// a nested Zero) resolves the inherited value against its own cwd instead.
// Denying both costs two rules and closes the mismatch in either direction.
func credentialOverrideBaseDirs(commandDir string) []string {
	var dirs []string
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		dirs = append(dirs, filepath.Clean(cwd))
	}
	if dir := strings.TrimSpace(commandDir); dir != "" {
		dirs = append(dirs, filepath.Clean(dir))
	}
	return dedupeStrings(dirs)
}

func credentialPathOptionsFromEnvironment(commandDir string, commandEnv []string) credentialPathOptions {
	env := commandEnv
	if env == nil {
		env = os.Environ()
	}
	baseDirs := credentialOverrideBaseDirs(commandDir)
	homes := resolveCredentialOverridePaths(credentialEnvValue(env, "HOME"), baseDirs)
	if len(homes) == 0 {
		homes = resolveCredentialOverridePaths(credentialEnvValue(env, "USERPROFILE"), baseDirs)
	}
	if len(homes) == 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			homes = resolveCredentialOverridePaths(home, baseDirs)
		}
	}
	configDirs := resolveCredentialOverridePaths(credentialEnvValue(env, "XDG_CONFIG_HOME"), baseDirs)
	if len(configDirs) == 0 {
		for _, home := range homes {
			configDirs = append(configDirs, filepath.Join(home, ".config"))
		}
	}
	return credentialPathOptions{
		Homes:             homes,
		GoogleCredentials: resolveCredentialOverridePaths(credentialEnvValue(env, "GOOGLE_APPLICATION_CREDENTIALS"), baseDirs),
		ZeroConfigDirs:    dedupeStrings(configDirs),
		OAuthTokens:       resolveCredentialOverridePaths(credentialEnvValue(env, "ZERO_OAUTH_TOKENS_PATH"), baseDirs),
		MCPOAuthTokens:    resolveCredentialOverridePaths(credentialEnvValue(env, "ZERO_MCP_OAUTH_TOKENS_PATH"), baseDirs),
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
	Homes             []string
	GoogleCredentials []string
	ZeroConfigDirs    []string
	OAuthTokens       []string
	MCPOAuthTokens    []string
}

// credentialDenyReadPathsIn is the pure core of credentialDenyReadPaths,
// separated so tests can exercise it against a synthetic home directory.
func credentialDenyReadPathsIn(options credentialPathOptions, allowRead []string) credentialDenyPaths {
	var candidates []string
	var carveouts []string
	var ensureDirs []string
	for _, home := range options.Homes {
		if strings.TrimSpace(home) == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".config", "gcloud"),
			filepath.Join(home, ".azure"),
		)
	}
	candidates = append(candidates, options.GoogleCredentials...)
	for _, configDir := range options.ZeroConfigDirs {
		if strings.TrimSpace(configDir) == "" {
			continue
		}
		// Deny the whole directory rather than an itemized file list: Zero's
		// credential, token, and config stores publish through temporary siblings
		// before an atomic rename, the legacy MCP store leaves a
		// mcp-oauth-tokens.json.migrated backup behind, and a concurrent login can
		// add a store that did not exist when this profile was built. Only a
		// directory rule covers all three. Zero owns this directory, so it is also
		// an EnsureDir: bubblewrap cannot mask a path that is absent when the
		// namespace is assembled.
		zeroDir := filepath.Join(configDir, "zero")
		candidates = append(candidates, zeroDir)
		ensureDirs = append(ensureDirs, zeroDir)
		for _, name := range zeroConfigReadCarveoutNames {
			carveouts = append(carveouts, filepath.Join(zeroDir, name))
		}
	}
	for _, tokenPath := range options.OAuthTokens {
		candidates = append(candidates, credentialTokenStorePaths(tokenPath)...)
		ensureDirs = append(ensureDirs, credentialPublicationDirs(tokenPath)...)
	}
	for _, tokenPath := range options.MCPOAuthTokens {
		candidates = append(candidates, credentialTokenStorePaths(tokenPath)...)
		// The legacy store renames itself aside after importing into the unified
		// store, leaving a readable copy of the same tokens behind.
		candidates = append(candidates, tokenPath+".migrated")
		ensureDirs = append(ensureDirs, credentialPublicationDirs(tokenPath)...)
	}
	allowRoots := normalizeProfilePaths(allowRead)
	out := make([]string, 0, len(candidates))
	for _, path := range normalizeProfilePaths(candidates) {
		if credentialPathReincluded(allowRoots, path) {
			continue
		}
		out = append(out, path)
	}
	return credentialDenyPaths{
		Paths:      out,
		Carveouts:  credentialCarveoutPaths(out, carveouts),
		EnsureDirs: credentialEnsureDirs(out, ensureDirs),
	}
}

// credentialTokenStorePaths returns the deny entries for one token-store path:
// the store, its lock siblings, its encryption-key sibling, and the directory
// it publishes new contents through. The names are fixed so an override outside
// Zero's config directory is protected by exact rules instead of hiding an
// arbitrary parent such as the workspace or /tmp.
func credentialTokenStorePaths(tokenPath string) []string {
	if strings.TrimSpace(tokenPath) == "" {
		return nil
	}
	paths := []string{
		tokenPath,
		tokenPath + ".lockfile",
		tokenPath + ".secret",
		tokenPath + ".secret.lock",
		// Left behind by a Zero older than the publication directories below.
		tokenPath + ".tmp",
		tokenPath + ".secret.tmp",
	}
	return append(paths, credentialPublicationDirs(tokenPath)...)
}

// credentialPublicationDirs are the per-store directories the OAuth stores
// create their randomly-named temporary file in (see oauth.PublicationDir) —
// one for the token blob, one for its encryption key. The directory NAME is
// derived from the store path so the profile can deny it up front, while the
// random name inside it is what keeps a sandboxed same-user process from
// opening, or renaming away, the file that briefly holds the plaintext.
func credentialPublicationDirs(tokenPath string) []string {
	if strings.TrimSpace(tokenPath) == "" {
		return nil
	}
	return []string{
		tokenPath + credentialPublicationDirSuffix,
		tokenPath + ".secret" + credentialPublicationDirSuffix,
	}
}

// credentialPublicationDirSuffix mirrors oauth.PublicationDirSuffix, duplicated
// because internal/mcp depends on this package and internal/oauth must stay
// importable from both.
const credentialPublicationDirSuffix = ".publish"

// pathsOutsideRoots drops every path that lies within one of roots.
func pathsOutsideRoots(paths []string, roots []string) []string {
	if len(paths) == 0 || len(roots) == 0 {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if credentialPathReincluded(roots, path) {
			continue
		}
		out = append(out, path)
	}
	return out
}

func credentialPathReincluded(allowRoots []string, path string) bool {
	for _, allow := range allowRoots {
		if pathWithinRoot(allow, path) {
			return true
		}
	}
	return false
}

// credentialCarveoutPaths keeps only the carveouts that sit inside a path that
// is actually denied, so an AllowRead opt-out that removed the deny does not
// leave a stray allow-back rule behind.
func credentialCarveoutPaths(denied []string, carveouts []string) []string {
	if len(carveouts) == 0 {
		return nil
	}
	out := make([]string, 0, len(carveouts))
	for _, carveout := range normalizeProfilePaths(carveouts) {
		for _, deny := range denied {
			if carveout != deny && pathWithinRoot(deny, carveout) {
				out = append(out, carveout)
				break
			}
		}
	}
	return dedupeStrings(out)
}

// credentialEnsureDirs keeps only the directories that are still denied, so the
// sandbox never creates a directory it is not going to mask.
func credentialEnsureDirs(denied []string, ensureDirs []string) []string {
	if len(ensureDirs) == 0 {
		return nil
	}
	out := make([]string, 0, len(ensureDirs))
	for _, dir := range normalizeProfilePaths(ensureDirs) {
		for _, deny := range denied {
			if dir == deny {
				out = append(out, dir)
				break
			}
		}
	}
	return dedupeStrings(out)
}

// resolveCredentialOverridePaths mirrors the token stores' own override
// resolution (oauth.ResolveStorePath, mcp.ResolveTokenStorePath — reimplemented
// here rather than imported because internal/mcp depends on this package): the
// value is used literally, NOT tilde-expanded the way normalizeProfilePath
// expands other candidates. Using normalizeProfilePath here would derive a deny
// path that doesn't match where the store actually writes — e.g.
// ZERO_OAUTH_TOKENS_PATH=~/x resolves to <cwd>/~/x on disk (the store never
// expands "~"), but normalizeProfilePath would deny $HOME/x instead, leaving
// the real file unprotected.
//
// A relative value yields one candidate per base dir (see
// credentialOverrideBaseDirs) because the process that resolves it is not
// necessarily the one that writes the store.
func resolveCredentialOverridePaths(override string, baseDirs []string) []string {
	override = strings.TrimSpace(override)
	if override == "" {
		return nil
	}
	if filepath.IsAbs(override) {
		return []string{filepath.Clean(override)}
	}
	out := make([]string, 0, len(baseDirs))
	for _, baseDir := range baseDirs {
		if strings.TrimSpace(baseDir) == "" {
			continue
		}
		out = append(out, filepath.Clean(filepath.Join(baseDir, override)))
	}
	return dedupeStrings(out)
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
