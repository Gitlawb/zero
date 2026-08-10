package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// This file implements the fine-grained AllowRead/DenyRead/AllowWrite/DenyWrite
// path lists (Policy fields). They layer ON TOP of the workspace + Scope guards
// and never bypass them: AllowRead only re-includes inside a DenyRead carve-out,
// AllowWrite is consulted only after the workspace/Scope guard already denied a
// write, and every match is symlink-resolved so a symlink prefix cannot evade a
// deny or sneak past an allow. All lists default empty, so an unconfigured policy
// behaves exactly as before.

// resolvePolicyPath home-expands, makes absolute, and symlink-resolves a single
// policy path entry. ok is false for a blank entry or one that does not exist
// (EvalSymlinks requires existence) so a bogus entry is dropped — a non-existent
// deny protects nothing and a non-existent allow grants nothing.
func resolvePolicyPath(entry string) (string, bool) {
	trimmed := strings.TrimSpace(entry)
	if trimmed == "" {
		return "", false
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		trimmed = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(trimmed[1:], "/"), string(filepath.Separator)))
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", false
	}
	return resolved, true
}

// These name the alternative sources of the remote bridge's bearer token. They
// are duplicated from internal/daemon/remote (which cannot be imported here)
// exactly like the copies scrubSensitiveEnv keeps.
const (
	daemonRemoteTokenEnv     = "ZERO_DAEMON_REMOTE_TOKEN"
	daemonRemoteTokenFileEnv = "ZERO_DAEMON_REMOTE_TOKEN_FILE"
)

// selectedDaemonRemoteTokenFile returns the token-file pointer only when the
// daemon would use it. TokenFromEnv gives the inline token precedence, so an
// inherited file pointer is not a credential when both variables are set.
//
// The pointer is used verbatim, mirroring remote.TokenFilePathFromEnv: the
// daemon reads whatever bytes the variable names, so trimming whitespace here
// would leave a token file whose name begins or ends with a space unprotected
// while the daemon still reads it. Only a value that is entirely whitespace
// counts as unset.
func selectedDaemonRemoteTokenFile() string {
	if strings.TrimSpace(os.Getenv(daemonRemoteTokenEnv)) != "" {
		return ""
	}
	configured := os.Getenv(daemonRemoteTokenFileEnv)
	if strings.TrimSpace(configured) == "" {
		return ""
	}
	return configured
}

// protectedCredentialPaths returns credential files that Zero's own in-process
// file tools must never read or modify, independent of Policy.
//
// This is deliberately separate from credentialDenyReadPaths, which only shapes
// the OS sandbox profile for wrapped shell commands: read_file resolves scoped
// paths itself, and grep/glob build their exclusions from the policy, so a
// profile-only rule leaves the in-process tool boundary open. It is also
// separate from Policy.DenyRead, whose emptiness gates escalated (unsandboxed)
// execution and must keep reflecting user configuration alone.
//
// Entries here are NOT re-includable through AllowRead/AllowWrite or a
// session/turn permission profile: the bridge bearer token grants control of
// this daemon, so a remote-controlled agent must not be able to read it, nor
// replace it to hijack the next bridge start, even when the file sits inside
// its own session workspace. Unlike the profile list this applies on Windows
// too, where filesystem deny-read has no sandbox representation (#662).
//
// Both the selected pathname and the target it currently resolves to are
// protected: `zero daemon serve-remote` canonicalizes the value it selects (see
// remote.CanonicalizeTokenFileEnv), but a symlinked selected value inherited
// from elsewhere must not leave the link replaceable.
func protectedCredentialPaths() []string {
	// os.ReadFile — the daemon's own reader — treats the value literally, so a
	// relative path resolves against the working directory and a leading "~" is
	// NOT expanded. resolvePolicyPath would expand it and protect the wrong file.
	return daemonTokenDenyPaths(selectedDaemonRemoteTokenFile())
}

// daemonTokenDenyPaths is the shared pathname authority for both the
// in-process boundary and OS sandbox profiles. Filename whitespace is data:
// only an entirely blank setting is unset, matching the daemon reader.
func daemonTokenDenyPaths(configured string) []string {
	if strings.TrimSpace(configured) == "" {
		return nil
	}
	absolute, err := filepath.Abs(configured)
	if err != nil {
		return nil
	}
	paths := []string{absolute}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil && resolved != absolute {
		paths = append(paths, resolved)
	}
	return dedupeStrings(paths)
}

// protectedCredentialPathBlock returns the block for the first requested path
// that targets a protected credential file, or nil. It exists for the callers
// that bypass validatePathWithPolicy — currently the ModeDisabled short-circuit,
// where the exclusion still applies because it is not policy-derived.
//
// Only the side effects that name a path are covered. SideEffectShell is not,
// and cannot be: a shell request carries a command line, not a file path, so
// there is nothing here to compare against the token. Shell is confined by the
// OS wrapper instead — which under ModeDisabled does not exist. See the
// ModeDisabled short-circuit in Engine.Evaluate for what that boundary means.
func protectedCredentialPathBlock(request Request, workspaceRoot string) *pathBlock {
	switch request.SideEffect {
	case SideEffectRead, SideEffectWrite, SideEffectOutOfWorkspace:
	default:
		return nil
	}
	protected := protectedCredentialPaths()
	if len(protected) == 0 {
		return nil
	}
	verb := "readable"
	if request.SideEffect != SideEffectRead {
		verb = "writable"
	}
	for _, path := range requestPaths(request) {
		if protectedPathDenied(protected, workspaceRoot, path) {
			return &pathBlock{
				Code:   BlockDenied,
				Path:   path,
				Reason: path + " holds the remote bridge token and is never " + verb,
			}
		}
	}
	return nil
}

// protectedPathDenied reports whether path targets one of the protected
// credential files. There is no allow-list consultation by design.
func protectedPathDenied(protected []string, workspaceRoot, path string) bool {
	if len(protected) == 0 {
		return false
	}
	for _, entry := range protected {
		if pathUnderProtectedRoot(path, entry, workspaceRoot) {
			return true
		}
	}

	// Keep the lexical check above: in particular, it protects a configured
	// pathname even when the file is absent, preventing its replacement. For an
	// existing request, also compare the object reached by the filesystem. This
	// closes aliases created after the token path was selected: EvalSymlinks
	// catches symbolic links, while SameFile catches hard links (and any other
	// platform-specific names for the same file).
	//
	// This inode-level closure is specific to Zero's in-process tools, which see
	// every requested path before opening it. The OS layer is pathname-based and
	// stays that way: seatbelt and Bubblewrap rules name paths, so a sandboxed
	// shell on macOS can still `ln <token> alias && cat alias` — a hard link is a
	// second name for the same inode, and no path-based rule covers a name that
	// did not exist when the profile was built. That is the same model a
	// user-configured DenyRead has always had, deliberately: an aliasing defense
	// at the OS layer would have to resolve every path at open time, which is not
	// something either backend's policy language expresses. Shell access to a
	// host running a remote bridge is therefore access to the token, and the
	// protection here is the in-process boundary plus the pathname deny rules,
	// not an inode-tight OS guarantee.
	abs := path
	if !filepath.IsAbs(abs) {
		if workspaceRoot == "" {
			return false
		}
		abs = filepath.Join(workspaceRoot, abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return false
	}
	requestInfo, err := os.Stat(resolved)
	if err != nil {
		return false
	}
	for _, entry := range protected {
		protectedInfo, err := os.Stat(entry)
		if err == nil && os.SameFile(requestInfo, protectedInfo) {
			return true
		}
	}
	return false
}

// protectedPathFoldsCase reports whether a case-variant spelling of a path opens
// the SAME file on this platform, so the protected-credential comparison must
// fold case to stay closed.
//
// pathWithinRoot ends in filepath.Rel, which ALREADY folds case on Windows
// (path_windows.go's sameWord uses strings.EqualFold) but never on Unix. macOS
// volumes are case-insensitive by default (APFS), so without folding a request
// for `.../Bridge-Token` misses a protected `.../bridge-token` while the OS
// opens the very same bearer-token file. Windows is listed too so the guarantee
// does not silently depend on a filepath.Rel implementation detail.
//
// Case-sensitive outliers (a case-sensitive APFS volume) only make this
// over-deny a genuinely different file that happens to differ by case alone,
// which is the safe direction for a credential the sandbox must never expose.
func protectedPathFoldsCase() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// pathUnderProtectedRoot is pathUnderPolicyRoot for the automatic credential
// exclusions: identical anchoring and symlink normalization, plus the platform's
// filesystem case semantics. Only the final containment comparison folds — the
// normalization above it keeps operating on the path as spelled, so symlink
// resolution is unaffected.
func pathUnderProtectedRoot(requestedPath, root, workspaceRoot string) bool {
	normalized, ok := normalizePathForPolicyRoot(requestedPath, root, workspaceRoot)
	if !ok {
		return false
	}
	if pathWithinRoot(root, normalized) {
		return true
	}
	if !protectedPathFoldsCase() {
		return false
	}
	return pathWithinRoot(strings.ToLower(root), strings.ToLower(normalized))
}

// resolvePolicyPaths resolves and de-duplicates a list of policy path entries,
// dropping blanks and non-existent entries. Files and directories are both kept
// (a DenyRead/DenyWrite entry may target a single sensitive file).
func resolvePolicyPaths(entries []string) []string {
	if len(entries) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		resolved, ok := resolvePolicyPath(entry)
		if !ok {
			continue
		}
		if _, dup := seen[resolved]; dup {
			continue
		}
		seen[resolved] = struct{}{}
		out = append(out, resolved)
	}
	return out
}

// resolveWriteRootPaths is resolvePolicyPaths restricted to existing directories
// that are not the filesystem root — the only valid targets for an OS write bind
// and for an AllowWrite grant root.
func resolveWriteRootPaths(entries []string) []string {
	resolved := resolvePolicyPaths(entries)
	if len(resolved) == 0 {
		return nil
	}
	out := make([]string, 0, len(resolved))
	for _, path := range resolved {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		if filepath.Dir(path) == path {
			continue // refuse the filesystem root as a write root
		}
		out = append(out, path)
	}
	return out
}

// pathUnderPolicyRoot reports whether requestedPath lies within root. A relative
// requestedPath is resolved against workspaceRoot; the portion of an absolute
// path outside root is symlink-resolved (via NormalizePrefixForRoot) so a
// symlink prefix cannot evade the match. root must be an already-resolved
// absolute path.
func pathUnderPolicyRoot(requestedPath, root, workspaceRoot string) bool {
	normalized, ok := normalizePathForPolicyRoot(requestedPath, root, workspaceRoot)
	if !ok {
		return false
	}
	return pathWithinRoot(root, normalized)
}

// normalizePathForPolicyRoot anchors requestedPath (a relative one against
// workspaceRoot) and symlink-normalizes the portion outside root, yielding the
// path pathUnderPolicyRoot compares. ok is false when there is nothing to
// compare against: a blank root, or a relative path with no workspace root.
func normalizePathForPolicyRoot(requestedPath, root, workspaceRoot string) (string, bool) {
	if root == "" {
		return "", false
	}
	abs := requestedPath
	if !filepath.IsAbs(abs) {
		if workspaceRoot == "" {
			return "", false
		}
		abs = filepath.Join(workspaceRoot, abs)
	}
	return NormalizePrefixForRoot(abs, root), true
}

// readDenied reports whether path is excluded by the DenyRead list with no
// more-specific AllowRead re-inclusion. "More specific" means an AllowRead entry
// nested inside the matched DenyRead entry — that subtree is read back in while
// the rest of the denied tree stays blocked. It resolves the policy entries on
// each call, so it suits a one-shot check (the Evaluate gate). A search walk that
// checks many paths should use ReadExclusions, which resolves the entries once.
func readDenied(policy Policy, workspaceRoot, path string) bool {
	return readDeniedResolved(workspaceRoot, resolvePolicyPaths(policy.DenyRead), resolvePolicyPaths(policy.AllowRead), path)
}

// readDeniedResolved is readDenied operating on ALREADY-resolved deny/allow roots,
// so a caller that resolved the policy entries once can reuse them across many
// path checks instead of re-running Abs/EvalSymlinks per path.
func readDeniedResolved(workspaceRoot string, denyRoots, allowRoots []string, path string) bool {
	if len(denyRoots) == 0 {
		return false
	}
	for _, deny := range denyRoots {
		if !pathUnderPolicyRoot(path, deny, workspaceRoot) {
			continue
		}
		reincluded := false
		for _, allow := range allowRoots {
			// The allow entry must sit inside the deny entry to be "more specific",
			// and the path must fall under that allow entry.
			if pathWithinRoot(deny, allow) && pathUnderPolicyRoot(path, allow, workspaceRoot) {
				reincluded = true
				break
			}
		}
		if !reincluded {
			return true
		}
	}
	return false
}

// ReadExclusions holds the resolved DenyRead/AllowRead roots for a policy so a
// search walk resolves each policy entry ONCE (Abs/EvalSymlinks) and reuses the
// result across every visited path, rather than re-resolving per path. Build it
// with Engine.ReadExclusions and reuse it for the whole grep/glob walk.
type ReadExclusions struct {
	workspaceRoot string
	denyRoots     []string
	allowRoots    []string
	// protectedRoots are the automatic credential exclusions
	// (protectedCredentialPaths); AllowRead never re-includes them.
	protectedRoots []string
}

// Active reports whether anything is excluded: a configured DenyRead root or an
// automatic protected credential path. When false the exclusions are a no-op and
// the search behaves exactly as before.
func (rx *ReadExclusions) Active() bool {
	return rx != nil && (len(rx.denyRoots) > 0 || len(rx.protectedRoots) > 0)
}

// PathExcluded reports whether reading path is excluded by DenyRead, honoring a
// more-specific AllowRead re-inclusion, or by an automatic credential exclusion,
// which no allow entry re-includes. It is the per-file predicate for a walk.
func (rx *ReadExclusions) PathExcluded(path string) bool {
	if !rx.Active() {
		return false
	}
	if protectedPathDenied(rx.protectedRoots, rx.workspaceRoot, path) {
		return true
	}
	return readDeniedResolved(rx.workspaceRoot, rx.denyRoots, rx.allowRoots, path)
}

// DirExcluded reports whether a directory subtree can be skipped wholesale during
// a walk: it is read-denied AND contains no nested AllowRead root (descending is
// required to reach a re-included subtree). When it returns false on a denied dir
// because of a nested allow, PathExcluded still filters the denied siblings.
func (rx *ReadExclusions) DirExcluded(path string) bool {
	if !rx.Active() {
		return false
	}
	// A protected credential entry is a file, so it only prunes a directory when
	// the directory IS that entry; PathExcluded still filters it during the walk.
	if protectedPathDenied(rx.protectedRoots, rx.workspaceRoot, path) {
		return true
	}
	if !readDeniedResolved(rx.workspaceRoot, rx.denyRoots, rx.allowRoots, path) {
		return false
	}
	abs := path
	if !filepath.IsAbs(abs) && rx.workspaceRoot != "" {
		abs = filepath.Join(rx.workspaceRoot, path)
	}
	// allowRoots are symlink-resolved (resolvePolicyPaths), so resolve abs the
	// same way before the prefix comparison — otherwise a dir reached THROUGH a
	// symlink would fail to match a nested AllowRead root and be wrongly skipped,
	// dropping a re-included subtree. Best-effort: keep abs if it can't resolve
	// (e.g. a non-existent path), matching the deny check's tolerant behavior.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return !hasNestedAllowReadResolved(rx.allowRoots, abs)
}

// allowWriteScope builds an ad-hoc Scope from the resolved AllowWrite roots so a
// write to an AllowWrite path is validated with the SAME symlink-traversal logic
// the workspace Scope uses. Returns nil when there are no usable AllowWrite roots.
func allowWriteScope(policy Policy) *Scope {
	roots := resolveWriteRootPaths(policy.AllowWrite)
	if len(roots) == 0 {
		return nil
	}
	return &Scope{workspaceRoot: roots[0], extraRoots: roots[1:]}
}

// validateWritePath enforces the write precedence: DenyWrite wins, then (when
// workspace enforcement is on) a workspace/Scope-writable path is allowed, then
// an absolute path under an AllowWrite root is allowed, otherwise the base
// workspace/Scope block stands. The DenyWrite list applies regardless of
// enforceWorkspace; the workspace boundary itself applies only when
// enforceWorkspace. It never bypasses the symlink/out-of-workspace guards.
func validateWritePath(scope *Scope, policy Policy, enforceWorkspace bool, workspaceRoot, path string) *pathBlock {
	// The protected credential files outrank every allow: overwriting or
	// truncating the bridge token denies service, and replacing it hands the next
	// bridge start an attacker-chosen secret.
	if protectedPathDenied(protectedCredentialPaths(), workspaceRoot, path) {
		return &pathBlock{
			Code:   BlockDenied,
			Path:   path,
			Reason: path + " holds the remote bridge token and is never writable",
		}
	}
	// DenyWrite wins regardless of workspace enforcement.
	for _, deny := range resolvePolicyPaths(policy.DenyWrite) {
		if pathUnderPolicyRoot(path, deny, workspaceRoot) {
			return &pathBlock{
				Code:   BlockDenied,
				Path:   path,
				Reason: path + " is excluded by the sandbox DenyWrite policy",
			}
		}
	}
	if !enforceWorkspace {
		// Workspace confinement is off: only the explicit DenyWrite list restricts.
		return nil
	}
	base := scope.validate(path)
	if base == nil {
		return nil // writable under the workspace / Scope guard
	}
	// AllowWrite only extends ABSOLUTE paths: a relative path is inherently
	// workspace-relative and already resolved by the base guard above.
	if filepath.IsAbs(path) {
		if allow := allowWriteScope(policy); allow != nil && allow.validate(path) == nil {
			return nil
		}
	}
	return base
}

// validatePathWithPolicy is the single entry point the engine uses to validate a
// request path. The fine-grained read/write lists (DenyRead/DenyWrite, with
// AllowRead/AllowWrite) apply whenever the sandbox is enforcing, INDEPENDENT of
// enforceWorkspace, so they match the grep/glob path that also honors DenyRead
// directly. The workspace boundary (scope.validate) applies only when
// enforceWorkspace. Behavior is unchanged when the lists are empty.
func validatePathWithPolicy(scope *Scope, policy Policy, sideEffect SideEffect, enforceWorkspace bool, workspaceRoot, path string) *pathBlock {
	// A relative path cannot be anchored without a workspace root, so it cannot be
	// checked against the (absolute) path lists or workspace boundary. Fail closed
	// when there is anything to enforce; otherwise it is a no-op (unchanged from the
	// pre-path-list behavior, where an empty workspace root skipped validation).
	if workspaceRoot == "" && !filepath.IsAbs(path) {
		// A configured bridge token counts as something to enforce: the relative
		// path cannot be anchored, so it cannot be proven to miss the token file.
		if enforceWorkspace || policyHasPathLists(policy) || len(protectedCredentialPaths()) > 0 {
			return &pathBlock{
				Code:   BlockOutsideWorkspace,
				Path:   path,
				Reason: path + " cannot be validated without a workspace root",
			}
		}
		return nil
	}
	switch sideEffect {
	case SideEffectRead:
		if protectedPathDenied(protectedCredentialPaths(), workspaceRoot, path) {
			return &pathBlock{
				Code:   BlockDenied,
				Path:   path,
				Reason: path + " holds the remote bridge token and is never readable",
			}
		}
		if readDenied(policy, workspaceRoot, path) {
			return &pathBlock{
				Code:   BlockDenied,
				Path:   path,
				Reason: path + " is excluded by the sandbox DenyRead policy",
			}
		}
		if enforceWorkspace {
			return scope.validateRead(path)
		}
		return nil
	case SideEffectWrite, SideEffectOutOfWorkspace:
		return validateWritePath(scope, policy, enforceWorkspace, workspaceRoot, path)
	default:
		if enforceWorkspace {
			return scope.validate(path)
		}
		return nil
	}
}

// hasNestedAllowReadResolved reports whether any already-resolved AllowRead root
// sits strictly inside dir (an already-resolved absolute path). When true, a
// read-denied dir must still be descended during a walk so the re-included
// subtree is reachable.
func hasNestedAllowReadResolved(allowRoots []string, dir string) bool {
	for _, allow := range allowRoots {
		if allow != dir && pathWithinRoot(dir, allow) {
			return true
		}
	}
	return false
}

// workspaceRelGlob returns target as a clean, slash-separated path relative to
// workspaceRoot, or ok=false when target is the root itself or lies outside it
// (a workspace-rooted search never reaches such a path, so no glob is needed).
func workspaceRelGlob(workspaceRoot, target string) (string, bool) {
	rel, err := filepath.Rel(workspaceRoot, target)
	if err != nil {
		return "", false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// ReadExclusionGlobs returns ripgrep-style --glob exclusion args for the policy's
// DenyRead subtrees that fall inside the scope's workspace root, so a
// ripgrep-based search never descends into a read-denied subtree. For each such
// entry it emits `--glob`, `!<rel>` and `--glob`, `!<rel>/**`. Mirrors the
// read-subtree exclusion globs used by comparable executor sandboxes.
//
// The projection is exclusions-only: a positive ripgrep glob would switch the
// search into whitelist mode and restrict it to only matching files, so AllowRead
// re-inclusion is NOT expressed here. The Go-native grep/glob tools honor
// AllowRead precisely via the cached predicate (Engine.ReadExclusions); this
// function is the coarser ripgrep-format export for an external rg-based
// consumer. Empty when DenyRead is unset (the default), so search behavior is
// unchanged.
func ReadExclusionGlobs(policy Policy, scope *Scope) []string {
	// The automatic credential exclusions ride along so an rg-based consumer never
	// walks the bridge token when it happens to live inside the workspace.
	denyRoots := dedupeStrings(append(resolvePolicyPaths(policy.DenyRead), protectedCredentialPaths()...))
	if len(denyRoots) == 0 || scope == nil {
		return nil
	}
	workspaceRoot := scope.WorkspaceRoot()
	if workspaceRoot == "" {
		return nil
	}
	var globs []string
	for _, deny := range denyRoots {
		rel, ok := workspaceRelGlob(workspaceRoot, deny)
		if !ok {
			continue
		}
		globs = append(globs, "--glob", "!"+rel, "--glob", "!"+rel+"/**")
	}
	return globs
}

// policyHasPathLists reports whether any fine-grained path list has an
// ENFORCEABLE entry. It resolves the lists (matching how the rest of this file
// normalizes them) rather than counting raw config, so a typo or non-existent
// entry — which resolution drops — doesn't spuriously fail-close relative
// requests when there is no workspace root.
func policyHasPathLists(policy Policy) bool {
	return len(resolvePolicyPaths(policy.DenyRead)) > 0 ||
		len(resolvePolicyPaths(policy.AllowRead)) > 0 ||
		len(resolvePolicyPaths(policy.DenyWrite)) > 0 ||
		len(resolveWriteRootPaths(policy.AllowWrite)) > 0
}

// dedupeStrings returns xs with duplicates removed, preserving first-seen order.
func dedupeStrings(xs []string) []string {
	if len(xs) <= 1 {
		return xs
	}
	seen := make(map[string]struct{}, len(xs))
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if _, dup := seen[x]; dup {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
