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

func (readMinifiedFileTool) outputCategory(map[string]any) outputCategory {
	return outputCategoryFile
}

func NewReadMinifiedFileTool(workspaceRoot string) Tool {
	return NewScopedReadMinifiedFileTool(workspaceRoot, nil)
}

func NewScopedReadMinifiedFileTool(workspaceRoot string, scope PathScope) Tool {
	return readMinifiedFileTool{
		baseTool: baseTool{
			name:        "read_minified_file",
			description: "Read source code in a safe, token-efficient form using language-aware compaction when available. Use this first to understand code; use read_file when exact text or line numbers are required.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path":   {Type: "string", Description: "Path of the file to read in minified form."},
					"offset": {Type: "integer", Description: "Optional 1-based source line to start from.", Minimum: intPtr(1)},
					"limit":  {Type: "integer", Description: "Optional maximum number of source lines to minify.", Minimum: intPtr(1)},
				},
				Required:             []string{"path"},
				AdditionalProperties: false,
			},
			safety:       readOnlySafety("Reads a minified view of file contents without modifying files."),
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: true, ResourceKeys: fileResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool readMinifiedFileTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.run(args, RunOptions{}, true)
}

func (tool readMinifiedFileTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	return tool.run(args, options, false)
}

func (tool readMinifiedFileTool) run(args map[string]any, options RunOptions, directBudget bool) Result {
	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filepath", "filename"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for read_minified_file: " + err.Error())
	}
	offset, err := intArg(args, "offset", 1, 1, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for read_minified_file: " + err.Error())
	}
	limit, err := intArg(args, "limit", 0, 1, 0)
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

	selected := selectSourceLines(content, offset, limit)
	result := minify.File(relativePath, selected)
	rawLines := lineCount(string(selected))
	minLines := lineCount(result.Content)
	pct := 0
	if rawBytes := len(selected); rawBytes > 0 {
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

	rawBytes := len(selected)
	compactBytes := len(result.Content)
	savedTokens := 0
	if savedBytes := rawBytes - compactBytes; savedBytes > 0 {
		savedTokens = estimatedTokensFromBytes(savedBytes)
	}
	output := header + "\n\n" + result.Content
	meta := map[string]string{}
	meta["path"] = relativePath
	meta["mode"] = result.Language
	meta["compacted"] = strconv.FormatBool(result.Applied)
	meta["raw_bytes"] = strconv.Itoa(rawBytes)
	meta["compact_bytes"] = strconv.Itoa(compactBytes)
	meta["emitted_bytes"] = strconv.Itoa(len(output))
	meta["raw_lines"] = strconv.Itoa(rawLines)
	meta["emitted_lines"] = strconv.Itoa(minLines)
	meta["estimated_tokens_saved"] = strconv.Itoa(savedTokens)
	toolResult := Result{Status: StatusOK, Output: output, Meta: meta}
	if directBudget {
		return applyLegacyByteBudgetToResult(toolResult, readOutputBudgetBytes, "use offset/limit to request a smaller source range, or read_file when exact text is required")
	}
	return toolResult
}

func selectSourceLines(content []byte, offset, limit int) []byte {
	if offset <= 1 && limit == 0 {
		return content
	}
	lines := strings.Split(string(content), "\n")
	start := offset - 1
	if start > len(lines) {
		start = len(lines)
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return []byte(strings.Join(lines[start:end], "\n"))
}

// lineCount reports the number of newline-separated lines in s (an empty string
// counts as 0 lines, matching how a reader perceives an empty file).
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
