package agent

import (
	"context"

	"github.com/Gitlawb/zero/internal/hooks"
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
	PermissionModeAuto      PermissionMode = "auto"
	PermissionModeAsk       PermissionMode = "ask"
	PermissionModeUnsafe    PermissionMode = "unsafe"
	PermissionModeSpecDraft PermissionMode = "spec-draft"
)

type StopReason string

const (
	StopReasonSpecReviewRequired StopReason = "spec_review_required"
)

const (
	PermissionActionAllow  PermissionAction = "allow"
	PermissionActionPrompt PermissionAction = "prompt"
	PermissionActionDeny   PermissionAction = "deny"
	PermissionActionCancel PermissionAction = "cancel"
)

const (
	PermissionDecisionAllow           PermissionDecisionAction = "allow"
	PermissionDecisionAllowStrict     PermissionDecisionAction = "allow_with_strict_auto_review"
	PermissionDecisionAllowForSession PermissionDecisionAction = "allow_for_session"
	PermissionDecisionAllowPrefix     PermissionDecisionAction = "allow_prefix_for_session"
	PermissionDecisionDeny            PermissionDecisionAction = "deny"
	PermissionDecisionAlwaysAllow     PermissionDecisionAction = "always_allow"
	PermissionDecisionCancel          PermissionDecisionAction = "cancel"
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
	// LoadedTools carries the deferred-tool names a tool_search call asked the
	// loop to expose next turn (lifted from Meta["load_tools"]). nil for every
	// ordinary tool result; only tool_search populates it.
	LoadedTools []string
	// RequestedModel is the model id a tool asked the loop to switch to for the
	// rest of the run (lifted from the tool's Meta["escalate_to_model"]). Empty
	// for every normal tool result; the Run loop performs the switch when it is
	// set and Options.ModelSwitcher is wired.
	RequestedModel string
}

// DenialCategory classifies why a tool call was blocked before it executed.
type DenialCategory string

const (
	DenialNone             DenialCategory = ""
	DenialFiltered         DenialCategory = "filtered"          // tool not enabled for this run
	DenialPermissionDenied DenialCategory = "permission_denied" // approval declined
	DenialApprovalCanceled DenialCategory = "approval_canceled" // approval canceled and run aborted
	DenialSandboxViolation DenialCategory = "sandbox_violation" // blocked by the sandbox
	DenialHookBlocked      DenialCategory = "hook_blocked"      // vetoed by a beforeTool hook
)

type PermissionRequest struct {
	ToolCallID         string                     `json:"toolCallId"`
	ToolName           string                     `json:"name"`
	Action             PermissionAction           `json:"action"`
	Permission         string                     `json:"permission"`
	PermissionMode     PermissionMode             `json:"permissionMode"`
	Autonomy           string                     `json:"autonomy,omitempty"`
	SideEffect         string                     `json:"sideEffect"`
	Reason             string                     `json:"reason,omitempty"`
	Scope              string                     `json:"scope,omitempty"`
	Risk               sandbox.Risk               `json:"risk"`
	Args               map[string]any             `json:"args,omitempty"`
	Violation          *sandbox.Violation         `json:"violation,omitempty"`
	GrantMatched       bool                       `json:"grantMatched,omitempty"`
	Grant              *sandbox.Grant             `json:"grant,omitempty"`
	CommandPrefix      []string                   `json:"commandPrefix,omitempty"`
	AvailableDecisions []PermissionDecisionAction `json:"availableDecisions,omitempty"`
}

type PermissionDecision struct {
	Action PermissionDecisionAction `json:"action"`
	Reason string                   `json:"reason,omitempty"`
}

type PermissionEvent struct {
	ToolCallID        string                   `json:"toolCallId"`
	ToolName          string                   `json:"name"`
	Action            PermissionAction         `json:"action"`
	DecisionAction    PermissionDecisionAction `json:"decisionAction,omitempty"`
	Permission        string                   `json:"permission"`
	PermissionGranted bool                     `json:"permissionGranted,omitempty"`
	PermissionMode    PermissionMode           `json:"permissionMode"`
	Autonomy          string                   `json:"autonomy,omitempty"`
	SideEffect        string                   `json:"sideEffect"`
	Reason            string                   `json:"reason,omitempty"`
	Scope             string                   `json:"scope,omitempty"`
	DecisionReason    string                   `json:"decisionReason,omitempty"`
	Risk              sandbox.Risk             `json:"risk"`
	Violation         *sandbox.Violation       `json:"violation,omitempty"`
	GrantMatched      bool                     `json:"grantMatched,omitempty"`
	Grant             *sandbox.Grant           `json:"grant,omitempty"`
	CommandPrefix     []string                 `json:"commandPrefix,omitempty"`
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

// SpecialistInfo is a one-line summary of a delegatable sub-agent (its name and
// when-to-use description) surfaced to the orchestrator's system prompt so it can
// route work to the right specialist. It is plain data so the agent package needs
// no dependency on internal/specialist.
type SpecialistInfo struct {
	Name      string
	WhenToUse string
}

type Options struct {
	MaxTurns int
	// DeferThreshold activates deferred MCP-tool loading: when the number of
	// deferred-eligible visible tools is >= this value (and it is > 0), their
	// full schemas are withheld and advertised as compact lines via tool_search.
	// 0 (or below the eligible count) keeps every tool eager — byte-identical to
	// the pre-deferral behavior.
	DeferThreshold int
	// Specialists lists the sub-agents the orchestrator may delegate to via the
	// Task tool; when non-empty the system prompt gains a delegation section that
	// names them and nudges the model to offload read-heavy work (search,
	// exploration) so verbose tool output stays out of the main context. It is
	// populated only where the Task tool is actually registered, so an empty slice
	// (the default) reproduces the previous prompt byte-for-byte.
	Specialists []SpecialistInfo
	// Specialist/sub-agent metadata is carried through exec now and consumed by
	// the specialist runtime in later slices.
	SessionID        string
	CallingSessionID string
	CallingToolUseID string
	Tag              string
	Depth            int
	SessionTitle     string
	ProviderName     string
	Model            string
	ReasoningEffort  string
	Cwd              string
	SystemPrompt     string
	// Images are optional image attachments to seed onto the initial user turn.
	// nil for text-only runs (the seeded message then carries no images, exactly
	// as before).
	Images []zeroruntime.ImageBlock
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
	// FileTracker records per-session file read/write versions so the write tools
	// can detect a file changed on disk outside Zero since it was last read. nil
	// disables the check. Created once per session and threaded into every tool run.
	FileTracker *tools.FileTracker
	// Hooks, when set, runs configured beforeTool (blocking) and afterTool
	// (advisory) commands around each tool call. nil disables hooks entirely; a
	// dispatcher built from an empty config is also a safe no-op.
	Hooks               *hooks.Dispatcher
	EnabledTools        []string
	DisabledTools       []string
	OnText              func(string)
	OnReasoning         func(string)
	OnToolCall          func(ToolCall)
	OnPermissionRequest func(context.Context, PermissionRequest) (PermissionDecision, error)
	OnPermission        func(PermissionEvent)
	OnAskUser           func(context.Context, AskUserRequest) (AskUserResponse, error)
	OnToolResult        func(ToolResult)
	OnUsage             func(Usage)
	// OnContext, when set, is called once per turn with the per-category context
	// budget of the request about to be sent, so a surface (TUI/CLI) can show
	// context utilization. Opt-in like the other callbacks; nil is a no-op.
	OnContext func(ContextBreakdown)
	// ModelSwitcher, when set, lets a tool escalate the run to a stronger model
	// mid-run: the loop calls it with the requested model id and, on success,
	// swaps the active provider and updates Options.Model for the rest of the
	// run. nil DISABLES escalation entirely (the loop ignores any switch
	// request), so every existing caller is unaffected. A returned error is
	// non-fatal: the run continues on the current model.
	ModelSwitcher func(ctx context.Context, modelID string) (Provider, error)
	// SelfCorrect, when set, runs a post-edit verify-and-correct cycle after a
	// mutating tool call: it verifies the changed files (LSP diagnostics + project
	// tests) and feeds failures back to the model to fix, bounded by an attempt
	// ceiling and the autonomy gate. nil disables it entirely (the loop is
	// byte-identical to before). One instance per run — it holds attempt state.
	SelfCorrect *SelfCorrector

	runPermissions *permissionRunState
}

type Result struct {
	FinalAnswer string
	Turns       int
	Messages    []Message
	StopReason  StopReason
	// FinishReason is the provider's normalized terminal stop reason for the turn
	// that produced FinalAnswer: zeroruntime.FinishReasonLength when the output
	// hit the token cap, FinishReasonContentFilter when it was filtered. Empty for
	// a normal completion.
	FinishReason string
}

// Truncated reports whether the final response ended abnormally (cut off at the
// output token cap or withheld by a content filter) rather than completing
// naturally. Callers can use it to warn the user that FinalAnswer is incomplete.
func (result Result) Truncated() bool {
	return result.FinishReason != ""
}

// TruncationNotice returns a user-facing warning when the final response was
// truncated, or "" for a normal completion. Shared by the CLI and TUI so the
// wording stays consistent.
func (result Result) TruncationNotice() string {
	switch result.FinishReason {
	case zeroruntime.FinishReasonLength:
		return "Response was cut off at the output token limit and may be incomplete. " +
			"Raise the model's max output tokens or ask zero to continue."
	case zeroruntime.FinishReasonContentFilter:
		return "Response was withheld or cut off by the provider's content filter and may be incomplete."
	case "":
		return ""
	default:
		return "Response ended early (" + result.FinishReason + ") and may be incomplete."
	}
}
