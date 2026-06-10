package tools

import (
	"context"

	"github.com/Gitlawb/zero/internal/sandbox"
)

type SideEffect string
type Permission string
type Status string

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

type Safety struct {
	SideEffect SideEffect
	Permission Permission
	Reason     string
	// AdvertiseInAuto allows selected prompt-gated tools to be visible while
	// still requiring the normal permission flow before execution.
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
}

type Tool interface {
	Name() string
	Description() string
	Parameters() Schema
	Safety() Safety
	Run(ctx context.Context, args map[string]any) Result
}

type baseTool struct {
	name        string
	description string
	parameters  Schema
	safety      Safety
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
