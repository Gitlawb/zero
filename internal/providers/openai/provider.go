package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/providers/providerio"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const defaultBaseURL = "https://api.openai.com/v1"

// defaultStreamIdleTimeout aborts a streaming read when the upstream goes silent
// without closing the connection. Hosted gateways (e.g. Ollama Cloud) sometimes
// stall mid-stream after a tool-call delta; without this the agent blocks forever.
const defaultStreamIdleTimeout = 90 * time.Second

// Options configures an OpenAI-compatible chat completions provider.
type Options struct {
	APIKey          string
	BaseURL         string
	Model           string
	AuthHeader      string
	AuthScheme      string
	AuthHeaderValue string
	CustomHeaders   map[string]string
	HTTPClient      *http.Client
	UserAgent       string
	// MaxTokens caps the model's output tokens. Zero omits the cap (the model's
	// own default applies). Resolved from the model registry by the factory.
	MaxTokens int
	// StreamIdleTimeout aborts the stream if no data arrives for this long.
	// Zero uses defaultStreamIdleTimeout.
	StreamIdleTimeout time.Duration
}

// Provider streams completions from an OpenAI-compatible chat completions API.
type Provider struct {
	apiKey            string
	baseURL           string
	model             string
	authHeader        string
	authScheme        string
	authHeaderValue   string
	customHeaders     map[string]string
	maxTokens         int
	httpClient        *http.Client
	userAgent         string
	streamIdleTimeout time.Duration
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

	idleTimeout := options.StreamIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultStreamIdleTimeout
	}

	maxTokens := options.MaxTokens
	if maxTokens < 0 {
		maxTokens = 0
	}

	return &Provider{
		apiKey:            options.APIKey,
		baseURL:           baseURL,
		model:             model,
		authHeader:        strings.TrimSpace(options.AuthHeader),
		authScheme:        strings.TrimSpace(options.AuthScheme),
		authHeaderValue:   strings.TrimSpace(options.AuthHeaderValue),
		customHeaders:     providerio.CopyHeaders(options.CustomHeaders),
		maxTokens:         maxTokens,
		httpClient:        httpClient,
		userAgent:         options.UserAgent,
		streamIdleTimeout: idleTimeout,
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

	// streamCtx lets the idle watchdog abort an in-flight body read by cancelling
	// the request, rather than closing response.Body directly (which would race
	// with the deferred Close below). Cancelling unblocks the reader goroutine.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	// Retry transient failures (network errors, 429, and 5xx) before surfacing
	// them — hosted gateways return intermittent 500s and rate limits that
	// succeed on a quick retry. Shared with the Anthropic/Gemini providers.
	response, err := providerio.SendWithRetry(streamCtx, provider.httpClient, http.MethodPost, endpoint, body, func(request *http.Request) {
		request.Header.Set("Content-Type", "application/json")
		if provider.userAgent != "" {
			request.Header.Set("User-Agent", provider.userAgent)
		}
		providerio.ApplyAuthHeaders(request, providerio.AuthHeaders{
			APIKey:            provider.apiKey,
			DefaultAuthHeader: "Authorization",
			DefaultAuthScheme: "Bearer",
			AuthHeader:        provider.authHeader,
			AuthScheme:        provider.authScheme,
			AuthHeaderValue:   provider.authHeaderValue,
			CustomHeaders:     provider.customHeaders,
		})
	}, 0)
	if err != nil {
		sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
		return
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		provider.emitHTTPError(ctx, response, events)
		return
	}

	state := newToolState()
	// Use the shared SSE reader (also used by the Anthropic/Gemini providers) so
	// multi-line "data:" continuation fields are joined into one payload, and the
	// idle watchdog / context cancellation are handled uniformly.
	err = providerio.ScanSSEDataWithContext(streamCtx, cancelStream, response.Body, provider.streamIdleTimeout, func(data string) bool {
		return provider.emitPayload(ctx, data, state, events)
	})
	if errors.Is(err, providerio.ErrStreamIdle) {
		state.closeOpen(ctx, events)
		sendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:  zeroruntime.StreamEventError,
			Error: provider.redact(fmt.Sprintf("provider stream error: idle timeout after %s (upstream stopped sending data)", provider.streamIdleTimeout)),
		})
		return
	}
	if err != nil {
		state.closeOpen(ctx, events)
		sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
		return
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		state.closeOpen(ctx, events)
		sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + ctxErr.Error())})
		return
	}
	if !state.done {
		state.closeOpen(ctx, events)
		sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone, FinishReason: state.finishReason})
	}
}

// emitPayload handles one accumulated SSE data payload ([DONE]/blank lines are
// already filtered by the shared reader). It returns false to abort the stream
// after emitting a terminal error.
func (provider *Provider) emitPayload(ctx context.Context, data string, state *toolState, events chan<- zeroruntime.StreamEvent) bool {
	var chunk streamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		state.closeOpen(ctx, events)
		sendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:  zeroruntime.StreamEventError,
			Error: provider.redact("provider stream error: malformed JSON: " + err.Error()),
		})
		state.done = true
		return false
	}
	if chunk.Error != nil {
		state.closeOpen(ctx, events)
		sendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:  zeroruntime.StreamEventError,
			Error: provider.classifiedError(http.StatusInternalServerError, chunk.Error.Message),
		})
		state.done = true
		return false
	}
	provider.emitChunk(ctx, chunk, state, events)
	return true
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
		if reason := mapFinishReason(choice.FinishReason); reason != "" {
			state.finishReason = reason
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

// mapFinishReason maps OpenAI's finish_reason onto the runtime's normalized
// terminal reasons. A normal finish ("stop"/"tool_calls"/"") returns "".
func mapFinishReason(reason string) string {
	switch reason {
	case "length":
		return zeroruntime.FinishReasonLength
	case "content_filter":
		return zeroruntime.FinishReasonContentFilter
	default:
		return ""
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
	return providerio.ClassifiedError(statusCode, message, provider.apiKey, provider.authHeaderValue)
}

func (provider *Provider) redact(message string) string {
	return providerio.Redact(message, provider.apiKey, provider.authHeaderValue)
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
		// Request the terminal usage chunk; OpenAI omits it on streams otherwise,
		// which silently zeroes token accounting.
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	if provider.maxTokens > 0 {
		mapped.MaxCompletionTokens = provider.maxTokens
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
