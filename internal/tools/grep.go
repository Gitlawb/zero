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
	pattern, err := stringArg(args, "pattern", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
	}
	targetPath, err := stringArg(args, "path", ".", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for grep: " + err.Error())
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

	var globMatcher *regexp.Regexp
	if globPattern != "" {
		globMatcher, err = compileGlob(globPattern)
		if err != nil {
			return errorResult("Error running grep: " + err.Error())
		}
	}

	files, err := grepFiles(tool.workspaceRoot, target, globMatcher)
	if err != nil {
		return errorResult("Error running grep: " + err.Error())
	}

	matches := collectGrepMatches(tool.workspaceRoot, files, compiled)
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

func grepFiles(workspaceRoot string, target string, globMatcher *regexp.Regexp) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		relative, err := filepath.Rel(workspaceRoot, target)
		if err != nil {
			return nil, err
		}
		if globMatcher == nil || globMatcher.MatchString(filepath.ToSlash(relative)) {
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
		relative, err := filepath.Rel(workspaceRoot, path)
		if err != nil {
			return err
		}
		if globMatcher == nil || globMatcher.MatchString(filepath.ToSlash(relative)) {
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

func collectGrepMatches(workspaceRoot string, files []string, compiled *regexp.Regexp) []grepMatch {
	matches := []grepMatch{}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(workspaceRoot, file)
		if err != nil {
			continue
		}
		for index, line := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
			lineMatches := compiled.FindAllStringIndex(line, -1)
			if len(lineMatches) == 0 {
				continue
			}
			matches = append(matches, grepMatch{
				file: filepath.ToSlash(relative),
				line: index + 1,
				text: strings.TrimRight(line, "\r"),
				hits: len(lineMatches),
			})
		}
	}
	return matches
}
