package tools

import "context"

type Registry struct {
	tools map[string]Tool
}

type RunOptions struct {
	PermissionGranted bool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (registry *Registry) Register(tool Tool) {
	registry.tools[tool.Name()] = tool
}

func (registry *Registry) Get(name string) (Tool, bool) {
	tool, ok := registry.tools[name]
	return tool, ok
}

func (registry *Registry) All() []Tool {
	tools := make([]Tool, 0, len(registry.tools))
	for _, tool := range registry.tools {
		tools = append(tools, tool)
	}
	return tools
}

func (registry *Registry) Run(ctx context.Context, name string, args map[string]any) Result {
	return registry.RunWithOptions(ctx, name, args, RunOptions{})
}

func (registry *Registry) RunWithOptions(ctx context.Context, name string, args map[string]any, options RunOptions) Result {
	tool, ok := registry.Get(name)
	if !ok {
		return errorResult(`Error: Unknown tool "` + name + `".`)
	}

	switch tool.Safety().Permission {
	case PermissionAllow:
	case PermissionPrompt:
		if !options.PermissionGranted {
			return errorResult("Error: Permission required for " + name + ": " + tool.Safety().Reason + ` The tool is marked "prompt" and was not executed.`)
		}
	default:
		return errorResult("Error: Permission denied for " + name + ": " + tool.Safety().Reason)
	}

	return tool.Run(ctx, args)
}

func CoreReadOnlyTools(workspaceRoot string) []Tool {
	return []Tool{
		NewReadFileTool(workspaceRoot),
		NewListDirectoryTool(workspaceRoot),
		NewGlobTool(workspaceRoot),
		NewGrepTool(workspaceRoot),
	}
}

func CoreWriteTools(workspaceRoot string) []Tool {
	return []Tool{
		NewWriteFileTool(workspaceRoot),
		NewEditFileTool(workspaceRoot),
		NewApplyPatchTool(workspaceRoot),
		NewUpdatePlanTool(),
	}
}

func CoreTools(workspaceRoot string) []Tool {
	tools := append([]Tool{}, CoreReadOnlyTools(workspaceRoot)...)
	tools = append(tools, CoreWriteTools(workspaceRoot)...)
	return tools
}
