package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type editFileTool struct {
	baseTool
	workspaceRoot string
}

func NewEditFileTool(workspaceRoot string) Tool {
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
			safety: promptSafety(SideEffectWrite, "Edits files in place."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
	}
}

func (tool editFileTool) Run(_ context.Context, args map[string]any) Result {
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

	absolutePath, relativePath, err := resolveWorkspacePath(tool.workspaceRoot, requestedPath)
	if err != nil {
		return errorResult("Error reading " + requestedPath + ": " + err.Error())
	}
	contentBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		return errorResult("Error reading " + relativePath + ": " + err.Error())
	}
	content := string(contentBytes)
	occurrences := strings.Count(content, oldString)

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
	if err := recheckWorkspaceWriteTarget(tool.workspaceRoot, requestedPath); err != nil {
		return errorResult("Error writing " + relativePath + ": " + err.Error())
	}
	if err := os.WriteFile(absolutePath, []byte(updated), 0o644); err != nil {
		return errorResult("Error writing " + relativePath + ": " + err.Error())
	}

	suffix := ""
	if replacedCount != 1 {
		suffix = "s"
	}
	summary := fmt.Sprintf("Successfully edited %s (replaced %d occurrence%s).", relativePath, replacedCount, suffix)
	result := okResult(summary)
	result.ChangedFiles = []string{relativePath}
	result.Display = Display{Summary: fmt.Sprintf("Edited %s", relativePath), Kind: "diff"}
	return result
}
