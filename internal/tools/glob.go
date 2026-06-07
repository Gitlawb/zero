package tools

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type globTool struct {
	baseTool
	workspaceRoot string
}

func NewGlobTool(workspaceRoot string) Tool {
	return globTool{
		baseTool: baseTool{
			name:        "glob",
			description: "Find files by glob pattern inside the workspace.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"pattern":      {Type: "string", Description: `Glob pattern to match, for example "**/*.go".`},
					"cwd":          {Type: "string", Description: "Directory to scan. Defaults to workspace root.", Default: "."},
					"limit":        {Type: "integer", Description: "Maximum matches to return.", Default: 100, Minimum: intPtr(1), Maximum: intPtr(1000)},
					"include_dirs": {Type: "boolean", Description: "Whether directory matches should be included.", Default: false},
				},
				Required:             []string{"pattern"},
				AdditionalProperties: false,
			},
			safety: readOnlySafety("Finds matching paths without reading contents or modifying files."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
	}
}

func (tool globTool) Run(_ context.Context, args map[string]any) Result {
	pattern, err := aliasedStringArg(args, []string{"pattern", "glob", "match", "query", "expression"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for glob: " + err.Error())
	}
	// Optional with a "." default: treat an explicit empty cwd (a common
	// weak-model quirk) the same as the key being absent rather than erroring.
	cwd, err := aliasedStringArg(args, []string{"cwd", "dir", "directory", "path"}, ".", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for glob: " + err.Error())
	}
	if cwd == "" {
		cwd = "."
	}
	limit, err := intArg(args, "limit", 100, 1, 1000)
	if err != nil {
		return errorResult("Error: Invalid arguments for glob: " + err.Error())
	}
	includeDirs, err := boolArg(args, "include_dirs", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for glob: " + err.Error())
	}

	root, _, err := resolveWorkspacePath(tool.workspaceRoot, cwd)
	if err != nil {
		return errorResult("Error running glob " + fmt.Sprintf("%q", pattern) + ": " + err.Error())
	}
	matcher, err := compileGlob(pattern)
	if err != nil {
		return errorResult("Error running glob " + fmt.Sprintf("%q", pattern) + ": " + err.Error())
	}

	matches, err := scanGlob(root, matcher, includeDirs)
	if err != nil {
		return errorResult("Error running glob " + fmt.Sprintf("%q", pattern) + ": " + err.Error())
	}
	if len(matches) == 0 {
		return okResult("No matches found for " + pattern)
	}

	sort.Strings(matches)
	truncated := len(matches) > limit
	if truncated {
		matches = matches[:limit]
	}

	return Result{
		Status:    StatusOK,
		Output:    strings.Join(matches, "\n"),
		Truncated: truncated,
		Meta: map[string]string{
			"pattern": pattern,
		},
	}
}

func scanGlob(root string, matcher *regexp.Regexp, includeDirs bool) ([]string, error) {
	matches := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.IsDir() && shouldSkipDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() && !includeDirs {
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		normalized := filepath.ToSlash(relative)
		if matcher.MatchString(normalized) {
			matches = append(matches, normalized)
		}
		return nil
	})
	return matches, err
}

func compileGlob(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	return regexp.Compile("^" + globToRegexp(filepath.ToSlash(pattern)) + "$")
}

func globToRegexp(pattern string) string {
	var builder strings.Builder
	for index := 0; index < len(pattern); index++ {
		char := pattern[index]
		switch char {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				index++
				if index+1 < len(pattern) && pattern[index+1] == '/' {
					index++
					builder.WriteString("(?:.*/)?")
				} else {
					builder.WriteString(".*")
				}
			} else {
				builder.WriteString("[^/]*")
			}
		case '?':
			builder.WriteString("[^/]")
		default:
			builder.WriteString(regexp.QuoteMeta(string(char)))
		}
	}
	return builder.String()
}
