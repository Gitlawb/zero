package zeroruntime

import "context"

// MessageRole identifies the origin of a conversation message.
type MessageRole string

// StreamEventType identifies one event in a provider completion stream.
type StreamEventType string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

const (
	StreamEventText          StreamEventType = "text"
	StreamEventToolCallStart StreamEventType = "tool-call-start"
	StreamEventToolCallDelta StreamEventType = "tool-call-delta"
	StreamEventToolCallEnd   StreamEventType = "tool-call-end"
	StreamEventUsage         StreamEventType = "usage"
	StreamEventDone          StreamEventType = "done"
	StreamEventError         StreamEventType = "error"
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

// Usage records normalized token accounting reported by a provider.
type Usage struct {
	PromptTokens      int
	CompletionTokens  int
	CachedInputTokens int
}

// TotalTokens returns prompt plus completion tokens.
func (usage Usage) TotalTokens() int {
	return usage.PromptTokens + usage.CompletionTokens
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
