package zeroruntime

import "context"

type MessageRole string
type StreamEventType string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

const (
	StreamEventText          StreamEventType = "text"
	StreamEventToolCallStart StreamEventType = "tool_call_start"
	StreamEventToolCallDelta StreamEventType = "tool_call_delta"
	StreamEventToolCallEnd   StreamEventType = "tool_call_end"
	StreamEventUsage         StreamEventType = "usage"
	StreamEventDone          StreamEventType = "done"
	StreamEventError         StreamEventType = "error"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type Message struct {
	Role       MessageRole
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type Usage struct {
	PromptTokens      int
	CompletionTokens  int
	CachedInputTokens int
}

func (usage Usage) TotalTokens() int {
	return usage.PromptTokens + usage.CompletionTokens
}

type StreamEvent struct {
	Type              StreamEventType
	Content           string
	ToolCallID        string
	ToolName          string
	ArgumentsFragment string
	Usage             Usage
	Error             string
}

type CompletionRequest struct {
	Messages []Message
	Tools    []ToolDefinition
}

type Provider interface {
	StreamCompletion(ctx context.Context, request CompletionRequest) (<-chan StreamEvent, error)
}
