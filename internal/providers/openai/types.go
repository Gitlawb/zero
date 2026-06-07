package openai

type chatCompletionRequest struct {
	Model               string           `json:"model"`
	Messages            []chatMessage    `json:"messages"`
	Tools               []toolDefinition `json:"tools,omitempty"`
	MaxCompletionTokens int              `json:"max_completion_tokens,omitempty"`
	Stream              bool             `json:"stream"`
	StreamOptions       *streamOptions   `json:"stream_options,omitempty"`
}

// streamOptions requests the final usage chunk on a streaming response. Without
// include_usage the OpenAI streaming API never sends the usage object, so token
// accounting is silently zero for real OpenAI streams.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []requestToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type requestToolCall struct {
	ID       string                  `json:"id"`
	Type     string                  `json:"type"`
	Function requestToolCallFunction `json:"function"`
}

type requestToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolDefinition struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type streamChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *usage         `json:"usage"`
	Error   *apiError      `json:"error"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type streamDelta struct {
	Content   string                `json:"content"`
	ToolCalls []streamToolCallDelta `json:"tool_calls"`
}

type streamToolCallDelta struct {
	Index    int                 `json:"index"`
	ID       string              `json:"id"`
	Function streamFunctionDelta `json:"function"`
}

type streamFunctionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type usage struct {
	PromptTokens        int                `json:"prompt_tokens"`
	CompletionTokens    int                `json:"completion_tokens"`
	PromptTokensDetails promptTokenDetails `json:"prompt_tokens_details"`
}

type promptTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}
