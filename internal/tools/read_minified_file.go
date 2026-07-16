package tools

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/minify"
)

type readMinifiedFileTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewReadMinifiedFileTool(workspaceRoot string) Tool {
	return NewScopedReadMinifiedFileTool(workspaceRoot, nil)
}

func NewScopedReadMinifiedFileTool(workspaceRoot string, scope PathScope) Tool {
	return readMinifiedFileTool{
		baseTool: baseTool{
			name:        "read_minified_file",
			description: "Read a source file in a dense, token-cheap form: comments and redundant whitespace removed, no line numbers. Use it to scan or understand code for far fewer tokens than read_file. For exact text, comments, line numbers, or before editing, use read_file instead.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path": {Type: "string", Description: "Path of the file to read in minified form."},
				},
				Required:             []string{"path"},
				AdditionalProperties: false,
			},
			safety:       readOnlySafety("Reads a minified view of file contents without modifying files."),
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: false, ResourceKeys: fileResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool readMinifiedFileTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool readMinifiedFileTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filepath", "filename"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for read_minified_file: " + err.Error())
	}

	absolutePath, relativePath, err := resolveScopedReadPath(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error reading file " + requestedPath + ": " + err.Error())
	}

	content, err := os.ReadFile(absolutePath)
	if err != nil {
		return errorResult("Error reading file " + relativePath + ": " + err.Error())
	}
	// Record the raw whole-file baseline (matching read_file/edit_file) so a later
	// write can still detect an out-of-Zero modification — the minification only
	// affects what the model SEES, not the tracked on-disk state.
	info, _ := os.Stat(absolutePath)
	options.FileTracker.Record(absolutePath, content, info)

	result := minify.File(relativePath, content)
	rawLines := lineCount(string(content))
	minLines := lineCount(result.Content)
	pct := 0
	if rawBytes := len(content); rawBytes > 0 {
		if saved := rawBytes - len(result.Content); saved > 0 {
			pct = saved * 100 / rawBytes
		}
	}

	var header string
	if result.Applied {
		header = fmt.Sprintf("File: %s — minified %s view (comments stripped, no line numbers; %d→%d lines, ~%d%% fewer bytes). For exact text/comments or before editing, use read_file.",
			relativePath, result.Language, rawLines, minLines, pct)
	} else {
		header = fmt.Sprintf("File: %s — whitespace-normalized view (no line numbers; %d→%d lines; full minification not available for this file type). For exact text, use read_file.",
			relativePath, rawLines, minLines)
	}

	rawBytes := len(content)
	compactBytes := len(result.Content)
	savedTokens := 0
	if savedBytes := rawBytes - compactBytes; savedBytes > 0 {
		savedTokens = estimatedTokensFromBytes(savedBytes)
	}
	output := header + "\n\n" + result.Content
	budgeted := applyOutputBudget(output, readOutputBudgetBytes, "use read_file with start_line/end_line or max_lines for a smaller exact range")
	meta := outputBudgetMeta(budgeted)
	meta["path"] = relativePath
	meta["mode"] = result.Language
	meta["compacted"] = strconv.FormatBool(result.Applied)
	meta["raw_bytes"] = strconv.Itoa(rawBytes)
	meta["compact_bytes"] = strconv.Itoa(compactBytes)
	meta["emitted_bytes"] = strconv.Itoa(budgeted.EmittedBytes)
	meta["raw_lines"] = strconv.Itoa(rawLines)
	meta["emitted_lines"] = strconv.Itoa(minLines)
	meta["estimated_tokens_saved"] = strconv.Itoa(savedTokens)
	if budgeted.Truncated {
		meta["truncated"] = "true"
		meta["truncation_reason"] = "byte_budget"
	}

	return Result{Status: StatusOK, Output: budgeted.Output, Truncated: budgeted.Truncated, Meta: meta}
}

// lineCount reports the number of newline-separated lines in s (an empty string
// counts as 0 lines, matching how a reader perceives an empty file).
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
