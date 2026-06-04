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
}

func NewWriteFileTool(workspaceRoot string) Tool {
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
	}
}

func (tool writeFileTool) Run(_ context.Context, args map[string]any) Result {
	requestedPath, err := stringArg(args, "path", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}
	content, err := stringArgWithEmpty(args, "content", "", true, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}
	overwrite, err := boolArg(args, "overwrite", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}

	absolutePath, relativePath, err := resolveWorkspaceTargetPath(tool.workspaceRoot, requestedPath)
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
	if err := recheckWorkspaceWriteTarget(tool.workspaceRoot, requestedPath); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}

	if existed {
		return okResult(fmt.Sprintf("Overwrote %s (%d bytes).", relativePath, len([]byte(content))))
	}
	return okResult(fmt.Sprintf("Created %s (%d bytes).", relativePath, len([]byte(content))))
}
