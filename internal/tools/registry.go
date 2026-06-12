package tools

import (
	"context"

	"github.com/Gitlawb/zero/internal/redaction"
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
	ToolCallID        string
	SessionID         string
	Model             string
	ReasoningEffort   string
	Depth             int
	Cwd               string
	// FileTracker, when set, records the version of each file read or written this
	// session so write_file/edit_file can refuse to clobber a file that changed on
	// disk outside Zero since it was last read. nil disables the feature entirely
	// (the read/write tools behave exactly as before).
	FileTracker *FileTracker
	// EnabledTools / DisabledTools carry the run's operator tool filters so a
	// filter-aware tool (tool_search) never discloses or loads an operator-hidden
	// tool. They use the same allow/deny semantics as the agent's filter gate:
	// denied if listed in DisabledTools; if EnabledTools is non-empty, allowed
	// only when listed in it.
	EnabledTools  []string
	DisabledTools []string
}

type sandboxAwareTool interface {
	RunWithSandbox(ctx context.Context, args map[string]any, engine *sandbox.Engine) Result
}

type optionsAwareTool interface {
	RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result
}

// deferredTool is an optional interface a tool implements to mark itself
// deferred-eligible: when many such tools are registered, the agent loop may
// withhold their full schema and advertise them via tool_search instead.
type deferredTool interface {
	Deferred() bool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// IsDeferred reports whether a tool opts into deferred loading. A tool is
// deferred-eligible only if it implements deferredTool and its Deferred()
// method returns true; tools that do not implement the interface are eager.
func IsDeferred(t Tool) bool {
	d, ok := t.(deferredTool)
	return ok && d.Deferred()
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

func (registry *Registry) RunWithOptions(ctx context.Context, name string, args map[string]any, options RunOptions) (result Result) {
	// Every return path passes through scrubResultSecrets exactly once, so denial,
	// permission, and unknown-tool error messages (which can echo secret-bearing
	// args/paths) are redacted at the boundary just like tool output.
	defer func() { result = scrubResultSecrets(result) }()

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

	if optioned, ok := tool.(optionsAwareTool); ok {
		res := optioned.RunWithOptions(ctx, args, options)
		if res.SandboxDecision == nil {
			res.SandboxDecision = sandboxDecision
		}
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

// scrubResultSecrets redacts known secret forms from a tool result's Output at
// the registry boundary, so a tool can never leak a secret into the transcript.
func scrubResultSecrets(res Result) Result {
	if scrubbed := redaction.RedactString(res.Output, redaction.Options{}); scrubbed != res.Output {
		res.Output = scrubbed
		res.Redacted = true
	}
	// Display.Summary can echo command/output fragments, so scrub it too: a caller
	// that prefers Display must not bypass the boundary redaction.
	if scrubbed := redaction.RedactString(res.Display.Summary, redaction.Options{}); scrubbed != res.Display.Summary {
		res.Display.Summary = scrubbed
		res.Redacted = true
	}
	// Meta values carry model-controlled strings (e.g. glob pattern, bash cwd) and
	// are forwarded into the transcript, so they are part of the boundary too.
	for key, value := range res.Meta {
		if scrubbed := redaction.RedactString(value, redaction.Options{}); scrubbed != value {
			res.Meta[key] = scrubbed
			res.Redacted = true
		}
	}
	return res
}

func CoreReadOnlyTools(workspaceRoot string) []Tool {
	return CoreReadOnlyToolsScoped(workspaceRoot, nil)
}
func CoreReadOnlyToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	return []Tool{
		NewScopedReadFileTool(workspaceRoot, scope),
		NewScopedListDirectoryTool(workspaceRoot, scope),
		NewScopedGlobTool(workspaceRoot, scope),
		NewScopedGrepTool(workspaceRoot, scope),
		// skill reads reusable instruction files from the skills dir (it resolves
		// skills.DefaultDir itself); read-only, so it is safe in the core/MCP set.
		NewSkillTool(""),
		NewAskUserTool(),
	}
}

func CoreWriteTools(workspaceRoot string) []Tool { return CoreWriteToolsScoped(workspaceRoot, nil) }
func CoreWriteToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	return []Tool{
		NewScopedWriteFileTool(workspaceRoot, scope),
		NewScopedEditFileTool(workspaceRoot, scope),
		NewScopedApplyPatchTool(workspaceRoot, scope),
		NewUpdatePlanTool(),
	}
}

func CoreShellTools(workspaceRoot string) []Tool { return CoreShellToolsScoped(workspaceRoot, nil) }
func CoreShellToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	return []Tool{
		NewScopedBashTool(workspaceRoot, scope),
	}
}

func CoreNetworkTools() []Tool {
	return []Tool{
		NewWebFetchTool(),
		NewWebSearchTool(),
	}
}

func CoreTools(workspaceRoot string) []Tool { return CoreToolsScoped(workspaceRoot, nil) }
func CoreToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	tools := append([]Tool{}, CoreReadOnlyToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreWriteToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreShellToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreNetworkTools()...)
	return tools
}
