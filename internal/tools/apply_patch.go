package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type applyPatchTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewApplyPatchTool(workspaceRoot string) Tool {
	return NewScopedApplyPatchTool(workspaceRoot, nil)
}

func NewScopedApplyPatchTool(workspaceRoot string, scope PathScope) Tool {
	return applyPatchTool{
		baseTool: baseTool{
			name:        "apply_patch",
			description: "Apply a unified diff patch inside the workspace or an explicitly granted extra write root.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"patch": {Type: "string", Description: "Unified diff patch to apply."},
					"cwd":   {Type: "string", Description: "Directory where the patch should be applied. Relative paths stay in the workspace; use an absolute path to target a granted extra write root. Defaults to workspace root.", Default: "."},
				},
				Required:             []string{"patch"},
				AdditionalProperties: false,
			},
			safety: promptSafety(SideEffectWrite, "Applies patch hunks that can create, edit, or delete files."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool applyPatchTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool applyPatchTool) RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result {
	patch, err := aliasedStringArg(args, []string{"patch", "diff"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for apply_patch: " + err.Error())
	}
	cwd, err := stringArg(args, "cwd", ".", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for apply_patch: " + err.Error())
	}

	applyRoot, relativeRoot, err := resolveScopedPath(tool.workspaceRoot, tool.scope, cwd)
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	if err := validatePatchPaths(applyRoot, patch); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	tempFile, err := os.CreateTemp("", "zero-patch-*.patch")
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	patchPath := tempFile.Name()
	defer func() {
		_ = os.Remove(patchPath)
	}()
	if _, err := tempFile.WriteString(patch); err != nil {
		_ = tempFile.Close()
		return errorResult("Error applying patch: " + err.Error())
	}
	if err := tempFile.Close(); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	if err := recheckPatchWriteTargets(applyRoot, patch); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	command := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", patchPath)
	command.Dir = applyRoot
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errorResult("Error applying patch: " + message)
	}

	summary := "Patch applied successfully."
	if relativeRoot != "." {
		summary = "Patch applied successfully in " + relativeRoot + "."
	}
	result := okResult(summary)
	result.ChangedFiles = changedFilesFromPatch(relativeRoot, patch)
	result.Display = Display{Summary: summary, Kind: "diff", Preview: capPreviewDiff(patch)}
	// git apply already rejects a patch whose context drifted, so it has its own
	// staleness guard. Drop any tracked baseline for the files it rewrote so a
	// subsequent edit_file/write_file re-reads instead of false-flagging the
	// patch's own change as an external modification.
	for _, changed := range result.ChangedFiles {
		if absolute, _, rerr := resolveScopedPath(tool.workspaceRoot, tool.scope, changed); rerr == nil {
			options.FileTracker.Forget(absolute)
		}
	}
	return result
}

// changedFilesFromPatch extracts the unique, WORKSPACE-relative paths a patch
// touches, reusing the same per-line parser used for validation. Patch paths are
// relative to the apply cwd, so relativeRoot (the workspace-relative cwd, e.g.
// "sub/dir", or "." for the workspace root) is prefixed so callers get true
// workspace-relative paths regardless of cwd. When the apply cwd resolves to an
// extra write root, resolveScopedPath returns the absolute path as relativeRoot;
// in that case the entries in the returned slice are absolute paths, since
// workspace-relative would be ambiguous there.
func changedFilesFromPatch(relativeRoot string, patch string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, path := range patchHeaderPaths(patch) {
		if path == "" || path == "/dev/null" {
			continue
		}
		workspacePath := path
		if relativeRoot != "" && relativeRoot != "." {
			workspacePath = filepath.ToSlash(filepath.Join(relativeRoot, path))
		}
		if seen[workspacePath] {
			continue
		}
		seen[workspacePath] = true
		paths = append(paths, workspacePath)
	}
	return paths
}

func validatePatchPaths(root string, patch string) error {
	for _, path := range patchHeaderPaths(patch) {
		if path == "" || path == "/dev/null" {
			continue
		}
		if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
			return fmt.Errorf("patch path %q must stay inside the workspace", path)
		}
		if _, _, err := resolveWorkspaceTargetPath(root, path); err != nil {
			return err
		}
	}
	return nil
}

func recheckPatchWriteTargets(root string, patch string) error {
	for _, path := range patchHeaderPaths(patch) {
		if path == "" || path == "/dev/null" {
			continue
		}
		if err := recheckWorkspaceWriteTarget(root, path); err != nil {
			return err
		}
	}
	return nil
}

// patchHeaderPaths returns the file paths declared in a unified diff's headers
// (`diff --git` and `---`/`+++` lines). It tracks hunk state by counting body
// lines from each `@@ -a,b +c,d @@` header, so a removed/added content line that
// merely begins with "--- "/"+++ " (e.g. the removal of a markdown line "-- x")
// is NOT mistaken for a file header. This mirrors how `git apply` parses hunks,
// so a line this skips is content git won't write to either — no security gap.
func patchHeaderPaths(patch string) []string {
	var paths []string
	oldRemaining, newRemaining := 0, 0
	inHunk := false
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		if inHunk && (oldRemaining > 0 || newRemaining > 0) {
			switch {
			case strings.HasPrefix(line, "-"):
				oldRemaining--
			case strings.HasPrefix(line, "+"):
				newRemaining--
			case strings.HasPrefix(line, "\\"):
				// "\ No newline at end of file" — not a content line.
			default: // context line (" ...") or a blank context line
				oldRemaining--
				newRemaining--
			}
			continue
		}
		inHunk = false
		switch {
		case strings.HasPrefix(line, "diff --git "):
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				paths = append(paths, stripPatchPrefix(fields[2]), stripPatchPrefix(fields[3]))
			}
		case strings.HasPrefix(line, "@@"):
			oldRemaining, newRemaining = parseHunkCounts(line)
			inHunk = oldRemaining > 0 || newRemaining > 0
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			// Take everything after the fixed 4-char prefix instead of splitting on
			// spaces, so a path that contains spaces survives. Drop a trailing
			// tab-delimited timestamp (unified-diff convention) and unquote a
			// C-quoted git path (git quotes names with spaces/specials) (L18).
			rest := line[len("--- "):] // "--- " and "+++ " are both 4 bytes
			if tab := strings.IndexByte(rest, '\t'); tab >= 0 {
				rest = rest[:tab]
			}
			if p := strings.TrimSpace(unquoteGitPath(rest)); p != "" && p != "/dev/null" {
				paths = append(paths, stripPatchPrefix(p))
			}
		}
	}
	return paths
}

// parseHunkCounts reads the old/new line counts from a "@@ -a,b +c,d @@" header.
// A missing count (e.g. "@@ -a +c @@") means 1 per unified-diff convention.
//
// Only the range section BETWEEN the opening and closing "@@" is parsed. A hunk
// header may carry a free-form section heading after the closing "@@" (e.g.
// "@@ -1,1 +1,1 @@ func foo()"), and that text can itself contain "+"/"-"
// tokens. Scanning the whole line would let a crafted heading like
// "@@ -1,1 +1,1 @@ +1,999999" overwrite the real count, keep the parser stuck in
// hunk mode, and swallow later "--- "/"+++ " file headers so they escape
// validatePatchPaths / recheckPatchWriteTargets — a workspace-confinement bypass.
func parseHunkCounts(line string) (int, int) {
	_, rest, ok := strings.Cut(line, "@@")
	if !ok {
		return 0, 0
	}
	rangeSection := rest
	if before, _, ok := strings.Cut(rest, "@@"); ok {
		rangeSection = before // drop the section heading after the closing "@@"
	}
	old, next := 0, 0
	for _, field := range strings.Fields(rangeSection) {
		switch {
		case strings.HasPrefix(field, "-"):
			old = hunkCount(field[1:])
		case strings.HasPrefix(field, "+"):
			next = hunkCount(field[1:])
		}
	}
	return old, next
}

func hunkCount(spec string) int {
	if _, count, ok := strings.Cut(spec, ","); ok {
		if n, err := strconv.Atoi(count); err == nil {
			return n
		}
		return 0
	}
	return 1
}

// unquoteGitPath undoes git's C-style quoting of a diff path. Git wraps a path in
// double quotes and backslash-escapes special bytes (spaces, tabs, high bytes as
// octal) when it contains anything unusual; an unquoted path is returned as-is.
func unquoteGitPath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if unquoted, err := strconv.Unquote(s); err == nil {
			return unquoted
		}
	}
	return s
}

func stripPatchPrefix(path string) string {
	path = strings.TrimSpace(path)
	// A unified-diff path carries exactly one of the a/ or b/ prefixes; strip a
	// single one so a real directory literally named "a" or "b" is preserved.
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return filepath.ToSlash(path)
}
