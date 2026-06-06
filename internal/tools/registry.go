package tools

import (
	"context"

	"github.com/Gitlawb/zero/internal/sandbox"
)

type Registry struct {
	tools map[string]Tool
}

type RunOptions struct {
	PermissionGranted bool
	PermissionMode    string
	Autonomy          string
	Sandbox           *sandbox.Engine
}

type sandboxAwareTool interface {
	RunWithSandbox(ctx context.Context, args map[string]any, engine *sandbox.Engine) Result
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

	sandboxGrantAuthorized := false
	if options.Sandbox != nil {
		decision := options.Sandbox.Evaluate(ctx, sandbox.Request{
			ToolName:          name,
			SideEffect:        sandbox.SideEffect(tool.Safety().SideEffect),
			Permission:        sandbox.Permission(tool.Safety().Permission),
			PermissionGranted: options.PermissionGranted,
			PermissionMode:    sandbox.PermissionMode(options.PermissionMode),
			Autonomy:          sandbox.Autonomy(options.Autonomy),
			Args:              args,
			Reason:            tool.Safety().Reason,
		})
		if decision.Action == sandbox.ActionDeny {
			return errorResult(decision.ErrorString())
		}
		if decision.Action == sandbox.ActionPrompt && !options.PermissionGranted {
			return errorResult("Error: Sandbox approval required for " + name + ": " + decision.Reason)
		}
		sandboxGrantAuthorized = decision.Action == sandbox.ActionAllow && decision.GrantMatched
	}

	switch tool.Safety().Permission {
	case PermissionAllow:
	case PermissionPrompt:
		if !options.PermissionGranted && !sandboxGrantAuthorized {
			return errorResult("Error: Permission required for " + name + ": " + tool.Safety().Reason + ` The tool is marked "prompt" and was not executed.`)
		}
	default:
		return errorResult("Error: Permission denied for " + name + ": " + tool.Safety().Reason)
	}

	if options.Sandbox != nil {
		if sandboxed, ok := tool.(sandboxAwareTool); ok {
			return sandboxed.RunWithSandbox(ctx, args, options.Sandbox)
		}
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

func CoreShellTools(workspaceRoot string) []Tool {
	return []Tool{
		NewBashTool(workspaceRoot),
	}
}

func CoreTools(workspaceRoot string) []Tool {
	tools := append([]Tool{}, CoreReadOnlyTools(workspaceRoot)...)
	tools = append(tools, CoreWriteTools(workspaceRoot)...)
	tools = append(tools, CoreShellTools(workspaceRoot)...)
	return tools
}
