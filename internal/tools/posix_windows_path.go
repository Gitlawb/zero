package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// looksLikePosixAbsolute reports whether path looks like a POSIX absolute
// path (leading "/") rather than a UNC path ("//...") or a Windows volume
// ("C:/..."). Backslashes are normalized with ToSlash first.
func looksLikePosixAbsolute(path string) bool {
	normalized := strings.TrimSpace(filepath.ToSlash(path))
	if normalized == "" {
		return false
	}
	if !strings.HasPrefix(normalized, "/") {
		return false
	}
	if strings.HasPrefix(normalized, "//") {
		return false
	}
	return true
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// isAbsForGOOS reports whether path is absolute on goos. On Windows a POSIX
// leading "/" is not absolute (no volume or UNC), matching Windows
// filepath.IsAbs, so those paths join onto the workspace.
func isAbsForGOOS(goos, path string) bool {
	if goos != "windows" {
		return filepath.IsAbs(path)
	}
	if path == "" {
		return false
	}
	n := filepath.ToSlash(path)
	if strings.HasPrefix(n, "//") {
		return true
	}
	if len(path) >= 2 && path[1] == ':' && isDriveLetter(path[0]) {
		return true
	}
	// Volume-relative Windows abs uses a leading backslash, not POSIX "/".
	return path[0] == '\\'
}

// joinAgainstRoot joins target onto root using goos path rules. Windows
// POSIX-absolute paths are joined as relative (trim leading "/"), because
// host filepath.Join on Unix would treat them as absolute and drop root.
func joinAgainstRoot(goos, root, target string) string {
	if isAbsForGOOS(goos, target) {
		return target
	}
	if goos == "windows" && looksLikePosixAbsolute(target) {
		rel := strings.Trim(filepath.ToSlash(target), "/")
		if rel == "" {
			return root
		}
		return filepath.Join(root, filepath.FromSlash(rel))
	}
	return filepath.Join(root, target)
}

func workspaceBasename(workspaceRoot string) string {
	repo := filepath.Base(filepath.Clean(workspaceRoot))
	switch repo {
	case "", ".", "..", string(filepath.Separator):
		return ""
	default:
		return repo
	}
}

func posixPathSegments(path string) []string {
	normalized := strings.TrimSpace(filepath.ToSlash(path))
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return nil
	}
	normalized = strings.TrimPrefix(normalized, "/")
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "/")
}

func restAfterPrefix(parts []string, prefixLen int) (string, bool) {
	if prefixLen > len(parts) {
		return "", false
	}
	rest := parts[prefixLen:]
	if len(rest) == 0 {
		return ".", true
	}
	joined := strings.Join(rest, "/")
	if strings.HasPrefix(joined, "/") {
		return "", false
	}
	return joined, true
}

func matchSyntheticHomePrefix(parts []string, lead, repo string) (string, bool) {
	// /home/<user>/<repo>/rest or /Users/<user>/<repo>/rest
	if len(parts) < 3 || lead == "" || repo == "" {
		return "", false
	}
	if parts[0] != lead {
		return "", false
	}
	user := parts[1]
	if user == "" || user == "." || user == ".." {
		return "", false
	}
	if !strings.EqualFold(parts[2], repo) {
		return "", false
	}
	return restAfterPrefix(parts, 3)
}

func matchSyntheticDirPrefix(parts []string, lead []string, repo string) (string, bool) {
	// /tmp/<repo>/rest or /var/tmp/<repo>/rest
	if repo == "" || len(parts) < len(lead)+1 {
		return "", false
	}
	for i, segment := range lead {
		if parts[i] != segment {
			return "", false
		}
	}
	if !strings.EqualFold(parts[len(lead)], repo) {
		return "", false
	}
	return restAfterPrefix(parts, len(lead)+1)
}

// stripSyntheticPosixWorkspacePrefix strips a known synthetic POSIX workspace
// prefix when it includes the workspace basename. It does not use a naive
// index of "/"+repo+"/" (a workspace named "home" would mis-strip
// /home/user/file). Callers are Windows-only; rest may contain ".." and is
// still subject to resolveWorkspacePath confinement.
func stripSyntheticPosixWorkspacePrefix(workspaceRoot, requested string) (string, bool) {
	if !looksLikePosixAbsolute(requested) {
		return requested, false
	}
	repo := workspaceBasename(workspaceRoot)
	if repo == "" {
		return requested, false
	}
	parts := posixPathSegments(requested)
	if rest, ok := matchSyntheticHomePrefix(parts, "home", repo); ok {
		return rest, true
	}
	if rest, ok := matchSyntheticHomePrefix(parts, "Users", repo); ok {
		return rest, true
	}
	if rest, ok := matchSyntheticDirPrefix(parts, []string{"tmp"}, repo); ok {
		return rest, true
	}
	if rest, ok := matchSyntheticDirPrefix(parts, []string{"var", "tmp"}, repo); ok {
		return rest, true
	}
	return requested, false
}

// rewritePosixWorkspacePath is a Windows-only rewrite of synthetic POSIX
// prefixes (/home/<user>/<repo>, /Users/<user>/<repo>, /tmp/<repo>,
// /var/tmp/<repo>) when they include the workspace basename. It does not
// rewrite real Windows absolute paths and does not invent files.
func rewritePosixWorkspacePath(goos, workspaceRoot, requested string) string {
	if goos != "windows" {
		return requested
	}
	if stripped, ok := stripSyntheticPosixWorkspacePrefix(workspaceRoot, requested); ok {
		return stripped
	}
	return requested
}

func isMissingPathError(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr != nil && os.IsNotExist(pathErr) {
		return true
	}
	return false
}

// annotatePosixWindowsPathError appends an actionable hint when a Windows host
// fails to find a path that looks POSIX-absolute. Confinement failures
// (outsideWorkspaceError) are left unchanged — those messages are already
// actionable. The requested path is the original argument so the hint names
// what the model passed, even if a synthetic prefix was already stripped.
func annotatePosixWindowsPathError(goos, workspaceRoot, requested string, err error) error {
	if goos != "windows" || err == nil {
		return err
	}
	if !looksLikePosixAbsolute(requested) {
		return err
	}
	if !isMissingPathError(err) {
		return err
	}
	root := workspaceRoot
	if abs, absErr := filepath.Abs(workspaceRoot); absErr == nil {
		root = abs
	}
	return fmt.Errorf("%w; host is Windows and %q looks like a POSIX absolute path; use a workspace-relative path or a Windows path (workspace root: %s)", err, requested, root)
}
