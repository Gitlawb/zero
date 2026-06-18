package tools

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type readFileTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewReadFileTool(workspaceRoot string) Tool {
	return NewScopedReadFileTool(workspaceRoot, nil)
}

func NewScopedReadFileTool(workspaceRoot string, scope PathScope) Tool {
	return readFileTool{
		baseTool: baseTool{
			name:        "read_file",
			description: "Read a file with optional 1-based inclusive line range and max line cap.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path":       {Type: "string", Description: "Path of the file to read."},
					"start_line": {Type: "integer", Description: "1-based inclusive line number to start reading from.", Minimum: intPtr(1)},
					"end_line":   {Type: "integer", Description: "1-based inclusive line number to stop reading at.", Minimum: intPtr(1)},
					"max_lines":  {Type: "integer", Description: "Maximum number of lines to return.", Minimum: intPtr(1)},
				},
				Required:             []string{"path"},
				AdditionalProperties: false,
			},
			safety: readOnlySafety("Reads file contents without modifying files."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool readFileTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool readFileTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filepath", "filename"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for read_file: " + err.Error())
	}
	startLine, err := intArg(args, "start_line", 1, 1, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for read_file: " + err.Error())
	}
	endLine, err := intArg(args, "end_line", 0, 1, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for read_file: " + err.Error())
	}
	maxLines, err := intArg(args, "max_lines", 0, 1, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for read_file: " + err.Error())
	}

	absolutePath, relativePath, err := resolveScopedReadPath(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error reading file " + requestedPath + ": " + err.Error())
	}

	content, err := os.ReadFile(absolutePath)
	if err != nil {
		return errorResult("Error reading file " + relativePath + ": " + err.Error())
	}
	// Record the whole-file baseline (the raw bytes, matching what edit_file and
	// write_file read) so a later write can detect an out-of-Zero modification.
	// Stat is best-effort: a missing FileInfo only drops the diagnostic size/mtime,
	// not the authoritative content hash.
	info, _ := os.Stat(absolutePath)
	options.FileTracker.Record(absolutePath, content, info)

	normalizedContent := strings.ReplaceAll(string(content), "\r\n", "\n")
	normalizedContent = strings.TrimSuffix(normalizedContent, "\n")
	lines := strings.Split(normalizedContent, "\n")
	total := len(lines)
	if startLine > total {
		return okResult(fmt.Sprintf("File: %s\n(start_line %d is past the end of the file, which has %d lines)", relativePath, startLine, total))
	}

	if endLine == 0 || endLine > total {
		endLine = total
	}
	if endLine < startLine {
		return errorResult("Error: Invalid arguments for read_file: end_line must be greater than or equal to start_line")
	}

	selected := lines[startLine-1 : endLine]
	truncated := false
	if maxLines > 0 && len(selected) > maxLines {
		selected = selected[:maxLines]
		truncated = true
	}

	lastLine := startLine + len(selected) - 1
	width := len(strconv.Itoa(lastLine))
	numbered := make([]string, 0, len(selected))
	for index, line := range selected {
		lineNumber := strconv.Itoa(startLine + index)
		numbered = append(numbered, strings.Repeat(" ", width-len(lineNumber))+lineNumber+" | "+line)
	}

	header := fmt.Sprintf("File: %s (%d lines)", relativePath, total)
	if startLine != 1 || endLine != total || maxLines > 0 {
		header = fmt.Sprintf("File: %s (lines %d-%d of %d)", relativePath, startLine, lastLine, total)
	}

	body := strings.Join(numbered, "\n")
	if truncated {
		// The Truncated flag alone is invisible to the model in the rendered
		// output, so it cannot tell a max_lines cut from a complete read. Make the
		// cut explicit and tell it how to continue.
		body += fmt.Sprintf("\n\n[truncated: %d more line(s) in the requested range not shown; set start_line=%d to continue]", endLine-lastLine, lastLine+1)
	}

	return Result{
		Status:    StatusOK,
		Output:    header + "\n\n" + body,
		Truncated: truncated,
	}
}
