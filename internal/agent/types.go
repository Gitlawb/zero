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
	ToolCallID   string
	Name         string
	Status       tools.Status
	Output       string
	Meta         map[string]string
	Redacted     bool
	ChangedFiles []string
	Display      tools.Display
	// DenialReason categorizes why a tool call was blocked (empty when it ran).
	// It lets a surface distinguish the cause precisely instead of parsing Output.
	DenialReason DenialCategory
}

// DenialCategory classifies why a tool call was blocked before it executed.
type DenialCategory string

const (
	DenialNone             DenialCategory = ""
	DenialFiltered         DenialCategory = "filtered"          // tool not enabled for this run
	DenialPermissionDenied DenialCategory = "permission_denied" // approval declined
	DenialSandboxViolation DenialCategory = "sandbox_violation" // blocked by the sandbox
)

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

// AskUserQuestion is one clarifying question the agent wants answered.
type AskUserQuestion struct {
	Question    string   `json:"question"`
	Options     []string `json:"options,omitempty"`
	MultiSelect bool     `json:"multiSelect,omitempty"`
}

// AskUserRequest is handed to OnAskUser when the model invokes the ask_user tool.
type AskUserRequest struct {
	ToolCallID string            `json:"toolCallId"`
	Header     string            `json:"header,omitempty"`
	Questions  []AskUserQuestion `json:"questions"`
}

// AskUserResponse carries the user's answers back to the loop, one per question.
type AskUserResponse struct {
	Answers []string `json:"answers"`
}

type Options struct {
	MaxTurns int
	// Specialist/sub-agent metadata is carried through exec now and consumed by
	// the specialist runtime in later slices.
	SessionID        string
	CallingSessionID string
	CallingToolUseID string
	Tag              string
	Depth            int
	SessionTitle     string
	Model            string
	ReasoningEffort  string
	Cwd              string
	// ContextWindow is the model's maximum input token budget. When > 0 the agent
	// loop compacts long conversations once the estimated size crosses a fraction
	// of this window. 0 DISABLES compaction entirely (every existing caller/test
	// behaves identically).
	ContextWindow int
	// CompactionPreserveLast is how many trailing messages compaction keeps
	// verbatim. <= 0 falls back to defaultCompactionPreserveLast.
	CompactionPreserveLast int
	Registry               *tools.Registry
	PermissionMode         PermissionMode
	Autonomy               string
	Sandbox                *sandbox.Engine
	EnabledTools           []string
	DisabledTools          []string
	OnText                 func(string)
	OnToolCall             func(ToolCall)
	OnPermissionRequest    func(context.Context, PermissionRequest) (PermissionDecision, error)
	OnPermission           func(PermissionEvent)
	OnAskUser              func(context.Context, AskUserRequest) (AskUserResponse, error)
	OnToolResult           func(ToolResult)
	OnUsage                func(Usage)
	// OnContext, when set, is called once per turn with the per-category context
	// budget of the request about to be sent, so a surface (TUI/CLI) can show
	// context utilization. Opt-in like the other callbacks; nil is a no-op.
	OnContext func(ContextBreakdown)
}

type Result struct {
	FinalAnswer string
	Turns       int
	Messages    []Message
}
