package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

type editFileTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewScopedEditFileTool(workspaceRoot string, scope PathScope) Tool {
	return editFileTool{
		baseTool: baseTool{
			name:        "edit_file",
			description: "Replace an exact string in an existing file with uniqueness protection by default.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path":        {Type: "string", Description: "Path of the file to edit."},
					"old_string":  {Type: "string", Description: "Exact string to replace. Must match byte-for-byte."},
					"new_string":  {Type: "string", Description: "Replacement string. May be empty."},
					"replace_all": {Type: "boolean", Description: "Replace every occurrence instead of requiring uniqueness.", Default: false},
				},
				Required:             []string{"path", "old_string", "new_string"},
				AdditionalProperties: false,
			},
			safety:       promptSafety(SideEffectWrite, "Edits files in place."),
			capabilities: ToolCapabilities{Effect: EffectWorkspaceWrite, ThreadSafe: false, ResourceKeys: fileResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool editFileTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool editFileTool) RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result {
	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filename"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for edit_file: " + err.Error())
	}
	oldString, err := aliasedStringArg(args, []string{"old_string", "old", "search", "find", "old_str"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for edit_file: " + err.Error())
	}
	newString, err := aliasedStringArg(args, []string{"new_string", "new", "replace", "replacement", "new_str"}, "", true, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for edit_file: " + err.Error())
	}
	replaceAll, err := boolArg(args, "replace_all", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for edit_file: " + err.Error())
	}

	absolutePath, relativePath, err := resolveScopedPath(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error reading " + requestedPath + ": " + err.Error())
	}
	contentBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		return errorResult("Error reading " + relativePath + ": " + err.Error())
	}
	// Refuse to edit a file that changed on disk outside Zero since it was last
	// read: the model's old_string was formed against a stale view, so applying it
	// now could silently corrupt the newer content.
	if cerr := options.FileTracker.CheckConflict(absolutePath, contentBytes); cerr != nil {
		return errorResult(fileConflictMessage(relativePath))
	}
	content := string(contentBytes)
	occurrences := strings.Count(content, oldString)

	// CRLF fallback: read_file normalizes \r\n → \n before presenting content to
	// the model, so the model's old_string will use \n line endings. When the raw
	// file uses \r\n (common on Windows), the \n-based old_string won't match.
	// Detect this and transparently normalize: if the direct match fails, translate
	// old_string's line endings to \r\n and search again. If found, use the CRLF-translated
	// old_string and new_string for the replacement to preserve the file's existing EOL style
	// without rewriting EOLs in unrelated parts of the file.
	if occurrences == 0 && strings.Contains(content, "\r\n") && !strings.Contains(oldString, "\r\n") {
		crlfOldString := strings.ReplaceAll(oldString, "\n", "\r\n")
		normalizedOccurrences := strings.Count(content, crlfOldString)
		if normalizedOccurrences > 0 {
			occurrences = normalizedOccurrences
			oldString = crlfOldString
			if !strings.Contains(newString, "\r\n") {
				newString = strings.ReplaceAll(newString, "\n", "\r\n")
			}
		}
	}

	// Fuzzy fallback: when the exact string (and its CRLF translation) is absent,
	// run a cascade of tolerant matchers (trimmed lines, block anchors, collapsed
	// whitespace, indentation drift, escape normalization) to locate the span the
	// model intended. Only a span that occurs literally in the file is accepted,
	// so the replacement applied below is still exact.
	if occurrences == 0 {
		findOld, findNew := oldString, newString
		if strings.Contains(content, "\r\n") && !strings.Contains(findOld, "\r\n") {
			findOld = strings.ReplaceAll(findOld, "\n", "\r\n")
			findNew = strings.ReplaceAll(findNew, "\n", "\r\n")
		}
		search, ferr := fuzzyEditMatch(content, findOld, replaceAll)
		switch {
		case ferr == nil:
			oldString = search
			// The model's new_string was written at old_string's (mismatched)
			// indentation; re-shape it to the span actually being replaced so a
			// tolerant match never strips indentation or a trailing CR.
			newString = adaptReplacementToSpan(search, findOld, findNew)
			occurrences = strings.Count(content, search)
		case errors.Is(ferr, errEditFuzzyAmbiguous):
			return errorResult("Error: old_string matches multiple locations in " + relativePath + " even after fuzzy matching. Provide more surrounding context to make the match unique, or pass replace_all: true.")
		case errors.Is(ferr, errEditFuzzyNotFound):
			// Fall through to the exact-match error below.
		default:
			return errorResult("Error editing " + relativePath + ": " + ferr.Error())
		}
	}

	if occurrences == 0 {
		return errorResult("Error: Could not find the exact string to replace in " + relativePath + ". The old_string must match the file byte-for-byte.")
	}
	if !replaceAll && occurrences > 1 {
		return errorResult(fmt.Sprintf("Error: old_string matches %d locations in %s. Either make old_string more specific, or pass replace_all: true to replace every occurrence.", occurrences, relativePath))
	}

	updated := strings.Replace(content, oldString, newString, 1)
	replacedCount := 1
	if replaceAll {
		updated = strings.ReplaceAll(content, oldString, newString)
		replacedCount = occurrences
	}
	if updated == content {
		return okResult("No changes: new_string is identical to old_string.")
	}
	if err := recheckScopedWriteTarget(tool.workspaceRoot, tool.scope, requestedPath); err != nil {
		return errorResult("Error writing " + relativePath + ": " + err.Error())
	}
	if err := os.WriteFile(absolutePath, []byte(updated), 0o644); err != nil {
		return errorResult("Error writing " + relativePath + ": " + err.Error())
	}
	// Optional format-on-write (ZERO_FORMAT_ON_WRITE). Must run BEFORE the
	// FileTracker re-baseline: recording pre-format content would make the very
	// next edit look like an external modification and trip the conflict guard.
	updated = maybeFormatWrittenFile(ctx, absolutePath, updated)
	// Re-baseline to the content we just wrote so subsequent edits in this session
	// compare against the current on-disk state, not the pre-edit version.
	newInfo, _ := os.Stat(absolutePath)
	options.FileTracker.Record(absolutePath, []byte(updated), newInfo)

	suffix := ""
	if replacedCount != 1 {
		suffix = "s"
	}
	summary := fmt.Sprintf("Successfully edited %s (replaced %d occurrence%s).", relativePath, replacedCount, suffix)
	summary += inlineDiagnostics(ctx, options, absolutePath, relativePath)
	result := okResult(summary)
	result.ChangedFiles = []string{relativePath}
	// Card-only preview (Display.Preview): the model's Output stays the one-line
	// summary, so the red/green diff costs zero model tokens.
	result.Display = Display{Summary: fmt.Sprintf("Edited %s", relativePath), Kind: "diff", Preview: boundedUnifiedDiff(relativePath, content, updated)}
	return result
}
