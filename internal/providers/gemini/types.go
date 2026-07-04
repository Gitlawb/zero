package gemini

type generateContentRequest struct {
	SystemInstruction *geminiContent    `json:"systemInstruction,omitempty"`
	Contents          []geminiContent   `json:"contents"`
	GenerationConfig  generationConfig  `json:"generationConfig"`
	Tools             []geminiToolGroup `json:"tools,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens int             `json:"maxOutputTokens"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

// thinkingConfig requests a thinking token budget. It is omitted (nil) for
// normal requests so behavior is unchanged, and is only set when the effort maps
// to a positive budget — so Gemini is never sent {"thinkingBudget":0}. There is
// no IncludeThoughts request field here; thought summaries are suppressed on the
// response side by skipping streamed parts whose part.Thought is true.
type thinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	// ThoughtSignature replays the reasoning signature Gemini bound to a
	// functionCall, required for multi-turn function calling with thinking.
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolGroup struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type streamPayload struct {
	Candidates     []candidate     `json:"candidates"`
	FunctionCalls  []functionCall  `json:"functionCalls"`
	PromptFeedback *promptFeedback `json:"promptFeedback"`
	UsageMetadata  *usageMetadata  `json:"usageMetadata"`
	Error          *apiError       `json:"error"`
}

type candidate struct {
	Content      *candidateContent `json:"content"`
	FinishReason string            `json:"finishReason"`
}

type candidateContent struct {
	Role  string       `json:"role"`
	Parts []streamPart `json:"parts"`
}

type streamPart struct {
	Text             string        `json:"text"`
	FunctionCall     *functionCall `json:"functionCall"`
	ThoughtSignature string        `json:"thoughtSignature"`
	Thought          bool          `json:"thought"`
}

type functionCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Args      any    `json:"args"`
	Arguments any    `json:"arguments"`
}

type promptFeedback struct {
	BlockReason        string `json:"blockReason"`
	BlockReasonMessage string `json:"blockReasonMessage"`
}

type usageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}
