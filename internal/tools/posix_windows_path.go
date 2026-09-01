package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// looksLikePosixAbsolute reports whether path looks like a POSIX absolute
// path (leading "/") rather than a UNC path ("//..."), a Windows volume
// ("C:/..."), or a rooted Windows path (leading backslash). A leading
// backslash is rejected before ToSlash so a path like \tmp\zero\file is
// not treated as POSIX.
func looksLikePosixAbsolute(path string) bool {
	raw := strings.TrimSpace(path)
	if strings.HasPrefix(raw, `\`) {
		return false
	}
	normalized := filepath.ToSlash(raw)
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

// isDriveAbsoluteWindowsPath reports whether path begins with a drive letter and
// a directory separator (e.g. C:\foo or C:/foo).
func isDriveAbsoluteWindowsPath(path string) bool {
	raw := strings.TrimSpace(path)
	if len(raw) >= 3 && raw[1] == ':' && isDriveLetter(raw[0]) {
		return raw[2] == '/' || raw[2] == '\\'
	}
	return false
}

// isDriveRelativeWindowsPath reports whether path begins with a drive letter
// without a directory separator (e.g. C:foo or C:), which designates a drive-relative
// path on Windows rather than an absolute path.
func isDriveRelativeWindowsPath(path string) bool {
	raw := strings.TrimSpace(path)
	if len(raw) >= 2 && raw[1] == ':' && isDriveLetter(raw[0]) {
		if len(raw) == 2 || (raw[2] != '/' && raw[2] != '\\') {
			return true
		}
	}
	return false
}

// isAbsForGOOS reports whether path is absolute on goos, independently of the
// host. Non-Windows goos treats a leading "/" as absolute rather than calling
// host filepath.IsAbs. On Windows a POSIX leading "/" is not absolute (no
// volume or UNC), matching Windows filepath.IsAbs, so those paths join onto
// the workspace.
func isAbsForGOOS(goos, path string) bool {
	if goos != "windows" {
		return strings.HasPrefix(filepath.ToSlash(path), "/")
	}
	raw := strings.TrimSpace(path)
	if raw == "" {
		return false
	}
	n := filepath.ToSlash(raw)
	if strings.HasPrefix(n, "//") {
		return true
	}
	if isDriveAbsoluteWindowsPath(raw) {
		return true
	}
	// Volume-relative Windows abs uses a leading backslash, not POSIX "/".
	return raw[0] == '\\'
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
	raw := strings.Split(normalized, "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return parts
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
// rewrite real Windows absolute paths and does not invent files. Callers
// must keep the original argument when existingLiteralPosixWorkspacePath
// is true so an on-disk file at the literal join is not shadowed.
func rewritePosixWorkspacePath(goos, workspaceRoot, requested string) string {
	if goos != "windows" {
		return requested
	}
	if stripped, ok := stripSyntheticPosixWorkspacePrefix(workspaceRoot, requested); ok {
		return stripped
	}
	return requested
}

// existingLiteralPosixWorkspacePath reports whether the un-rewritten POSIX
// path already names an existing file inside the workspace. When it does,
// the rewrite must not steal the request: a model that named
// /tmp/<repo>/x when that file exists should read and write that file,
// not a different x at the workspace root.
func existingLiteralPosixWorkspacePath(goos, workspaceRoot, requested string) bool {
	if goos != "windows" {
		return false
	}
	if rewritePosixWorkspacePath(goos, workspaceRoot, requested) == requested {
		return false
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return false
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	target := joinAgainstRoot(goos, root, requested)
	target, err = filepath.Abs(target)
	if err != nil {
		return false
	}
	if _, err := workspaceRelative(root, target, requested); err != nil {
		return false
	}
	_, err = os.Lstat(target)
	return err == nil
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
// The hint does not name the workspace root: a POSIX path such as /etc/passwd
// joins into the workspace as a missing file, and naming the root would leak
// it next to the requested path.
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
	return fmt.Errorf("%w; host is Windows and %q looks like a POSIX absolute path; use a workspace-relative path or a Windows path", redactPathErrorWorkspaceRoot(err, requested), requested)
}

// redactPathErrorWorkspaceRoot replaces PathError.Path with the original
// request so a missing POSIX path such as /etc/passwd cannot echo the
// joined workspace root next to the hint. The PathError.Err value is kept
// so errors.Is(..., os.ErrNotExist) still holds.
func redactPathErrorWorkspaceRoot(err error, requested string) error {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr == nil {
		return err
	}
	redacted := *pathErr
	redacted.Path = requested
	return &redacted
}
