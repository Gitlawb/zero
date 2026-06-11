package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Scope is the shared set of directories the sandbox allows writes in: the
// workspace root plus zero or more user-granted extra roots. One instance is
// created per run and shared by the policy engine, the OS command runners, and
// the file tools, so a mid-session Add is immediately visible to every layer.
type Scope struct {
	mu            sync.RWMutex
	workspaceRoot string
	extraRoots    []string
}

// NewScope builds a scope for workspaceRoot plus the given extra roots. The
// workspace root is normalized best-effort (it may not exist in tests); every
// extra root must normalize strictly via Add and an invalid one fails the
// whole construction so a bad --add-dir/config entry surfaces at startup.
func NewScope(workspaceRoot string, extras []string) (*Scope, error) {
	scope := &Scope{workspaceRoot: normalizeWorkspaceRootBestEffort(workspaceRoot)}
	for _, extra := range extras {
		if _, err := scope.Add(extra); err != nil {
			return nil, fmt.Errorf("write root %q: %w", extra, err)
		}
	}
	return scope, nil
}

// WorkspaceRoot returns the resolved workspace root. It is safe to call
// without acquiring the lock because workspaceRoot is immutable after
// construction.
func (s *Scope) WorkspaceRoot() string {
	return s.workspaceRoot
}

// Roots returns the workspace root first, then the extra roots, as a copy.
func (s *Scope) Roots() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	roots := make([]string, 0, 1+len(s.extraRoots))
	roots = append(roots, s.workspaceRoot)
	roots = append(roots, s.extraRoots...)
	return roots
}

// Add grants write access under path. The path must be an existing directory;
// it is home-expanded, made absolute, and symlink-resolved before being
// trusted, and the filesystem root is rejected outright. Adding a path already
// covered by an existing root is an idempotent success.
func (s *Scope) Add(path string) (string, error) {
	root, err := normalizeScopeRoot(path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range append([]string{s.workspaceRoot}, s.extraRoots...) {
		if pathWithinRoot(existing, root) {
			return root, nil
		}
	}
	s.extraRoots = append(s.extraRoots, root)
	return root, nil
}

// validate reports whether requestedPath is allowed by any scope root.
// Relative paths resolve against the workspace root only; absolute paths are
// accepted if they validate (including per-segment symlink checks) under ANY
// root. A symlink whose final target lies inside any granted root is allowed —
// this is a deliberate semantic widening compared with single-root validation,
// because the true write target is inside an allowed root.
//
// When all roots deny, a ViolationSymlinkTraversal result from any root is
// preferred over ViolationOutsideWorkspace; the --add-dir hint is appended
// only on outside_workspace results. The returned violation always carries
// the caller's original requestedPath.
func (s *Scope) validate(requestedPath string) *pathViolation {
	roots := s.Roots()
	if !filepath.IsAbs(requestedPath) {
		return validateWorkspacePath(roots[0], requestedPath)
	}
	// For each root, normalize the leading path prefix so that platform-level
	// symlinks (e.g. macOS /var -> /private/var) are resolved before comparing
	// against the symlink-resolved scope roots, while leaving workspace-internal
	// symlinks intact so validateWorkspacePath can detect traversal violations.
	var outsideViolation *pathViolation
	var traversalViolation *pathViolation
	for _, root := range roots {
		normalized := NormalizePrefixForRoot(requestedPath, root)
		violation := validateWorkspacePath(root, normalized)
		if violation == nil {
			return nil
		}
		switch violation.Code {
		case ViolationSymlinkTraversal:
			if traversalViolation == nil {
				traversalViolation = violation
			}
		default:
			if outsideViolation == nil {
				outsideViolation = violation
			}
		}
	}
	// Prefer symlink-traversal: the path was lexically inside a granted root
	// but crossed an in-root symlink — the --add-dir hint would be misleading.
	if traversalViolation != nil {
		return &pathViolation{
			Code:   ViolationSymlinkTraversal,
			Path:   requestedPath,
			Reason: traversalViolation.Reason,
		}
	}
	// Plain outside-workspace denial — rebuild with the original path and hint.
	return &pathViolation{
		Code:   ViolationOutsideWorkspace,
		Path:   requestedPath,
		Reason: fmt.Sprintf("%s is outside the workspace (use /add-dir or --add-dir to allow writes there)", requestedPath),
	}
}

// NormalizePrefixForRoot resolves platform-level symlinks (e.g. macOS
// /var -> /private/var) in the portion of absPath that lies outside
// resolvedRoot, while leaving workspace-internal path components intact so
// that validateWorkspacePath can detect symlink traversal violations there.
// It is exported because the tools layer shares it to normalize absolute
// paths per scope root before running its own single-root checks.
//
// Algorithm: walk absPath component-by-component, resolving each via
// EvalSymlinks. Once the running resolved prefix equals resolvedRoot we are
// inside the root — stop resolving and append the remaining components
// verbatim. If a component inside the root is a symlink, leave it for
// validateWorkspacePath to handle. Non-existent tail components are always
// appended verbatim.
//
// The walk is volume-aware so it works on Windows as well as POSIX. On
// Windows the same alias problem appears in a different guise — a workspace
// created under an 8.3 short path (C:\Users\RUNNER~1\...) is resolved by
// EvalSymlinks to its long form (C:\Users\runneradmin\...), so a raw
// short-form request would escape the long-form root unless its prefix is
// resolved here first. The component walk must therefore start from the
// volume root (C:\ or \\host\share\), not "/", or it would mangle a drive
// path into a drive-relative form (C:\Users -> C:Users) that the single-root
// checks treat as RELATIVE — failing the policy gate OPEN. On POSIX
// VolumeName is empty and the volume root reduces to "/", so behavior there
// is byte-identical to a plain "/"-rooted walk.
func NormalizePrefixForRoot(absPath, resolvedRoot string) string {
	volume := filepath.VolumeName(absPath)
	volumeRoot := volume + string(filepath.Separator)
	tail := strings.TrimPrefix(filepath.Clean(absPath), volume)
	parts := strings.Split(strings.TrimPrefix(tail, string(filepath.Separator)), string(filepath.Separator))
	current := volumeRoot
	for i, part := range parts {
		if part == "" {
			continue
		}
		// If we've reached the resolved root boundary, stop resolving and
		// append the remaining tail verbatim so validateWorkspacePath sees the
		// original symlink names.
		if current == resolvedRoot {
			return filepath.Join(append([]string{current}, parts[i:]...)...)
		}
		next := filepath.Join(current, part)
		info, lerr := os.Lstat(next)
		if lerr != nil {
			// Non-existent component — append rest verbatim.
			return filepath.Join(append([]string{current}, parts[i:]...)...)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Symlink. Only resolve it if we're still outside the root.
			if pathWithinRoot(resolvedRoot, current) {
				// Inside root — leave this symlink for validateWorkspacePath.
				return filepath.Join(append([]string{current}, parts[i:]...)...)
			}
			// Outside root (or a jump into the root) — resolve this platform-level symlink.
			resolved, err := filepath.EvalSymlinks(next)
			if err != nil {
				return filepath.Join(append([]string{current}, parts[i:]...)...)
			}
			current = resolved
			continue
		}
		// Regular component outside root — resolve it.
		resolved, err := filepath.EvalSymlinks(next)
		if err != nil {
			current = next
		} else {
			current = resolved
		}
	}
	return current
}

func normalizeWorkspaceRootBestEffort(workspaceRoot string) string {
	trimmed := strings.TrimSpace(workspaceRoot)
	if trimmed == "" {
		return ""
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return filepath.Clean(absolute)
}

func normalizeScopeRoot(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("write root path is empty")
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		trimmed = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(trimmed[1:], "/"), string(filepath.Separator)))
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("write root must exist: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("write root %s is not a directory", resolved)
	}
	if filepath.Dir(resolved) == resolved {
		return "", fmt.Errorf("refusing filesystem root %s as a write root", resolved)
	}
	return resolved, nil
}

func pathWithinRoot(root string, candidate string) bool {
	if root == "" {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
