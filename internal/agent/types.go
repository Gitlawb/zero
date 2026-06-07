package agent

import (
	"context"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type Message = zeroruntime.Message
type Provider = zeroruntime.Provider
type ToolCall = zeroruntime.ToolCall
type Usage = zeroruntime.Usage

type PermissionMode string
type PermissionAction string
type PermissionDecisionAction string

const (
	PermissionModeAuto   PermissionMode = "auto"
	PermissionModeAsk    PermissionMode = "ask"
	PermissionModeUnsafe PermissionMode = "unsafe"
)

const (
	PermissionActionAllow  PermissionAction = "allow"
	PermissionActionPrompt PermissionAction = "prompt"
	PermissionActionDeny   PermissionAction = "deny"
)

const (
	PermissionDecisionAllow       PermissionDecisionAction = "allow"
	PermissionDecisionDeny        PermissionDecisionAction = "deny"
	PermissionDecisionAlwaysAllow PermissionDecisionAction = "always_allow"
)

type ToolResult struct {
	ToolCallID string
	Name       string
	Status     tools.Status
	Output     string
	Meta       map[string]string
}

type PermissionRequest struct {
	ToolCallID     string             `json:"toolCallId"`
	ToolName       string             `json:"name"`
	Action         PermissionAction   `json:"action"`
	Permission     string             `json:"permission"`
	PermissionMode PermissionMode     `json:"permissionMode"`
	Autonomy       string             `json:"autonomy,omitempty"`
	SideEffect     string             `json:"sideEffect"`
	Reason         string             `json:"reason,omitempty"`
	Risk           sandbox.Risk       `json:"risk"`
	Args           map[string]any     `json:"args,omitempty"`
	Violation      *sandbox.Violation `json:"violation,omitempty"`
	GrantMatched   bool               `json:"grantMatched,omitempty"`
	Grant          *sandbox.Grant     `json:"grant,omitempty"`
}

type PermissionDecision struct {
	Action PermissionDecisionAction `json:"action"`
	Reason string                   `json:"reason,omitempty"`
}

type PermissionEvent struct {
	ToolCallID        string             `json:"toolCallId"`
	ToolName          string             `json:"name"`
	Action            PermissionAction   `json:"action"`
	Permission        string             `json:"permission"`
	PermissionGranted bool               `json:"permissionGranted,omitempty"`
	PermissionMode    PermissionMode     `json:"permissionMode"`
	Autonomy          string             `json:"autonomy,omitempty"`
	SideEffect        string             `json:"sideEffect"`
	Reason            string             `json:"reason,omitempty"`
	DecisionReason    string             `json:"decisionReason,omitempty"`
	Risk              sandbox.Risk       `json:"risk"`
	Violation         *sandbox.Violation `json:"violation,omitempty"`
	GrantMatched      bool               `json:"grantMatched,omitempty"`
	Grant             *sandbox.Grant     `json:"grant,omitempty"`
}

type Options struct {
	MaxTurns int
	// Specialist/sub-agent metadata is carried through exec now and consumed by
	// the specialist runtime in later slices.
	CallingSessionID    string
	CallingToolUseID    string
	Tag                 string
	Depth               int
	SessionTitle        string
	Registry            *tools.Registry
	PermissionMode      PermissionMode
	Autonomy            string
	Sandbox             *sandbox.Engine
	EnabledTools        []string
	DisabledTools       []string
	OnText              func(string)
	OnToolCall          func(ToolCall)
	OnPermissionRequest func(context.Context, PermissionRequest) (PermissionDecision, error)
	OnPermission        func(PermissionEvent)
	OnToolResult        func(ToolResult)
	OnUsage             func(Usage)
}

type Result struct {
	FinalAnswer string
	Turns       int
	Messages    []Message
}
