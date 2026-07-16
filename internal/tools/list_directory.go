package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type listDirectoryTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewListDirectoryTool(workspaceRoot string) Tool {
	return NewScopedListDirectoryTool(workspaceRoot, nil)
}

func NewScopedListDirectoryTool(workspaceRoot string, scope PathScope) Tool {
	return listDirectoryTool{
		baseTool: baseTool{
			name:        "list_directory",
			description: "List files and directories in a workspace path with optional recursion.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path":      {Type: "string", Description: "Directory to list. Defaults to workspace root.", Default: "."},
					"recursive": {Type: "boolean", Description: "Whether to list recursively.", Default: false},
					"max_depth": {Type: "integer", Description: "Maximum recursion depth when recursive is true.", Default: 2, Minimum: intPtr(1), Maximum: intPtr(5)},
				},
				AdditionalProperties: false,
			},
			safety:       readOnlySafety("Lists directory entries without modifying files."),
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: false, ResourceKeys: directoryResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool listDirectoryTool) Run(_ context.Context, args map[string]any) Result {
	// Optional with a "." default: treat an explicit empty path (a common
	// weak-model quirk) the same as the key being absent rather than erroring.
	requestedPath, err := aliasedStringArg(args, []string{"path", "directory", "dir"}, ".", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for list_directory: " + err.Error())
	}
	if requestedPath == "" {
		requestedPath = "."
	}
	recursive, err := boolArg(args, "recursive", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for list_directory: " + err.Error())
	}
	maxDepth, err := intArg(args, "max_depth", 2, 1, 5)
	if err != nil {
		return errorResult("Error: Invalid arguments for list_directory: " + err.Error())
	}
	if !recursive {
		maxDepth = 0
	}

	absolutePath, relativePath, err := resolveScopedReadPath(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error listing directory " + requestedPath + ": " + err.Error())
	}

	entries, err := listDirectoryEntries(absolutePath, 0, maxDepth)
	if err != nil {
		return errorResult("Error listing directory " + relativePath + ": " + err.Error())
	}
	if len(entries) == 0 {
		return okResult("Directory is empty: " + relativePath)
	}
	output := "Contents of " + relativePath + ":\n\n" + strings.Join(entries, "\n")
	budgeted := applyOutputBudget(output, searchOutputBudgetBytes, "use path, recursive=false, or a smaller max_depth to narrow the listing")
	meta := outputBudgetMeta(budgeted)
	if budgeted.Truncated {
		meta["truncated"] = "true"
		meta["truncation_reason"] = "byte_budget"
	}
	return Result{Status: StatusOK, Output: budgeted.Output, Truncated: budgeted.Truncated, Meta: meta}
}

func listDirectoryEntries(path string, depth int, maxDepth int) ([]string, error) {
	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	sort.Slice(dirEntries, func(left int, right int) bool {
		if dirEntries[left].IsDir() != dirEntries[right].IsDir() {
			return dirEntries[left].IsDir()
		}
		return dirEntries[left].Name() < dirEntries[right].Name()
	})

	results := make([]string, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if entry.IsDir() && shouldSkipDirectory(entry.Name()) {
			continue
		}

		indent := strings.Repeat("  ", depth)
		if entry.IsDir() {
			results = append(results, indent+entry.Name()+"/")
			if depth < maxDepth {
				children, err := listDirectoryEntries(filepath.Join(path, entry.Name()), depth+1, maxDepth)
				if err == nil {
					results = append(results, children...)
				}
			}
			continue
		}

		results = append(results, fmt.Sprintf("%s%s", indent, entry.Name()))
	}

	return results, nil
}
