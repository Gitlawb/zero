package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type readFileTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func (readFileTool) outputCategory(map[string]any) outputCategory { return outputCategoryFile }

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
			// ThreadSafe: FileTracker is mutex-guarded; concurrent reads of
			// distinct paths are safe. Same-path calls still serialize via
			// resource-key conflict detection in the agent parallel planner.
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: true, ResourceKeys: fileResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool readFileTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.run(args, RunOptions{}, true)
}

func (tool readFileTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	return tool.run(args, options, false)
}

func (tool readFileTool) run(args map[string]any, options RunOptions, directBudget bool) Result {
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

	stats, err := scanReadFileStats(absolutePath)
	if err != nil {
		return errorResult("Error reading file " + relativePath + ": " + err.Error())
	}
	// Record the whole-file baseline (the raw bytes, matching what edit_file and
	// write_file read) so a later write can detect an out-of-Zero modification.
	// Stat is best-effort: a missing FileInfo only drops the diagnostic size/mtime,
	// not the authoritative content hash.
	options.FileTracker.RecordHash(absolutePath, stats.hash, stats.info)

	maxBytes := 0
	if directBudget {
		maxBytes = readOutputBudgetBytes
	}
	return renderReadFileRange(absolutePath, relativePath, stats.lines, startLine, endLine, maxLines, maxBytes)
}

func renderReadFileRange(absolutePath string, relativePath string, total int, startLine int, endLine int, maxLines int, maxBytes int) Result {
	if startLine > total {
		return okResult(fmt.Sprintf("File: %s\n(start_line %d is past the end of the file, which has %d lines)", relativePath, startLine, total))
	}
	if endLine == 0 || endLine > total {
		endLine = total
	}
	if endLine < startLine {
		return errorResult("Error: Invalid arguments for read_file: end_line must be greater than or equal to start_line")
	}

	truncated := false
	selectedLines := endLine - startLine + 1
	if maxLines > 0 && selectedLines > maxLines {
		selectedLines = maxLines
		truncated = true
	}

	lastLine := startLine + selectedLines - 1
	width := len(strconv.Itoa(lastLine))
	header := fmt.Sprintf("File: %s (%d lines)", relativePath, total)
	if startLine != 1 || endLine != total || maxLines > 0 {
		header = fmt.Sprintf("File: %s (lines %d-%d of %d)", relativePath, startLine, lastLine, total)
	}

	// The shared registry boundary applies the file policy after redaction. Its
	// input still needs a bounded capture: retain the requested range's head and
	// tail instead of materializing an arbitrarily large file in memory. Direct
	// Tool.Run calls preserve the legacy prefix-only byte budget.
	var budgetedOutput *outputBudgetBuilder
	if maxBytes > 0 {
		budgetedOutput = newOutputBudgetBuilder(maxBytes, "use start_line/end_line or max_lines to continue with a smaller range")
	} else {
		budgetedOutput = newHeadTailOutputBudgetBuilder(readOutputBudgetBytes, "use start_line/end_line or max_lines to continue with a smaller range")
	}
	budgetedOutput.WriteString(header)
	budgetedOutput.WriteString("\n\n")
	if err := appendReadFileRange(budgetedOutput, absolutePath, startLine, selectedLines, width); err != nil {
		return errorResult("Error reading file " + relativePath + ": " + err.Error())
	}
	if truncated {
		// The Truncated flag alone is invisible to the model in the rendered
		// output, so it cannot tell a max_lines cut from a complete read. Make the
		// cut explicit and tell it how to continue.
		budgetedOutput.WriteString(fmt.Sprintf("\n\n[truncated: %d more line(s) in the requested range not shown; set start_line=%d to continue]", endLine-lastLine, lastLine+1))
	}

	budgeted := budgetedOutput.Result()
	meta := map[string]string{}
	if maxBytes > 0 || budgeted.Truncated {
		for key, value := range outputBudgetMeta(budgeted) {
			meta[key] = value
		}
	}
	if truncated || budgeted.Truncated {
		meta["truncated"] = "true"
		if budgeted.Truncated {
			meta["truncation_reason"] = "byte_budget"
		} else {
			meta["truncation_reason"] = "max_lines"
		}
	}
	return Result{
		Status:    StatusOK,
		Output:    budgeted.Output,
		Truncated: truncated || budgeted.Truncated,
		Meta:      meta,
	}
}

type readFileStats struct {
	lines int
	hash  string
	info  os.FileInfo
}

func scanReadFileStats(path string) (readFileStats, error) {
	file, err := os.Open(path)
	if err != nil {
		return readFileStats{}, err
	}
	defer file.Close()

	hasher := sha256.New()
	reader := bufio.NewReader(file)
	lines := 0
	for {
		raw, _, err := readRawLine(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return readFileStats{}, err
		}
		if _, err := hasher.Write(raw); err != nil {
			return readFileStats{}, err
		}
		lines++
	}
	if lines == 0 {
		lines = 1
	}
	info, _ := file.Stat()
	return readFileStats{lines: lines, hash: hex.EncodeToString(hasher.Sum(nil)), info: info}, nil
}

func appendReadFileRange(output *outputBudgetBuilder, path string, startLine int, selectedLines int, width int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	lineNumber := 1
	emitted := 0
	for emitted < selectedLines {
		raw, ended, err := readRawLine(reader)
		if err == io.EOF {
			raw = nil
			ended = false
		} else if err != nil {
			return err
		}

		if lineNumber >= startLine {
			if emitted > 0 {
				output.WriteString("\n")
			}
			number := strconv.Itoa(lineNumber)
			output.WriteString(strings.Repeat(" ", width-len(number)))
			output.WriteString(number)
			output.WriteString(" | ")
			output.WriteString(string(trimLineBreak(raw, ended)))
			emitted++
		}
		if err == io.EOF {
			break
		}
		lineNumber++
	}
	return nil
}
