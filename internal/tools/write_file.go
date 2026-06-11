package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type writeFileTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewWriteFileTool(workspaceRoot string) Tool {
	return NewScopedWriteFileTool(workspaceRoot, nil)
}

func NewScopedWriteFileTool(workspaceRoot string, scope PathScope) Tool {
	return writeFileTool{
		baseTool: baseTool{
			name:        "write_file",
			description: "Create a new file, refusing to overwrite existing files unless overwrite is true.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path":      {Type: "string", Description: "Absolute or relative path of the file to write."},
					"content":   {Type: "string", Description: "Full file contents to write."},
					"overwrite": {Type: "boolean", Description: "Whether to allow overwriting an existing file.", Default: false},
				},
				Required:             []string{"path", "content"},
				AdditionalProperties: false,
			},
			safety: promptSafety(SideEffectWrite, "Creates or overwrites files."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool writeFileTool) Run(_ context.Context, args map[string]any) Result {
	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filename"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}
	content, err := fileContentArg(args)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}
	overwrite, err := boolArg(args, "overwrite", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}

	absolutePath, relativePath, err := resolveScopedTargetPath(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error writing file " + requestedPath + ": " + err.Error())
	}

	existed := false
	if _, err := os.Stat(absolutePath); err == nil {
		existed = true
		if !overwrite {
			return errorResult("Error: " + relativePath + " already exists. Pass overwrite: true to replace it.")
		}
	} else if !os.IsNotExist(err) {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}
	if err := recheckScopedWriteTarget(tool.workspaceRoot, tool.scope, requestedPath); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}

	verb := "Created"
	if existed {
		verb = "Overwrote"
	}
	summary := fmt.Sprintf("%s %s (%d bytes).", verb, relativePath, len([]byte(content)))
	result := okResult(summary)
	result.ChangedFiles = []string{relativePath}
	result.Display = Display{Summary: summary, Kind: "file"}
	return result
}

// fileContentArg reads the file body from "content" or a common alias that weaker
// models sometimes use instead (contents/text/body/data/file_content). It
// delegates to the shared aliasedStringArg so the present-but-non-string type
// error ("content must be a string") and the required-but-missing error
// ("content is required") stay consistent with every other tool. An empty string
// is allowed (writing an empty file), so allowEmpty is true.
func fileContentArg(args map[string]any) (string, error) {
	return aliasedStringArg(args, []string{"content", "contents", "text", "body", "data", "file_content"}, "", true, true)
}
