package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type grepTool struct {
	baseTool
	workspaceRoot string
}

type grepMatch struct {
	file string
	line int
	text string
	hits int
}

func NewGrepTool(workspaceRoot string) Tool {
	return grepTool{
		baseTool: baseTool{
			name:        "grep",
			description: "Search file contents with a regular expression inside the workspace.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"pattern":          {Type: "string", Description: "Regular expression pattern to search for."},
					"path":             {Type: "string", Description: "Directory or file to search. Defaults to workspace root.", Default: "."},
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
	}
}

func (tool grepTool) Run(_ context.Context, args map[string]any) Result {
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

	target, _, err := resolveWorkspacePath(tool.workspaceRoot, targetPath)
	if err != nil {
		return errorResult("Error running grep: " + err.Error())
	}

	// Resolve the workspace root through symlinks ONCE so (a) confinement checks
	// and (b) Rel computations both use the canonical root. tool.workspaceRoot is
	// only Abs-normalized (no EvalSymlinks); using it directly would produce
	// "../"-laden relative paths when the root itself lives under a symlink (e.g.
	// macOS /tmp -> /private/tmp) and would not catch files that resolve outside.
	resolvedRoot, err := filepath.EvalSymlinks(tool.workspaceRoot)
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

	files, err := grepFiles(resolvedRoot, target, globMatcher)
	if err != nil {
		return errorResult("Error running grep: " + err.Error())
	}

	matches := collectGrepMatches(resolvedRoot, files, compiled)
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
		return okResult(strings.Join(files, "\n"))
	default:
		lines := make([]string, 0, len(matches))
		for _, match := range matches {
			if len(lines) >= headLimit {
				break
			}
			lines = append(lines, fmt.Sprintf("%s:%d: %s", match.file, match.line, match.text))
		}
		return Result{
			Status:    StatusOK,
			Output:    strings.Join(lines, "\n"),
			Truncated: len(matches) > headLimit,
		}
	}
}

// confineGrepFile resolves a candidate file through symlinks and returns its
// clean, slash-separated path RELATIVE to the (already symlink-resolved) root.
// It returns ok=false when the resolved file escapes the workspace root, so a
// symlink inside the workspace that points outside is never searched/returned —
// mirroring resolveWorkspaceTargetPath / read_file confinement. resolvedRoot must
// already be EvalSymlinks-resolved so the Rel result is "../"-free for in-root
// files even when the root lives under a symlink.
func confineGrepFile(resolvedRoot string, path string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	relative, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return "", false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func grepFiles(resolvedRoot string, target string, globMatcher *regexp.Regexp) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		relative, ok := confineGrepFile(resolvedRoot, target)
		if !ok {
			return []string{}, nil
		}
		if globMatcher == nil || globMatcher.MatchString(relative) {
			return []string{target}, nil
		}
		return []string{}, nil
	}

	files := []string{}
	err = filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == target {
			return nil
		}
		if entry.IsDir() && shouldSkipDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		// Confine each candidate through symlinks: a symlink inside the workspace
		// pointing to a file OUTSIDE the root must be skipped, not searched.
		relative, ok := confineGrepFile(resolvedRoot, path)
		if !ok {
			return nil
		}
		if globMatcher == nil || globMatcher.MatchString(relative) {
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

func collectGrepMatches(resolvedRoot string, files []string, compiled *regexp.Regexp) []grepMatch {
	matches := []grepMatch{}
	for _, file := range files {
		// Re-confine at read time (defense-in-depth) AND to compute the clean
		// workspace-relative path used in output.
		relative, ok := confineGrepFile(resolvedRoot, file)
		if !ok {
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for index, line := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
			lineMatches := compiled.FindAllStringIndex(line, -1)
			if len(lineMatches) == 0 {
				continue
			}
			matches = append(matches, grepMatch{
				file: relative,
				line: index + 1,
				text: strings.TrimRight(line, "\r"),
				hits: len(lineMatches),
			})
		}
	}
	return matches
}
