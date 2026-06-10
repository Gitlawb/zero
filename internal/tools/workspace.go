package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ignoredDirectories = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".turbo":       true,
	"coverage":     true,
	".cache":       true,
	"tmp":          true,
	"temp":         true,
}

func normalizeWorkspaceRoot(workspaceRoot string) string {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return workspaceRoot
	}
	return root
}

func resolveWorkspacePath(workspaceRoot string, requestedPath string) (string, string, error) {
	if requestedPath == "" {
		requestedPath = "."
	}

	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", err
	}

	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("%s must stay inside the workspace", requestedPath)
	}
	if relative == "." {
		return target, ".", nil
	}
	return target, filepath.ToSlash(relative), nil
}

func resolveWorkspaceTargetPath(workspaceRoot string, requestedPath string) (string, string, error) {
	if requestedPath == "" {
		requestedPath = "."
	}

	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	if err := recheckWorkspaceWriteTarget(root, target); err != nil {
		return "", "", err
	}

	existing := target
	missingSegments := []string{}
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if os.IsNotExist(err) {
			parent := filepath.Dir(existing)
			if parent == existing {
				return "", "", err
			}
			missingSegments = append([]string{filepath.Base(existing)}, missingSegments...)
			existing = parent
			continue
		} else {
			return "", "", err
		}
	}

	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", "", err
	}
	for _, segment := range missingSegments {
		resolved = filepath.Join(resolved, segment)
	}

	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("%s must stay inside the workspace", requestedPath)
	}
	if relative == "." {
		return resolved, ".", nil
	}
	return resolved, filepath.ToSlash(relative), nil
}

func recheckWorkspaceWriteTarget(workspaceRoot string, requestedPath string) error {
	if requestedPath == "" {
		requestedPath = "."
	}

	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}

	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("%s must stay inside the workspace", requestedPath)
	}
	if relative == "." {
		return nil
	}

	current := root
	for _, segment := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if segment == "." || segment == "" {
			continue
		}

		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			symlinkRelative, err := filepath.Rel(root, current)
			if err != nil {
				symlinkRelative = current
			}
			return fmt.Errorf("%s must not traverse symlink %s", requestedPath, filepath.ToSlash(symlinkRelative))
		}
	}

	return nil
}

func shouldSkipDirectory(name string) bool {
	return ignoredDirectories[name]
}

// PathScope is the multi-root write scope shared with the sandbox engine.
// *sandbox.Scope satisfies it; nil means workspace-only (today's behavior).
type PathScope interface {
	Roots() []string
}

// scopedRoots returns the ordered roots to try for an absolute path:
// the scope's roots when present, else just the workspace root.
func scopedRoots(workspaceRoot string, scope PathScope) []string {
	if scope == nil {
		return []string{workspaceRoot}
	}
	return scope.Roots()
}

// resolveScopedPath is resolveWorkspacePath generalized to a scope: relative
// paths resolve against the workspace root only; an absolute path resolves
// against the first root that contains it. The workspace root's error is
// returned when no root matches so messages stay stable.
func resolveScopedPath(workspaceRoot string, scope PathScope, requestedPath string) (string, string, error) {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) {
		return resolveWorkspacePath(workspaceRoot, requestedPath)
	}
	// Normalize platform-level symlinks (e.g. macOS /var -> /private/var) so
	// the path can be compared against EvalSymlinks-resolved scope roots.
	normalizedPath := normalizeScopedAbsPath(requestedPath)
	var firstErr error
	for _, root := range scopedRoots(workspaceRoot, scope) {
		absolute, relative, err := resolveWorkspacePath(root, normalizedPath)
		if err == nil {
			return absolute, relative, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", "", firstErr
}

// resolveScopedTargetPath mirrors resolveWorkspaceTargetPath for write targets
// (the target may not exist yet) across all scope roots.
func resolveScopedTargetPath(workspaceRoot string, scope PathScope, requestedPath string) (string, string, error) {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) {
		return resolveWorkspaceTargetPath(workspaceRoot, requestedPath)
	}
	// Normalize the leading symlinks in the absolute path (e.g. macOS
	// /var -> /private/var) so the path can be compared against the
	// symlink-resolved scope roots. Walk up to the first existing ancestor,
	// resolve it, and re-append the missing tail segments.
	normalizedPath := normalizeScopedAbsPath(requestedPath)
	var firstErr error
	for _, root := range scopedRoots(workspaceRoot, scope) {
		absolute, relative, err := resolveWorkspaceTargetPath(root, normalizedPath)
		if err == nil {
			return absolute, relative, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", "", firstErr
}

// normalizeScopedAbsPath resolves platform-level symlinks in the existing
// prefix of an absolute path, leaving non-existent tail segments verbatim.
// This handles macOS /var -> /private/var style redirects so that an
// unresolved absolute path can be compared against EvalSymlinks-resolved roots.
func normalizeScopedAbsPath(absPath string) string {
	existing := absPath
	missing := []string{}
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if os.IsNotExist(err) {
			parent := filepath.Dir(existing)
			if parent == existing {
				return absPath // safety: can't walk further up
			}
			missing = append([]string{filepath.Base(existing)}, missing...)
			existing = parent
		} else {
			return absPath
		}
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return absPath
	}
	for _, seg := range missing {
		resolved = filepath.Join(resolved, seg)
	}
	return resolved
}

// recheckScopedWriteTarget mirrors recheckWorkspaceWriteTarget across roots.
func recheckScopedWriteTarget(workspaceRoot string, scope PathScope, requestedPath string) error {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) {
		return recheckWorkspaceWriteTarget(workspaceRoot, requestedPath)
	}
	// Normalize platform-level symlinks so the path can be compared against
	// EvalSymlinks-resolved scope roots.
	normalizedPath := normalizeScopedAbsPath(requestedPath)
	var firstErr error
	for _, root := range scopedRoots(workspaceRoot, scope) {
		err := recheckWorkspaceWriteTarget(root, normalizedPath)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
