package tools

import (
	"context"

	"github.com/Gitlawb/zero/internal/sandbox"
)

type SideEffect string
type Permission string
type Status string
type SandboxPermissionOverride string

const (
	// SideEffectNone marks a control-only tool that neither reads nor mutates
	// state — no read/write/shell/network/out-of-workspace effect. Examples:
	// tool_search (only reports already-registered tool schemas) and
	// escalate_model (only requests a loop-level model switch).
	SideEffectNone           SideEffect = "none"
	SideEffectRead           SideEffect = "read"
	SideEffectWrite          SideEffect = "write"
	SideEffectShell          SideEffect = "shell"
	SideEffectNetwork        SideEffect = "network"
	SideEffectLocalControl   SideEffect = "local_control"
	SideEffectLocalBrowser   SideEffect = "local_browser"
	SideEffectLocalDesktop   SideEffect = "local_desktop"
	SideEffectLocalTerminal  SideEffect = "local_terminal"
	SideEffectOutOfWorkspace SideEffect = "out_of_workspace"
)

const (
	PermissionAllow  Permission = "allow"
	PermissionPrompt Permission = "prompt"
	PermissionDeny   Permission = "deny"
)

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

const (
	SandboxPermissionsUseDefault                SandboxPermissionOverride = "use_default"
	SandboxPermissionsRequireEscalated          SandboxPermissionOverride = "require_escalated"
	SandboxPermissionsWithAdditionalPermissions SandboxPermissionOverride = "with_additional_permissions"
)

const (
	SandboxLikelyDeniedMeta  = "sandbox_likely_denied"
	SandboxDenialKindMeta    = "sandbox_denial_kind"
	SandboxDenialReasonMeta  = "sandbox_denial_reason"
	SandboxDenialKeywordMeta = "sandbox_denial_keyword"
)

const (
	SandboxDenialKindSandbox = "sandbox"
	SandboxDenialKindNetwork = "network"
)

type Safety struct {
	SideEffect SideEffect
	Permission Permission
	Reason     string
	// AdvertiseInAuto allows selected non-allow tools to be visible in auto mode
	// while still requiring the normal permission flow before execution.
	AdvertiseInAuto bool
}

type Schema struct {
	Type                 string                    `json:"type"`
	Properties           map[string]PropertySchema `json:"properties,omitempty"`
	Required             []string                  `json:"required,omitempty"`
	AdditionalProperties bool                      `json:"additionalProperties"`
}

type PropertySchema struct {
	Type        string          `json:"type"`
	Description string          `json:"description,omitempty"`
	Enum        []string        `json:"enum,omitempty"`
	Default     any             `json:"default,omitempty"`
	Items       *PropertySchema `json:"items,omitempty"`
	Minimum     *int            `json:"minimum,omitempty"`
	Maximum     *int            `json:"maximum,omitempty"`
	MinLength   *int            `json:"minLength,omitempty"`
	MinItems    *int            `json:"minItems,omitempty"`
	// Properties/Required describe nested object fields (for Type "object" or an
	// object-typed Items).
	Properties map[string]PropertySchema `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type Result struct {
	Status          Status
	Output          string
	Truncated       bool
	Meta            map[string]string
	SandboxDecision *sandbox.Decision `json:"-"`
	// Redacted is set when secret scrubbing altered Output before it left the
	// tool-execution boundary.
	Redacted bool
	// ChangedFiles lists workspace-relative paths a mutating tool wrote;
	// entries under a granted extra write root are absolute, since
	// workspace-relative would be ambiguous there.
	ChangedFiles []string
	// Display carries a short, structured summary for the TUI / stream.
	Display Display
}

// Display carries a short, structured summary of a tool result for the TUI/stream.
type Display struct {
	Summary string
	Kind    string // e.g. file, diff, search, shell
	// Preview is a multi-line, card-only body (e.g. a unified diff or file head)
	// for the TUI. It is NEVER sent to the model — Output stays the short summary
	// the model sees — so a rich code preview costs zero model tokens.
	Preview string
}

type Tool interface {
	Name() string
	Description() string
	Parameters() Schema
	Safety() Safety
	Run(ctx context.Context, args map[string]any) Result
}

// ArgsPermissioner is an optional interface a Tool can implement to refine its
// permission for a SPECIFIC call based on its arguments. When a tool implements
// it, the agent loop consults PermissionForArgs(args) instead of the static
// Safety().Permission when deciding whether the call needs approval. It exists to
// safely RELAX a prompt to allow for arguments the tool can prove are harmless
// (e.g. delegating to a read-only sub-agent); a tool must return its static,
// stricter permission whenever it cannot prove the call is safe.
type ArgsPermissioner interface {
	PermissionForArgs(args map[string]any) Permission
}

// PrePermissionRejecter lets a tool reject a call that cannot safely or validly
// run before any permission prompt is shown. Implementations must be purely
// local and deterministic: no filesystem, process, DNS, or network access.
type PrePermissionRejecter interface {
	RejectBeforePermission(args map[string]any) (Result, bool)
}

type baseTool struct {
	name         string
	description  string
	parameters   Schema
	safety       Safety
	capabilities ToolCapabilities // zero value = EffectUnknown, not thread-safe
}

func (tool baseTool) Name() string {
	return tool.name
}

func (tool baseTool) Description() string {
	return tool.description
}

func (tool baseTool) Parameters() Schema {
	return tool.parameters
}

func (tool baseTool) Safety() Safety {
	return tool.safety
}

func okResult(output string) Result {
	return Result{Status: StatusOK, Output: output}
}

func errorResult(output string) Result {
	return Result{Status: StatusError, Output: output}
}

func readOnlySafety(reason string) Safety {
	return Safety{
		SideEffect: SideEffectRead,
		Permission: PermissionAllow,
		Reason:     reason,
	}
}

func promptSafety(sideEffect SideEffect, reason string) Safety {
	return Safety{
		SideEffect: sideEffect,
		Permission: PermissionPrompt,
		Reason:     reason,
	}
}
