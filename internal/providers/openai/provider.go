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
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
	UserAgent  string
	// StreamIdleTimeout aborts the stream if no data arrives for this long.
	// Zero uses defaultStreamIdleTimeout.
	StreamIdleTimeout time.Duration
}

// Provider streams completions from an OpenAI-compatible chat completions API.
type Provider struct {
	apiKey            string
	baseURL           string
	model             string
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

	return &Provider{
		apiKey:            options.APIKey,
		baseURL:           baseURL,
		model:             model,
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

	// Retry transient failures (network errors and 5xx) before surfacing them.
	// Hosted OpenAI-compatible gateways (e.g. Ollama Cloud) return intermittent
	// 500s that succeed on a quick retry.
	const maxAttempts = 3
	var response *http.Response
	for attempt := 1; ; attempt++ {
		request, err := http.NewRequestWithContext(streamCtx, http.MethodPost, endpoint, bytes.NewReader(body))
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

		resp, err := provider.httpClient.Do(request)
		if err != nil {
			if attempt < maxAttempts && ctx.Err() == nil && backoff(ctx, attempt) {
				continue
			}
			sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + err.Error())})
			return
		}
		if resp.StatusCode >= http.StatusInternalServerError && attempt < maxAttempts && backoff(ctx, attempt) {
			_ = resp.Body.Close()
			continue
		}
		response = resp
		break
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		provider.emitHTTPError(ctx, response, events)
		return
	}

	state := newToolState()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 4096), 16*1024*1024)

	// Read SSE lines on a dedicated goroutine so the consumer below can enforce
	// an idle deadline with a select. The reader exits when the body EOFs/errors
	// or when streamCtx is cancelled (idle abort), then closes lines.
	lines := make(chan string)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-streamCtx.Done():
				return
			}
		}
	}()

	idle := time.NewTimer(provider.streamIdleTimeout)
	defer idle.Stop()

	for {
		select {
		case <-ctx.Done():
			state.closeOpen(ctx, events)
			sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventError, Error: provider.redact("provider stream error: " + ctx.Err().Error())})
			return
		case <-idle.C:
			// Upstream went silent without closing. Abort the read and surface a
			// timeout instead of blocking the agent forever.
			cancelStream()
			state.closeOpen(ctx, events)
			sendEvent(ctx, events, zeroruntime.StreamEvent{
				Type:  zeroruntime.StreamEventError,
				Error: provider.redact(fmt.Sprintf("provider stream error: idle timeout after %s (upstream stopped sending data)", provider.streamIdleTimeout)),
			})
			return
		case raw, ok := <-lines:
			if !ok {
				// Reader finished: EOF, scanner error, or context cancel.
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
				return
			}

			// Any line is activity: refresh the idle deadline.
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(provider.streamIdleTimeout)

			line := strings.TrimSpace(raw)
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
	}
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
	return providerio.ClassifiedError(statusCode, message, provider.apiKey)
}

func (provider *Provider) redact(message string) string {
	return providerio.Redact(message, provider.apiKey)
}

// backoff waits before a retry attempt, returning false if the context is
// cancelled while waiting.
func backoff(ctx context.Context, attempt int) bool {
	timer := time.NewTimer(time.Duration(attempt) * 400 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
