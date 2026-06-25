package tools

import (
	"context"

	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/streamjson"
)

type Registry struct {
	tools map[string]Tool
}

type RunOptions struct {
	PermissionGranted bool
	PermissionMode    string
	Autonomy          string
	Sandbox           *sandbox.Engine
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
	// Progress, when set, is called with each stream-json event emitted by a
	// specialist child process while it runs. Used by the TUI to show live
	// tool-call progress in the specialist card. nil is a no-op (the default
	// for every non-Task tool).
	Progress func(streamjson.Event)
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

// deferralEligibleTool lets a tool keep counting toward the deferral threshold
// even when it is dynamically exposed eagerly (Deferred()==false). A tool whose
// Deferred() flips false at runtime — e.g. the swarm coordination tools once a
// swarm is active — would otherwise lower the global eligible count and risk
// dropping it below DeferThreshold, which would deactivate deferral for ALL
// tools (force-exposing every MCP schema). Implementing this keeps the count
// stable so un-deferring one tool can never force-expose others.
type deferralEligibleTool interface {
	DeferralEligible() bool
}

// IsDeferralEligible reports whether a tool counts toward the DeferThreshold.
// A currently-deferred tool always counts. A tool may ALSO opt in via
// DeferralEligible() to keep counting while exposed eagerly (see above). This is
// the count partitionTools uses for the active-gate; whether a tool is actually
// hidden is still decided by IsDeferred.
func IsDeferralEligible(t Tool) bool {
	if IsDeferred(t) {
		return true
	}
	d, ok := t.(deferralEligibleTool)
	return ok && d.DeferralEligible()
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
	if rejecter, ok := tool.(PrePermissionRejecter); ok {
		if res, rejected := rejecter.RejectBeforePermission(args); rejected {
			return res
		}
	}

	permission := effectiveToolPermission(tool, args)
	sandboxGrantAuthorized := false
	var sandboxDecision *sandbox.Decision
	if options.Sandbox != nil {
		d := options.Sandbox.Evaluate(ctx, sandbox.Request{
			ToolName:          name,
			SideEffect:        sandbox.SideEffect(tool.Safety().SideEffect),
			Permission:        sandbox.Permission(permission),
			PermissionGranted: options.PermissionGranted,
			PermissionMode:    sandbox.PermissionMode(options.PermissionMode),
			Args:              args,
			Reason:            tool.Safety().Reason,
		})
		sandboxDecision = &d
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
		// A persistent grant OR a sandbox auto-allow authorizes a prompt tool to run
		// without a separately-recorded PermissionGranted; the sandbox is the safety
		// boundary.
		sandboxGrantAuthorized = d.Action == sandbox.ActionAllow && (d.GrantMatched || d.AutoAllowed)
	}

	switch permission {
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

func effectiveToolPermission(tool Tool, args map[string]any) Permission {
	if permissioner, ok := tool.(ArgsPermissioner); ok {
		return permissioner.PermissionForArgs(args)
	}
	return tool.Safety().Permission
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
	// Display.Preview is a file head / diff that can contain secrets, so scrub it at
	// the same boundary as Output/Summary before it reaches the card.
	if scrubbed := redaction.RedactString(res.Display.Preview, redaction.Options{}); scrubbed != res.Display.Preview {
		res.Display.Preview = scrubbed
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
		NewScopedReadMinifiedFileTool(workspaceRoot, scope),
		NewScopedListDirectoryTool(workspaceRoot, scope),
		NewScopedGlobTool(workspaceRoot, scope),
		NewScopedGrepTool(workspaceRoot, scope),
		// lsp_navigate is semantic code navigation (definition/references/impl/
		// workspace_symbol) via the language server; read-only, scoped to the
		// workspace, and degrades to a clear "unavailable" when no server is
		// installed for the file type.
		NewScopedLSPNavigateTool(workspaceRoot, scope),
		// skill reads reusable instruction files from the skills dir (it resolves
		// skills.DefaultDir itself); read-only, so it is safe in the core/MCP set.
		NewSkillTool(""),
		NewAskUserTool(),
		NewRequestPermissionsTool(),
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
	execManager := newExecSessionManager()
	return []Tool{
		NewScopedExecCommandTool(workspaceRoot, scope, execManager),
		NewWriteStdinTool(execManager),
		NewScopedBashTool(workspaceRoot, scope),
	}
}

func CoreNetworkTools() []Tool {
	tools := []Tool{NewWebFetchTool()}
	// Only offer the built-in web_search when a backend is actually configured.
	// Registering it unconfigured makes the model waste calls (and high-risk
	// permission prompts) on a tool that can only return "no backend configured";
	// an MCP-provided search tool (e.g. Exa) stands on its own without it.
	if defaultSearchBackend() != nil {
		tools = append(tools, NewWebSearchTool())
	}
	return tools
}

func CoreTools(workspaceRoot string) []Tool { return CoreToolsScoped(workspaceRoot, nil) }
func CoreToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	tools := append([]Tool{}, CoreReadOnlyToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreWriteToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreShellToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreNetworkTools()...)
	return tools
}
