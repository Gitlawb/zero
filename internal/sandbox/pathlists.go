package sandbox

import (
	"os"
	"path/filepath"
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
	if root == "" {
		return false
	}
	abs := requestedPath
	if !filepath.IsAbs(abs) {
		if workspaceRoot == "" {
			return false
		}
		abs = filepath.Join(workspaceRoot, abs)
	}
	normalized := NormalizePrefixForRoot(abs, root)
	return pathWithinRoot(root, normalized)
}

// readDenied reports whether path is excluded by the DenyRead list with no
// more-specific AllowRead re-inclusion. "More specific" means an AllowRead entry
// nested inside the matched DenyRead entry — that subtree is read back in while
// the rest of the denied tree stays blocked.
func readDenied(policy Policy, workspaceRoot, path string) bool {
	denyRoots := resolvePolicyPaths(policy.DenyRead)
	if len(denyRoots) == 0 {
		return false
	}
	allowRoots := resolvePolicyPaths(policy.AllowRead)
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

// validateWritePath enforces the write precedence: DenyWrite wins, then a
// workspace/Scope-writable path is allowed, then an absolute path under an
// AllowWrite root is allowed, otherwise the base workspace/Scope violation
// stands. It never bypasses the symlink/out-of-workspace guards.
func validateWritePath(scope *Scope, policy Policy, workspaceRoot, path string) *pathViolation {
	for _, deny := range resolvePolicyPaths(policy.DenyWrite) {
		if pathUnderPolicyRoot(path, deny, workspaceRoot) {
			return &pathViolation{
				Code:   ViolationPolicyDenied,
				Path:   path,
				Reason: path + " is excluded by the sandbox DenyWrite policy",
			}
		}
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
// request path: it applies the fine-grained read/write lists for read and write
// side effects and falls back to the plain workspace/Scope guard for everything
// else, so behavior is unchanged when the lists are empty.
func validatePathWithPolicy(scope *Scope, policy Policy, sideEffect SideEffect, workspaceRoot, path string) *pathViolation {
	switch sideEffect {
	case SideEffectRead:
		if readDenied(policy, workspaceRoot, path) {
			return &pathViolation{
				Code:   ViolationPolicyDenied,
				Path:   path,
				Reason: path + " is excluded by the sandbox DenyRead policy",
			}
		}
		return scope.validate(path)
	case SideEffectWrite, SideEffectOutOfWorkspace:
		return validateWritePath(scope, policy, workspaceRoot, path)
	default:
		return scope.validate(path)
	}
}

// hasNestedAllowRead reports whether any AllowRead root sits strictly inside dir
// (an already-resolved absolute path). When true, a read-denied dir must still be
// descended during a walk so the re-included subtree is reachable.
func hasNestedAllowRead(policy Policy, dir string) bool {
	for _, allow := range resolvePolicyPaths(policy.AllowRead) {
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
// AllowRead precisely via the per-path predicate (Engine.ReadPathExcluded /
// ReadDirExcluded); this function is the coarser ripgrep-format export for an
// external rg-based consumer. Empty when DenyRead is unset (the default), so
// search behavior is unchanged.
func ReadExclusionGlobs(policy Policy, scope *Scope) []string {
	denyRoots := resolvePolicyPaths(policy.DenyRead)
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
