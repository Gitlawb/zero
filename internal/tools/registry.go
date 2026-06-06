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
	OnSandboxDecision func(sandbox.Decision)
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
	var sandboxDecision *sandbox.Decision
	if options.Sandbox != nil {
		d := options.Sandbox.Evaluate(ctx, sandbox.Request{
			ToolName:          name,
			SideEffect:        sandbox.SideEffect(tool.Safety().SideEffect),
			Permission:        sandbox.Permission(tool.Safety().Permission),
			PermissionGranted: options.PermissionGranted,
			PermissionMode:    sandbox.PermissionMode(options.PermissionMode),
			Autonomy:          sandbox.Autonomy(options.Autonomy),
			Args:              args,
			Reason:            tool.Safety().Reason,
		})
		sandboxDecision = &d
		if options.OnSandboxDecision != nil {
			go func(dec sandbox.Decision) {
				defer func() {
					if r := recover(); r != nil {
						// Fail-safe: never let a consumer callback panic the tool execution path.
						// In real use the agent loop or CLI may log this; for now we swallow to keep tools reliable.
					}
				}()
				options.OnSandboxDecision(dec)
			}(d)
		}
		if d.Action == sandbox.ActionDeny {
			res := errorResult(d.ErrorString())
			res.SandboxDecision = sandboxDecision
			return res
		}
		if d.Action == sandbox.ActionPrompt && !options.PermissionGranted {
			res := errorResult("Error: Sandbox approval required for " + name + ": " + d.Reason)
			res.SandboxDecision = sandboxDecision
			return res
		}
		sandboxGrantAuthorized = d.Action == sandbox.ActionAllow && d.GrantMatched
	}

	switch tool.Safety().Permission {
	case PermissionAllow:
	case PermissionPrompt:
		if !options.PermissionGranted && !sandboxGrantAuthorized {
			res := errorResult("Error: Permission required for " + name + ": " + tool.Safety().Reason + ` The tool is marked "prompt" and was not executed.`)
			res.SandboxDecision = sandboxDecision
			return res
		}
	default:
		res := errorResult("Error: Permission denied for " + name + ": " + tool.Safety().Reason)
		res.SandboxDecision = sandboxDecision
		return res
	}

	if options.Sandbox != nil {
		if sandboxed, ok := tool.(sandboxAwareTool); ok {
			res := sandboxed.RunWithSandbox(ctx, args, options.Sandbox)
			res.SandboxDecision = sandboxDecision
			return res
		}
	}
	res := tool.Run(ctx, args)
	res.SandboxDecision = sandboxDecision
	return res
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
