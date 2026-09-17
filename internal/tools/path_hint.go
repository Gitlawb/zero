package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// looksLikePosixAbsolutePath reports whether p has the shape of an absolute
// POSIX path: a single leading "/". A double slash is a Windows UNC path, and
// a drive-letter path is Windows-absolute, so neither is a POSIX path the
// model hallucinated. Shape-only: it never consults GOOS or the filesystem.
func looksLikePosixAbsolutePath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//")
}

// annotateMissingPosixPathError appends a Windows-only hint to a read-path
// resolution error. On Windows a leading-slash POSIX path is not absolute
// (filepath.IsAbs is false without a volume name), so it is joined under the
// workspace root and a miss surfaces as a raw OS-level error; the hint names
// the host OS and the workspace root so the model stops retrying the same
// shape. The error is wrapped with %w so errors.Is/errors.As keep working.
//
// It returns err unchanged unless the host is Windows, requestedPath looks
// like an absolute POSIX path, and err reports a missing path. It never
// changes which path is resolved or opened.
func annotateMissingPosixPathError(goos string, workspaceRoot string, requestedPath string, err error) error {
	if err == nil || goos != "windows" || !looksLikePosixAbsolutePath(requestedPath) || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Manual quotes rather than %q: %q escapes the backslashes in a Windows
	// workspace root, which makes the suggestion harder to use verbatim.
	hint := "[zero] path hint: this host is Windows, so the POSIX path was resolved under the workspace root \"" +
		workspaceRoot + "\" and does not exist there.\n" +
		"Suggestion: use a workspace-relative path such as \"internal/tools/workspace.go\", or a Windows absolute path under that workspace root."
	return fmt.Errorf("%w\n%s", err, hint)
}
