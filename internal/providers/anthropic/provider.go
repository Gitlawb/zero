package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/providers/providerio"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const defaultBaseURL = "https://api.anthropic.com"
const defaultVersion = "2023-06-01"
const defaultMaxTokens = 4096

// defaultStreamIdleTimeout aborts a streaming read when the upstream goes silent
// without closing the connection, so a stalled-but-open upstream cannot hang the
// agent forever.
const defaultStreamIdleTimeout = 90 * time.Second

// Options configures an Anthropic Messages API provider.
type Options struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
	Version    string
	Beta       string
	HTTPClient *http.Client
	UserAgent  string
	// StreamIdleTimeout aborts the stream if no data arrives for this long.
	// Zero uses defaultStreamIdleTimeout.
	StreamIdleTimeout time.Duration
}

// Provider streams completions from Anthropic's Messages API.
type Provider struct {
	apiKey            string
	baseURL           string
	model             string
	maxTokens         int
	version           string
	beta              string
	httpClient        *http.Client
	userAgent         string
	streamIdleTimeout time.Duration
}

// New creates an Anthropic provider.
func New(options Options) (*Provider, error) {
	model := strings.TrimSpace(options.Model)
	if model == "" {
		return nil, errors.New("anthropic provider requires a model")
	}
	maxTokens, err := providerio.PositiveOrDefault(options.MaxTokens, defaultMaxTokens, "zero Anthropic provider maxTokens")
	if err != nil {
		return nil, err
	}
	baseURL, err := providerio.NormalizeBaseURL(options.BaseURL, defaultBaseURL, "Anthropic")
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(options.Version)
	if version == "" {
		version = defaultVersion
	}
	idleTimeout := options.StreamIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultStreamIdleTimeout
	}
	return &Provider{
		apiKey:            options.APIKey,
		baseURL:           baseURL,
		model:             model,
		maxTokens:         maxTokens,
		version:           version,
		beta:              strings.TrimSpace(options.Beta),
		httpClient:        providerio.HTTPClient(options.HTTPClient),
		userAgent:         options.UserAgent,
		streamIdleTimeout: idleTimeout,
	}, nil
}

// StreamCompletion sends one streaming Anthropic Messages request.
func (provider *Provider) StreamCompletion(
	ctx context.Context,
	request zeroruntime.CompletionRequest,
) (<-chan zeroruntime.StreamEvent, error) {
	mapped, err := provider.anthropicRequest(request)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(mapped)
	if err != nil {
		return nil, fmt.Errorf("encode Anthropic request: %w", err)
	}

	events := make(chan zeroruntime.StreamEvent, 16)
	go func() {
		defer close(events)
		provider.stream(ctx, body, events)
	}()
	return events, nil
}

func (provider *Provider) stream(ctx context.Context, body []byte, events chan<- zeroruntime.StreamEvent) {
	// streamCtx lets the idle watchdog abort an in-flight body read by cancelling
	// the request, which unblocks the SSE reader goroutine.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	httpRequest, err := http.NewRequestWithContext(streamCtx, http.MethodPost, provider.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider request error: " + err.Error())})
		return
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("anthropic-version", provider.version)
	if provider.apiKey != "" {
		httpRequest.Header.Set("x-api-key", provider.apiKey)
	}
	if provider.beta != "" {
		httpRequest.Header.Set("anthropic-beta", provider.beta)
	}
	if provider.userAgent != "" {
		httpRequest.Header.Set("User-Agent", provider.userAgent)
	}

	response, err := provider.httpClient.Do(httpRequest)
	if err != nil {
		providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
		return
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		provider.emitHTTPError(ctx, response, events)
		return
	}

	state := newStreamState()
	err = providerio.ScanSSEDataWithContext(streamCtx, cancelStream, response.Body, provider.streamIdleTimeout, func(data string) bool {
		return provider.emitPayload(ctx, data, state, events)
	})
	if errors.Is(err, providerio.ErrStreamIdle) {
		state.closeOpen(ctx, events)
		providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:  zeroruntime.StreamEventError,
			Error: provider.redact(fmt.Sprintf("provider stream error: idle timeout after %s (upstream stopped sending data)", provider.streamIdleTimeout)),
		})
		return
	}
	if err != nil {
		state.closeOpen(ctx, events)
		providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
		return
	}
	if err := ctx.Err(); err != nil {
		state.closeOpen(ctx, events)
		providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
		return
	}
	if !state.done {
		provider.emitDone(ctx, state, events)
	}
}

func (provider *Provider) emitPayload(ctx context.Context, data string, state *streamState, events chan<- zeroruntime.StreamEvent) bool {
	var payload streamPayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		state.closeOpen(ctx, events)
		providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:  zeroruntime.StreamEventError,
			Error: provider.redact("provider stream error: malformed JSON: " + err.Error()),
		})
		state.done = true
		return false
	}

	switch payload.Type {
	case "message_start":
		if payload.Message != nil {
			state.recordUsage(payload.Message.Usage)
		}
	case "content_block_start":
		if payload.ContentBlock != nil && payload.ContentBlock.Type == "tool_use" {
			if payload.ContentBlock.ID == "" || payload.ContentBlock.Name == "" {
				// A tool_use block without a usable id/name can't be dispatched.
				// Signal a drop once so the agent can ask the model to retry
				// instead of silently ending the turn (mirrors OpenAI).
				providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDropped})
				return true
			}
			state.startTool(ctx, payload.Index, payload.ContentBlock.ID, payload.ContentBlock.Name, events)
			if len(payload.ContentBlock.Input) > 0 {
				encoded, err := json.Marshal(payload.ContentBlock.Input)
				if err == nil {
					state.deltaTool(ctx, payload.Index, string(encoded), events)
				}
			}
		}
	case "content_block_delta":
		if payload.Delta == nil {
			return true
		}
		switch payload.Delta.Type {
		case "text_delta":
			if payload.Delta.Text != "" {
				providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: payload.Delta.Text})
			}
		case "input_json_delta":
			if payload.Delta.PartialJSON != "" {
				state.deltaTool(ctx, payload.Index, payload.Delta.PartialJSON, events)
			}
		}
	case "content_block_stop":
		state.stopTool(ctx, payload.Index, events)
	case "message_delta":
		if payload.Usage != nil {
			state.recordUsage(*payload.Usage)
		}
		if payload.Delta != nil {
			if reason := mapStopReason(payload.Delta.StopReason); reason != "" {
				state.finishReason = reason
			}
		}
	case "message_stop":
		provider.emitDone(ctx, state, events)
	case "error":
		message := "Anthropic stream error"
		if payload.Error != nil {
			message = firstNonEmpty(payload.Error.Message, payload.Error.Type, message)
		}
		state.closeOpen(ctx, events)
		providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:  zeroruntime.StreamEventError,
			Error: provider.classifiedError(http.StatusInternalServerError, message),
		})
		state.done = true
		return false
	}
	return true
}

func (provider *Provider) emitDone(ctx context.Context, state *streamState, events chan<- zeroruntime.StreamEvent) {
	state.closeOpen(ctx, events)
	if state.hasInputUsage || state.hasOutputUsage {
		usage, err := zeroruntime.NormalizeUsage(zeroruntime.TokenUsage{
			InputTokens:  state.inputTokens,
			OutputTokens: state.outputTokens,
		})
		if err == nil {
			providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventUsage, Usage: usage})
		}
	}
	providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone, FinishReason: state.finishReason})
	state.done = true
}

func (provider *Provider) emitHTTPError(ctx context.Context, response *http.Response, events chan<- zeroruntime.StreamEvent) {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	message := response.Status
	if parsed := parseErrorMessage(body); parsed != "" {
		message = parsed
	} else if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		message = trimmed
	}
	providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{
		Type:  zeroruntime.StreamEventError,
		Error: provider.classifiedError(response.StatusCode, message),
	})
}

func (provider *Provider) anthropicRequest(request zeroruntime.CompletionRequest) (messagesRequest, error) {
	system, messages, err := mapMessages(request.Messages)
	if err != nil {
		return messagesRequest{}, err
	}
	if len(messages) == 0 {
		return messagesRequest{}, errors.New("zero Anthropic provider requires at least one non-system message")
	}

	mapped := messagesRequest{
		Model:     provider.model,
		MaxTokens: provider.maxTokens,
		Messages:  messages,
		System:    system,
		Stream:    true,
	}
	if len(request.Tools) > 0 {
		mapped.Tools = make([]anthropicTool, 0, len(request.Tools))
		for _, tool := range request.Tools {
			mapped.Tools = append(mapped.Tools, anthropicTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.Parameters,
			})
		}
	}
	return mapped, nil
}

func mapMessages(messages []zeroruntime.Message) (string, []anthropicMessage, error) {
	systemParts := []string{}
	mapped := []anthropicMessage{}
	for _, message := range messages {
		content := message.Content
		hasContent := strings.TrimSpace(content) != ""
		switch message.Role {
		case zeroruntime.MessageRoleSystem:
			if hasContent {
				systemParts = append(systemParts, content)
			}
		case zeroruntime.MessageRoleTool:
			if message.ToolCallID == "" {
				return "", nil, errors.New("zero Anthropic provider requires toolCallId on tool result messages")
			}
			appendUserBlocks(&mapped, []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": message.ToolCallID,
				"content":     content,
			}})
		case zeroruntime.MessageRoleAssistant:
			blocks := []map[string]any{}
			if hasContent {
				blocks = append(blocks, map[string]any{"type": "text", "text": content})
			}
			for _, toolCall := range message.ToolCalls {
				input, err := parseToolArguments(toolCall.Arguments, toolCall.Name)
				if err != nil {
					return "", nil, err
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    toolCall.ID,
					"name":  toolCall.Name,
					"input": input,
				})
			}
			if len(blocks) == 0 {
				continue
			}
			var messageContent any = blocks
			if len(blocks) == 1 && blocks[0]["type"] == "text" {
				messageContent = blocks[0]["text"]
			}
			mapped = append(mapped, anthropicMessage{Role: "assistant", Content: messageContent})
		default:
			if hasContent {
				appendUserBlocks(&mapped, []map[string]any{{"type": "text", "text": content}})
			}
		}
	}
	return strings.Join(systemParts, "\n\n"), mapped, nil
}

func appendUserBlocks(messages *[]anthropicMessage, blocks []map[string]any) {
	if len(*messages) > 0 && (*messages)[len(*messages)-1].Role == "user" {
		last := &(*messages)[len(*messages)-1]
		last.Content = append(contentBlocks(last.Content), blocks...)
		return
	}
	*messages = append(*messages, anthropicMessage{Role: "user", Content: blocks})
}

func contentBlocks(content any) []map[string]any {
	if content == nil {
		return nil
	}
	if text, ok := content.(string); ok {
		return []map[string]any{{"type": "text", "text": text}}
	}
	if blocks, ok := content.([]map[string]any); ok {
		return blocks
	}
	return nil
}

func parseToolArguments(argumentsJSON string, toolName string) (map[string]any, error) {
	if strings.TrimSpace(argumentsJSON) == "" {
		return map[string]any{}, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(argumentsJSON), &parsed); err != nil {
		return nil, fmt.Errorf("zero Anthropic provider could not parse tool arguments for %s as JSON", toolName)
	}
	object, ok := parsed.(map[string]any)
	if !ok || object == nil {
		return nil, fmt.Errorf("zero Anthropic provider requires tool arguments for %s to be a JSON object", toolName)
	}
	return object, nil
}

func parseErrorMessage(body []byte) string {
	var parsed struct {
		Error   apiError `json:"error"`
		Message string   `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return firstNonEmpty(parsed.Error.Message, parsed.Message)
}

func (provider *Provider) classifiedError(statusCode int, message string) string {
	return providerio.ClassifiedError(statusCode, message, provider.apiKey)
}

func (provider *Provider) redact(message string) string {
	return providerio.Redact(message, provider.apiKey)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

type toolBlock struct {
	id   string
	name string
}

type streamState struct {
	tools          map[int]toolBlock
	inputTokens    int
	outputTokens   int
	hasInputUsage  bool
	hasOutputUsage bool
	finishReason   string // normalized terminal stop reason (empty for normal stop)
	done           bool
}

// mapStopReason maps Anthropic's message_delta stop_reason onto the runtime's
// normalized terminal reasons. A normal stop ("end_turn"/"tool_use"/"stop_sequence"/"")
// returns "".
func mapStopReason(reason string) string {
	if reason == "max_tokens" {
		return zeroruntime.FinishReasonLength
	}
	return ""
}

func newStreamState() *streamState {
	return &streamState{tools: make(map[int]toolBlock)}
}

func (state *streamState) recordUsage(usage usage) {
	if usage.InputTokens != 0 {
		state.inputTokens = usage.InputTokens
		state.hasInputUsage = true
	}
	if usage.OutputTokens != 0 {
		state.outputTokens = usage.OutputTokens
		state.hasOutputUsage = true
	}
}

func (state *streamState) startTool(ctx context.Context, index int, id string, name string, events chan<- zeroruntime.StreamEvent) {
	state.tools[index] = toolBlock{id: id, name: name}
	providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{
		Type:       zeroruntime.StreamEventToolCallStart,
		ToolCallID: id,
		ToolName:   name,
	})
}

func (state *streamState) deltaTool(ctx context.Context, index int, fragment string, events chan<- zeroruntime.StreamEvent) {
	tool, ok := state.tools[index]
	if !ok || fragment == "" {
		return
	}
	providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{
		Type:              zeroruntime.StreamEventToolCallDelta,
		ToolCallID:        tool.id,
		ArgumentsFragment: fragment,
	})
}

func (state *streamState) stopTool(ctx context.Context, index int, events chan<- zeroruntime.StreamEvent) {
	tool, ok := state.tools[index]
	if !ok {
		return
	}
	providerio.SendEvent(ctx, events, zeroruntime.StreamEvent{
		Type:       zeroruntime.StreamEventToolCallEnd,
		ToolCallID: tool.id,
	})
	delete(state.tools, index)
}

func (state *streamState) closeOpen(ctx context.Context, events chan<- zeroruntime.StreamEvent) {
	for index := range state.tools {
		state.stopTool(ctx, index, events)
	}
}
