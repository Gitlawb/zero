package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Options configures an OpenAI-compatible chat completions provider.
type Options struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
	UserAgent  string
}

// Provider streams completions from an OpenAI-compatible chat completions API.
type Provider struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
	userAgent  string
}

// New creates an OpenAI-compatible provider.
func New(options Options) (*Provider, error) {
	model := strings.TrimSpace(options.Model)
	if model == "" {
		return nil, errors.New("openai provider requires a model")
	}

	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid OpenAI base URL: %w", err)
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Provider{
		apiKey:     options.APIKey,
		baseURL:    baseURL,
		model:      model,
		httpClient: httpClient,
		userAgent:  options.UserAgent,
	}, nil
}

// StreamCompletion sends one streaming chat completion request.
func (provider *Provider) StreamCompletion(
	ctx context.Context,
	request zeroruntime.CompletionRequest,
) (<-chan zeroruntime.StreamEvent, error) {
	body, err := json.Marshal(provider.openAIRequest(request))
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI request: %w", err)
	}

	events := make(chan zeroruntime.StreamEvent, 16)
	go func() {
		defer close(events)
		provider.stream(ctx, body, events)
	}()

	return events, nil
}

func (provider *Provider) stream(ctx context.Context, body []byte, events chan<- zeroruntime.StreamEvent) {
	endpoint := provider.baseURL + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider request error: " + err.Error())})
		return
	}
	request.Header.Set("Content-Type", "application/json")
	if provider.userAgent != "" {
		request.Header.Set("User-Agent", provider.userAgent)
	}
	if provider.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+provider.apiKey)
	}

	response, err := provider.httpClient.Do(request)
	if err != nil {
		sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
		return
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		provider.emitHTTPError(ctx, response, events)
		return
	}

	state := newToolState()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 4096), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			state.closeOpen(ctx, events)
			sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone})
			return
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			state.closeOpen(ctx, events)
			sendEvent(ctx, events, zeroruntime.StreamEvent{
				Type:  zeroruntime.StreamEventError,
				Error: provider.redact("provider stream error: malformed JSON: " + err.Error()),
			})
			return
		}
		if chunk.Error != nil {
			state.closeOpen(ctx, events)
			sendEvent(ctx, events, zeroruntime.StreamEvent{
				Type:  zeroruntime.StreamEventError,
				Error: provider.classifiedError(http.StatusInternalServerError, chunk.Error.Message),
			})
			return
		}
		provider.emitChunk(ctx, chunk, state, events)
	}

	state.closeOpen(ctx, events)
	if err := scanner.Err(); err != nil {
		sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
		return
	}
	if err := ctx.Err(); err != nil {
		sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
		return
	}
	sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone})
}

func (provider *Provider) emitChunk(
	ctx context.Context,
	chunk streamChunk,
	state *toolState,
	events chan<- zeroruntime.StreamEvent,
) {
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			sendEvent(ctx, events, zeroruntime.StreamEvent{
				Type:    zeroruntime.StreamEventText,
				Content: choice.Delta.Content,
			})
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			state.applyDelta(ctx, toolCall, events)
		}
		if choice.FinishReason == "tool_calls" {
			state.closeOpen(ctx, events)
		}
	}

	if chunk.Usage != nil {
		sendEvent(ctx, events, zeroruntime.StreamEvent{
			Type: zeroruntime.StreamEventUsage,
			Usage: zeroruntime.Usage{
				PromptTokens:      chunk.Usage.PromptTokens,
				CompletionTokens:  chunk.Usage.CompletionTokens,
				CachedInputTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
			},
		})
	}
}

func (provider *Provider) emitHTTPError(ctx context.Context, response *http.Response, events chan<- zeroruntime.StreamEvent) {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	message := strings.TrimSpace(string(body))
	var parsed struct {
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error.Message != "" {
		message = parsed.Error.Message
	}
	if message == "" {
		message = response.Status
	}
	sendEvent(ctx, events, zeroruntime.StreamEvent{
		Type:  zeroruntime.StreamEventError,
		Error: provider.classifiedError(response.StatusCode, message),
	})
}

func (provider *Provider) classifiedError(statusCode int, message string) string {
	prefix := "provider error: "
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		prefix = "auth error: "
	case http.StatusTooManyRequests:
		prefix = "rate limit error: "
	default:
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			prefix = "provider request error: "
		}
	}
	return provider.redact(prefix + message)
}

func (provider *Provider) redact(message string) string {
	if provider.apiKey != "" {
		message = strings.ReplaceAll(message, provider.apiKey, "[REDACTED]")
	}
	words := strings.Fields(message)
	for index := 0; index < len(words)-1; index++ {
		if strings.EqualFold(strings.TrimRight(words[index], ":"), "Bearer") {
			words[index] = "authorization"
			words[index+1] = "[REDACTED]"
		}
	}
	return strings.Join(words, " ")
}

func sendEvent(ctx context.Context, events chan<- zeroruntime.StreamEvent, event zeroruntime.StreamEvent) {
	select {
	case <-ctx.Done():
		if event.Type == zeroruntime.StreamEventError {
			select {
			case events <- event:
			default:
			}
		}
	case events <- event:
	}
}

func (provider *Provider) openAIRequest(request zeroruntime.CompletionRequest) chatCompletionRequest {
	messages := make([]chatMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, mapMessage(message))
	}

	mapped := chatCompletionRequest{
		Model:    provider.model,
		Messages: messages,
		Stream:   true,
	}
	if len(request.Tools) > 0 {
		mapped.Tools = make([]toolDefinition, 0, len(request.Tools))
		for _, tool := range request.Tools {
			mapped.Tools = append(mapped.Tools, toolDefinition{
				Type: "function",
				Function: toolFunction{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
				},
			})
		}
	}
	return mapped
}

func mapMessage(message zeroruntime.Message) chatMessage {
	mapped := chatMessage{
		Role:       string(message.Role),
		Content:    message.Content,
		ToolCallID: message.ToolCallID,
	}
	if len(message.ToolCalls) > 0 {
		mapped.ToolCalls = make([]requestToolCall, 0, len(message.ToolCalls))
		for _, toolCall := range message.ToolCalls {
			mapped.ToolCalls = append(mapped.ToolCalls, requestToolCall{
				ID:   toolCall.ID,
				Type: "function",
				Function: requestToolCallFunction{
					Name:      toolCall.Name,
					Arguments: toolCall.Arguments,
				},
			})
		}
	}
	return mapped
}
