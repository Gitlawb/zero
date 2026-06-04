package tools

import "context"

type Registry struct {
	tools map[string]Tool
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
	tool, ok := registry.Get(name)
	if !ok {
		return errorResult(`Error: Unknown tool "` + name + `".`)
	}

	if tool.Safety().Permission != PermissionAllow {
		return errorResult("Error: Permission required for " + name + ": " + tool.Safety().Reason)
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
