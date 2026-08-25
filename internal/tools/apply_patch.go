package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/sandbox"
)

type applyPatchTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewScopedApplyPatchTool(workspaceRoot string, scope PathScope) Tool {
	return applyPatchTool{
		baseTool: baseTool{
			name:        "apply_patch",
			description: "Apply a multi-hunk or multi-file patch inside the workspace (or a granted extra write root). Structured format:\n*** Begin Patch\n*** Update File: src/app.js\n@@\n unchanged context line\n-removed line\n+added line\n*** End Patch\nUse \"*** Add File: path\" with \"+\" lines to create a file and \"*** Delete File: path\" to remove one; several sections may follow each other. A unified diff (---/+++ headers with a/ b/ prefixes, @@ hunks) is also accepted and applied in-process. Paths are workspace-relative; absolute paths inside the workspace are fine. For a single targeted change, edit_file is simpler.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"patch": {Type: "string", Description: "The structured (*** Begin Patch) or unified-diff patch text."},
					"cwd":   {Type: "string", Description: "Directory where the patch should be applied. Relative paths stay in the workspace; use an absolute path to target a granted extra write root. Defaults to workspace root.", Default: "."},
				},
				Required:             []string{"patch"},
				AdditionalProperties: false,
			},
			safety:       promptSafety(SideEffectWrite, "Applies patch hunks that can create, edit, or delete files."),
			capabilities: ToolCapabilities{Effect: EffectWorkspaceWrite, ThreadSafe: false, ResourceKeys: applyPatchResourceKeys},
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
	if isStructuredPatch(patch) {
		return tool.runStructuredPatch(applyRoot, relativeRoot, patch, options)
	}
	// Fail closed on a path-bearing git header this process cannot interpret
	// exactly, using the SAME parser the sandbox gate uses: a target the two
	// disagree about is one the policy never saw. This is also the only
	// path-level refusal apply_patch has when reached through the plain
	// registry API, with no sandbox engine to consult.
	patchPaths, err := sandbox.PatchHeaderPaths(patch)
	if err != nil {
		return errorResult("Error applying patch: patch paths cannot be established safely: " + err.Error())
	}
	if err := validatePatchPaths(applyRoot, patchPaths); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	// Unified diffs are translated into the same operations and applied by
	// the same os.Root engine, so neither format opens a target by pathname
	// after validation (no check-to-use window) and git is not needed.
	operations, err := parseUnifiedPatch(patch)
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	return applyPatchOperations(applyRoot, relativeRoot, operations, options)
}

// changedFilesFromPatch extracts the unique, WORKSPACE-relative paths a patch
// touches, reusing the same per-line parser used for validation. Patch paths are
// relative to the apply cwd, so relativeRoot (the workspace-relative cwd, e.g.
// "sub/dir", or "." for the workspace root) is prefixed so callers get true
// workspace-relative paths regardless of cwd. When the apply cwd resolves to an
// extra write root, resolveScopedPath returns the absolute path as relativeRoot;
// in that case the entries in the returned slice are absolute paths, since
// workspace-relative would be ambiguous there.
func changedFilesFromPatch(relativeRoot string, patchPaths []string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, path := range patchPaths {
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

// normalizePatchPathForRoot resolves platform-level symlinks (macOS /var ->
// /private/var) in the prefix of an absolute patch path that lies outside the
// apply root, so a path the model copied from read_file compares equal to the
// symlink-resolved root. Relative paths are returned unchanged.
func normalizePatchPathForRoot(root string, path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return path
	}
	if evaluated, err := filepath.EvalSymlinks(resolvedRoot); err == nil {
		resolvedRoot = evaluated
	}
	return sandbox.NormalizePrefixForRoot(path, resolvedRoot)
}

func validatePatchPaths(root string, patchPaths []string) error {
	for _, path := range patchPaths {
		if path == "" || path == "/dev/null" {
			continue
		}
		// Relative traversal is rejected here; an absolute path is checked by
		// resolveWorkspaceTargetPath, which only accepts one inside the root.
		if path == ".." || strings.HasPrefix(path, "../") {
			return fmt.Errorf("patch path %q must stay inside the workspace", path)
		}
		absolute, _, err := resolveWorkspaceTargetPath(root, normalizePatchPathForRoot(root, path))
		if err != nil {
			return err
		}
		// The apply path refuses a protected credential per target inside
		// planStructuredPatch; repeat the lexical refusal here so a permission
		// preview never advertises the token as a writable target either.
		if err := protectedMutationDenied(absolute, root); err != nil {
			return err
		}
	}
	return nil
}

func patchFileHeaderPath(line string) string {
	if len(line) < len("--- ") {
		return ""
	}
	rest := line[len("--- "):] // "--- " and "+++ " are both 4 bytes
	if tab := strings.IndexByte(rest, '\t'); tab >= 0 {
		rest = rest[:tab]
	}
	return strings.TrimSpace(unquoteGitPath(rest))
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
