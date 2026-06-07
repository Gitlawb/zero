package zeroruntime

import "context"

// MessageRole identifies the origin of a conversation message.
type MessageRole string

// StreamEventType identifies one event in a provider completion stream.
type StreamEventType string

// AgentEventType is the stable PRD-level event stream shared by TUI,
// headless output, sessions, and future editor integrations.
type AgentEventType string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

const (
	AgentEventText       AgentEventType = "text"
	AgentEventToolCall   AgentEventType = "tool_call"
	AgentEventToolResult AgentEventType = "tool_result"
	AgentEventThinking   AgentEventType = "thinking"
	AgentEventUsage      AgentEventType = "usage"
	AgentEventPlanUpdate AgentEventType = "plan_update"
	AgentEventError      AgentEventType = "error"
	AgentEventTurnEnd    AgentEventType = "turn_end"
)

const (
	StreamEventText          StreamEventType = "text"
	StreamEventToolCallStart StreamEventType = "tool-call-start"
	StreamEventToolCallDelta StreamEventType = "tool-call-delta"
	StreamEventToolCallEnd   StreamEventType = "tool-call-end"
	// StreamEventToolCallDropped signals the model attempted a tool call that
	// was malformed (no usable name/id) and could not be dispatched. The agent
	// uses this to ask the model to retry instead of silently ending the turn.
	StreamEventToolCallDropped StreamEventType = "tool-call-dropped"
	StreamEventUsage           StreamEventType = "usage"
	StreamEventDone            StreamEventType = "done"
	StreamEventError           StreamEventType = "error"
)

// Normalized terminal finish reasons for responses that did not end normally.
// Providers map their native stop reasons onto these so consumers can detect a
// truncated or filtered response regardless of which provider produced it. A
// normal completion leaves the finish reason empty.
const (
	// FinishReasonLength means the response was truncated at the output token
	// cap (OpenAI finish_reason=="length", Anthropic stop_reason=="max_tokens",
	// Gemini finishReason=="MAX_TOKENS").
	FinishReasonLength = "length"
	// FinishReasonContentFilter means the response was withheld or cut off by a
	// content/safety filter (OpenAI finish_reason=="content_filter",
	// Gemini finishReason=="SAFETY").
	FinishReasonContentFilter = "content_filter"
)

// ToolCall is a normalized assistant request to run a tool.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Message is a normalized conversation turn passed to providers.
type Message struct {
	Role       MessageRole
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// ToolDefinition describes a model-visible tool and its JSON-schema parameters.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// TokenUsage accepts provider-specific token aliases before normalization.
type TokenUsage struct {
	InputTokens       int
	PromptTokens      int
	CachedInputTokens int
	OutputTokens      int
	CompletionTokens  int
	ReasoningTokens   int
}

// Usage records normalized token accounting reported by a provider.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	PromptTokens      int
	CompletionTokens  int
	CachedInputTokens int
	ReasoningTokens   int
}

// TotalTokens returns prompt plus completion tokens.
func (usage Usage) TotalTokens() int {
	return usage.EffectiveInputTokens() + usage.EffectiveOutputTokens() + usage.ReasoningTokens
}

func (usage Usage) EffectiveInputTokens() int {
	if usage.InputTokens != 0 {
		return usage.InputTokens
	}
	return usage.PromptTokens
}

func (usage Usage) EffectiveOutputTokens() int {
	if usage.OutputTokens != 0 {
		return usage.OutputTokens
	}
	return usage.CompletionTokens
}

func (usage Usage) BillableOutputTokens() int {
	return usage.EffectiveOutputTokens() + usage.ReasoningTokens
}

// StreamEvent is one normalized event emitted by a streaming provider.
type StreamEvent struct {
	Type              StreamEventType
	Content           string
	ToolCallID        string
	ToolName          string
	ArgumentsFragment string
	Usage             Usage
	Error             string
	// FinishReason carries the provider's normalized terminal stop reason when a
	// response did not end normally (e.g. FinishReasonLength when the output hit
	// the token cap, or FinishReasonContentFilter when it was filtered). It is
	// empty for a normal completion. Providers set it on the terminal/done event.
	FinishReason string
}

// CompletionRequest groups provider input messages and available tools.
type CompletionRequest struct {
	Messages []Message
	Tools    []ToolDefinition
}

// Provider streams normalized completion events for one request.
type Provider interface {
	StreamCompletion(ctx context.Context, request CompletionRequest) (<-chan StreamEvent, error)
}
