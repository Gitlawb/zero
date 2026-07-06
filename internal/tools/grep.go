package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/sandbox"
)

type grepTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

type grepMatch struct {
	file string
	line int
	text string
	hits int
}

func NewGrepTool(workspaceRoot string) Tool {
	return NewScopedGrepTool(workspaceRoot, nil)
}

func NewScopedGrepTool(workspaceRoot string, scope PathScope) Tool {
	return grepTool{
		baseTool: baseTool{
			name:        "grep",
			description: "Search file contents with a regular expression inside the workspace or an explicitly granted extra root.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"pattern":          {Type: "string", Description: "Regular expression pattern to search for."},
					"path":             {Type: "string", Description: "Directory or file to search. Relative paths stay in the workspace; use an absolute path to search a granted extra root. Defaults to workspace root.", Default: "."},
					"glob":             {Type: "string", Description: `Optional glob filter, for example "**/*.go".`},
					"output_mode":      {Type: "string", Description: "Output mode.", Enum: []string{"content", "files_with_matches", "count"}, Default: "content"},
					"-i":               {Type: "boolean", Description: "Case insensitive search.", Default: false},
					"case_insensitive": {Type: "boolean", Description: "Case insensitive search.", Default: false},
					"head_limit":       {Type: "integer", Description: "Maximum content results to return.", Default: 50, Minimum: intPtr(1)},
				},
				Required:             []string{"pattern"},
				AdditionalProperties: false,
			},
			safety: readOnlySafety("Searches file paths and matching lines without modifying files."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool grepTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.runWith(ctx, args, readExcluder{})
}

// RunWithSandbox runs the search while skipping subtrees the sandbox policy
// denies reads to (DenyRead), so grep never surfaces content from a read-denied
// path. With no DenyRead configured the excluder is a no-op and behavior is
// unchanged.
func (tool grepTool) RunWithSandbox(ctx context.Context, args map[string]any, engine *sandbox.Engine) Result {
	return tool.runWith(ctx, args, sandboxReadExcluder(engine))
}

func (tool grepTool) runWith(ctx context.Context, args map[string]any, exclude readExcluder) Result {
	pattern, err := aliasedStringArg(args, []string{"pattern", "query", "regex", "search", "expression"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
	}
	// Optional with a "." default: treat an explicit empty path (a common
	// weak-model quirk) the same as the key being absent rather than erroring.
	targetPath, err := aliasedStringArg(args, []string{"path", "dir", "directory"}, ".", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
	}
	if targetPath == "" {
		targetPath = "."
	}
	globPattern, err := stringArg(args, "glob", "", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
	}
	outputMode, err := stringArg(args, "output_mode", "content", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
	}
	if outputMode != "content" && outputMode != "files_with_matches" && outputMode != "count" {
		return errorResult("Error: Invalid arguments for grep: output_mode must be content, files_with_matches, or count")
	}
	caseInsensitive, err := boolArg(args, "case_insensitive", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
	}
	shortInsensitive, err := boolArg(args, "-i", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
	}
	headLimit, err := intArg(args, "head_limit", 50, 1, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
	}

	if caseInsensitive || shortInsensitive {
		pattern = "(?i)" + pattern
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return errorResult("Error running grep: " + err.Error())
	}

	target, displayRoot, err := resolveScopedReadPath(tool.workspaceRoot, tool.scope, targetPath)
	if err != nil {
		return errorResult("Error running grep: " + err.Error())
	}

	// Resolve the workspace root through symlinks ONCE so (a) confinement checks
	// and (b) Rel computations both use the canonical root. tool.workspaceRoot is
	// only Abs-normalized (no EvalSymlinks); using it directly would produce
	// "../"-laden relative paths when the root itself lives under a symlink (e.g.
	// macOS /tmp -> /private/tmp) and would not catch files that resolve outside.
	// When a scope is present, pick the scope root that contains the resolved
	// target so that confineGrepFile computes correct relative paths.
	resolvedRoot, err := resolveGrepRoot(tool.workspaceRoot, tool.scope, target)
	if err != nil {
		return errorResult("Error running grep: " + err.Error())
	}

	var globMatcher *regexp.Regexp
	if globPattern != "" {
		globMatcher, err = compileGlob(globPattern)
		if err != nil {
			return errorResult("Error running grep: " + err.Error())
		}
	}

	files, err := grepFiles(ctx, resolvedRoot, target, globMatcher, exclude)
	if err != nil {
		if res, ok := searchCancelledResult("grep", err); ok {
			return res
		}
		return errorResult("Error running grep: " + err.Error())
	}

	// resolveScopedPath returns an absolute displayRoot when the target resolved
	// to an extra (non-workspace) granted root; emit absolute match paths there so
	// they survive a round-trip through read_file/edit_file (mirrors glob).
	matches, err := collectGrepMatches(ctx, resolvedRoot, filepath.IsAbs(displayRoot), files, compiled)
	if err != nil {
		if res, ok := searchCancelledResult("grep", err); ok {
			return res
		}
		return errorResult("Error running grep: " + err.Error())
	}
	if len(matches) == 0 {
		if outputMode == "count" {
			return okResult("0 matches found")
		}
		return okResult("No matches found.")
	}

	switch outputMode {
	case "count":
		total := 0
		for _, match := range matches {
			total += match.hits
		}
		return okResult(fmt.Sprintf("%d matches found", total))
	case "files_with_matches":
		seen := map[string]bool{}
		files := []string{}
		for _, match := range matches {
			if !seen[match.file] {
				seen[match.file] = true
				files = append(files, match.file)
			}
		}
		sort.Strings(files)
		budgeted := applyOutputBudget(strings.Join(files, "\n"), searchOutputBudgetBytes, "narrow path/glob/pattern to continue")
		meta := outputBudgetMeta(budgeted)
		if budgeted.Truncated {
			meta["truncated"] = "true"
			meta["truncation_reason"] = "byte_budget"
		}
		return Result{Status: StatusOK, Output: budgeted.Output, Truncated: budgeted.Truncated, Meta: meta}
	default:
		lines := make([]string, 0, len(matches))
		for _, match := range matches {
			if len(lines) >= headLimit {
				break
			}
			lines = append(lines, fmt.Sprintf("%s:%d: %s", match.file, match.line, match.text))
		}
		truncated := len(matches) > headLimit
		output := strings.Join(lines, "\n")
		if truncated {
			output += fmt.Sprintf("\n\n[truncated: showing first %d of %d matches; narrow path/glob/pattern or increase head_limit]", len(lines), len(matches))
		}
		budgeted := applyOutputBudget(output, searchOutputBudgetBytes, "narrow path/glob/pattern or increase head_limit")
		meta := outputBudgetMeta(budgeted)
		if truncated || budgeted.Truncated {
			meta["truncated"] = "true"
			if budgeted.Truncated {
				meta["truncation_reason"] = "byte_budget"
			} else {
				meta["truncation_reason"] = "head_limit"
			}
		}
		return Result{
			Status:    StatusOK,
			Output:    budgeted.Output,
			Truncated: truncated || budgeted.Truncated,
			Meta:      meta,
		}
	}
}

// resolveGrepRoot picks the scope root whose EvalSymlinks-resolved path contains
// the already-resolved target, so that confineGrepFile computes correct
// workspace-relative paths even when the target lives in an extra root.
// Falls back to EvalSymlinks(workspaceRoot) when no scoped root matches.
func resolveGrepRoot(workspaceRoot string, scope PathScope, resolvedTarget string) (string, error) {
	roots, err := scopedRoots(workspaceRoot, scope)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(resolved, resolvedTarget)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
			return resolved, nil
		}
	}
	// Fall back to the workspace root.
	return filepath.EvalSymlinks(workspaceRoot)
}

// confineGrepFile resolves a candidate file through symlinks and returns its
// clean, slash-separated path RELATIVE to the (already symlink-resolved) root.
// It returns ok=false when the resolved file escapes the workspace root, so a
// symlink inside the workspace that points outside is never searched/returned —
// mirroring resolveWorkspaceTargetPath / read_file confinement. resolvedRoot must
// already be EvalSymlinks-resolved so the Rel result is "../"-free for in-root
// files even when the root lives under a symlink.
func confineGrepFile(resolvedRoot string, path string) (string, string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", false
	}
	relative, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return "", "", false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", false
	}
	// Return the symlink-resolved absolute path too: callers must read THAT (not
	// the unresolved input) so a symlink swap between this check and the read
	// cannot escape the workspace boundary.
	return filepath.ToSlash(relative), resolved, true
}

func grepFiles(ctx context.Context, resolvedRoot string, target string, globMatcher *regexp.Regexp, exclude readExcluder) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		relative, _, ok := confineGrepFile(resolvedRoot, target)
		if !ok {
			return []string{}, nil
		}
		if shouldSkipWorkspaceFile(relative) {
			return []string{}, nil
		}
		if exclude.fileExcluded(target) {
			return []string{}, nil
		}
		// A single explicit file is matched by its base name so a pattern like
		// "*.go" applies regardless of how deep the file sits under the workspace.
		if globMatcher == nil || globMatcher.MatchString(filepath.Base(target)) {
			return []string{target}, nil
		}
		return []string{}, nil
	}

	files := []string{}
	err = filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		// Checked first, ahead of walkErr: an unscoped search over a large tree
		// (e.g. a broad parent directory, not just the workspace) can run long
		// enough that cancelling the run must stop the walk promptly rather than
		// visiting every remaining entry to completion.
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == target {
				return walkErr
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == target {
			return nil
		}
		if entry.IsDir() && shouldSkipDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			// Skip a read-denied subtree (sandbox DenyRead) wholesale.
			if exclude.dirExcluded(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if exclude.fileExcluded(path) {
			return nil
		}
		// Confine each candidate through symlinks: a symlink inside the workspace
		// pointing to a file OUTSIDE the root must be skipped, not searched.
		relative, _, ok := confineGrepFile(resolvedRoot, path)
		if !ok {
			return nil
		}
		if shouldSkipWorkspaceFile(relative) {
			return nil
		}
		// Match the glob against the path relative to the SEARCH directory, not the
		// workspace root, so "*.go" with path="subdir" matches subdir/a.go (as
		// "a.go") — matching ripgrep's --glob semantics. Falls back to the
		// workspace-relative path if Rel fails.
		globPath := relative
		if rel, relErr := filepath.Rel(target, path); relErr == nil {
			globPath = filepath.ToSlash(rel)
		}
		if globMatcher == nil || globMatcher.MatchString(globPath) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func collectGrepMatches(ctx context.Context, resolvedRoot string, absolutePaths bool, files []string, compiled *regexp.Regexp) ([]grepMatch, error) {
	matches := []grepMatch{}
	for _, file := range files {
		// Checked per file, matching the walk's per-entry check in grepFiles: a
		// broad search can already have collected many files before the run is
		// cancelled, and reading/regex-scanning each one is real work that must
		// stop promptly rather than running to completion.
		if err := ctx.Err(); err != nil {
			return matches, err
		}
		// Re-confine at read time (defense-in-depth) AND to compute the clean
		// workspace-relative path used in output.
		relative, resolvedPath, ok := confineGrepFile(resolvedRoot, file)
		if !ok {
			continue
		}
		if shouldSkipWorkspaceFile(relative) {
			continue
		}
		// Read the symlink-RESOLVED path that confineGrepFile validated, not the
		// raw candidate, so a symlink swapped in after the check can't escape.
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			continue
		}
		for index, line := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
			lineMatches := compiled.FindAllStringIndex(line, -1)
			if len(lineMatches) == 0 {
				continue
			}
			fileLabel := relative
			if absolutePaths {
				// Extra-root search: report the absolute, symlink-resolved path
				// confineGrepFile already validated, so a bare workspace-relative
				// name can't resolve under the workspace and hit the wrong file when
				// the same name exists in both roots.
				fileLabel = filepath.ToSlash(resolvedPath)
			}
			matches = append(matches, grepMatch{
				file: fileLabel,
				line: index + 1,
				text: strings.TrimRight(line, "\r"),
				hits: len(lineMatches),
			})
		}
	}
	return matches, nil
}
