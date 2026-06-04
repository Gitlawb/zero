package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestStreamCompletionPostsChatCompletionRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotUserAgent string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotUserAgent = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writeSSE(w, `{"choices":[]}`)
		writeSSE(w, `[DONE]`)
	}))
	defer server.Close()

	provider, err := New(Options{
		APIKey:    "sk-secret",
		BaseURL:   server.URL + "/",
		Model:     "gpt-test",
		UserAgent: "zero-test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleSystem, Content: "system"},
			{Role: zeroruntime.MessageRoleUser, Content: "user"},
			{
				Role:    zeroruntime.MessageRoleAssistant,
				Content: "using a tool",
				ToolCalls: []zeroruntime.ToolCall{{
					ID:        "call_1",
					Name:      "read_file",
					Arguments: `{"path":"README.md"}`,
				}},
			},
			{Role: zeroruntime.MessageRoleTool, Content: "contents", ToolCallID: "call_1"},
		},
		Tools: []zeroruntime.ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			Parameters:  map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion returned error: %v", err)
	}
	drain(stream)

	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-secret" {
		t.Fatalf("auth = %q, want bearer token", gotAuth)
	}
	if gotUserAgent != "zero-test" {
		t.Fatalf("user agent = %q, want zero-test", gotUserAgent)
	}
	if gotBody["model"] != "gpt-test" || gotBody["stream"] != true {
		t.Fatalf("unexpected model/stream: %#v", gotBody)
	}
	if _, ok := gotBody["stream_options"]; ok {
		t.Fatalf("stream_options should be omitted in M0 request: %#v", gotBody["stream_options"])
	}
	messages := gotBody["messages"].([]any)
	assistant := messages[2].(map[string]any)
	toolCalls := assistant["tool_calls"].([]any)
	toolCall := toolCalls[0].(map[string]any)
	if toolCall["id"] != "call_1" || toolCall["type"] != "function" {
		t.Fatalf("unexpected assistant tool call: %#v", toolCall)
	}
	function := toolCall["function"].(map[string]any)
	if function["name"] != "read_file" || function["arguments"] != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool call function: %#v", function)
	}
	toolResult := messages[3].(map[string]any)
	if toolResult["role"] != "tool" || toolResult["tool_call_id"] != "call_1" {
		t.Fatalf("unexpected tool result message: %#v", toolResult)
	}
	tools := gotBody["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("unexpected tool wrapper: %#v", tool)
	}
}

func TestNewRequiresModelButNotAPIKey(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New without model returned nil error")
	}
	if _, err := New(Options{Model: "gpt-test"}); err != nil {
		t.Fatalf("New without API key returned error: %v", err)
	}
}

func TestStreamCompletionOmitsAuthAndToolsWhenEmpty(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writeSSE(w, `[DONE]`)
	}))
	defer server.Close()

	provider, err := New(Options{BaseURL: server.URL, Model: "local-model"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion returned error: %v", err)
	}
	drain(stream)

	if gotAuth != "" {
		t.Fatalf("auth = %q, want empty", gotAuth)
	}
	if _, ok := gotBody["tools"]; ok {
		t.Fatalf("tools present for empty tools: %#v", gotBody["tools"])
	}
}

func TestStreamCompletionEmitsTextUsageAndDone(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{"content":"hello "}}]}`)
		writeSSE(w, `{"choices":[{"delta":{"content":"zero"}}],"usage":{"prompt_tokens":12,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":3}}}`)
		writeSSE(w, `[DONE]`)
	})

	events := collectProviderEvents(t, provider)
	assertEvent(t, events[0], zeroruntime.StreamEventText, "hello ")
	assertEvent(t, events[1], zeroruntime.StreamEventText, "zero")
	if events[2].Type != zeroruntime.StreamEventUsage || events[2].Usage.PromptTokens != 12 || events[2].Usage.CompletionTokens != 5 || events[2].Usage.CachedInputTokens != 3 {
		t.Fatalf("unexpected usage event: %#v", events[2])
	}
	if events[3].Type != zeroruntime.StreamEventDone {
		t.Fatalf("last event = %#v, want done", events[3])
	}
}

func TestStreamCompletionBuffersToolArgsUntilIDAndNameArrive(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`)
		writeSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":"\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`)
		writeSSE(w, `[DONE]`)
	})

	events := collectProviderEvents(t, provider)
	if len(events) != 4 {
		t.Fatalf("events = %#v, want start, delta, end, done", events)
	}
	if events[0].Type != zeroruntime.StreamEventToolCallStart || events[0].ToolCallID != "call_1" || events[0].ToolName != "read_file" {
		t.Fatalf("unexpected start event: %#v", events[0])
	}
	if events[1].Type != zeroruntime.StreamEventToolCallDelta || events[1].ToolCallID != "call_1" || events[1].ArgumentsFragment != `{"path":"README.md"}` {
		t.Fatalf("unexpected delta event: %#v", events[1])
	}
	if events[2].Type != zeroruntime.StreamEventToolCallEnd || events[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected end event: %#v", events[2])
	}
	if events[3].Type != zeroruntime.StreamEventDone {
		t.Fatalf("unexpected done event: %#v", events[3])
	}
}

func TestStreamCompletionTracksMultipleToolCallsByIndex(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"query\":"}},{"index":0,"id":"call_a","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}`)
		writeSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"grep","arguments":"\"zero\"}"}}]},"finish_reason":"tool_calls"}]}`)
		writeSSE(w, `[DONE]`)
	})

	events := collectProviderEvents(t, provider)
	starts := eventsOfType(events, zeroruntime.StreamEventToolCallStart)
	deltas := eventsOfType(events, zeroruntime.StreamEventToolCallDelta)
	ends := eventsOfType(events, zeroruntime.StreamEventToolCallEnd)
	if len(starts) != 2 || len(deltas) != 2 || len(ends) != 2 {
		t.Fatalf("events = %#v, want two starts/deltas/ends", events)
	}
	if deltas[0].ToolCallID != "call_a" || deltas[0].ArgumentsFragment != `{"path":"a"}` {
		t.Fatalf("unexpected first delta: %#v", deltas[0])
	}
	if deltas[1].ToolCallID != "call_b" || deltas[1].ArgumentsFragment != `{"query":"zero"}` {
		t.Fatalf("unexpected second delta: %#v", deltas[1])
	}
}

func TestStreamCompletionClassifiesHTTPErrorsAndRedactsToken(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantPrefix string
	}{
		{"auth", http.StatusUnauthorized, `{"error":{"message":"bad key sk-secret","type":"invalid_request_error"}}`, "auth error:"},
		{"rate limit", http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`, "rate limit error:"},
		{"bad request", http.StatusBadRequest, `{"error":{"message":"bad request"}}`, "provider request error:"},
		{"server", http.StatusInternalServerError, `server saw Bearer sk-secret`, "provider error:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := newTestProviderWithKey(t, "sk-secret", func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, tc.body, tc.status)
			})
			stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{})
			if err != nil {
				t.Fatalf("StreamCompletion returned setup error: %v", err)
			}
			events := readAll(stream)
			if len(events) != 1 || events[0].Type != zeroruntime.StreamEventError {
				t.Fatalf("events = %#v, want one error", events)
			}
			if !strings.HasPrefix(events[0].Error, tc.wantPrefix) {
				t.Fatalf("error = %q, want prefix %q", events[0].Error, tc.wantPrefix)
			}
			if strings.Contains(events[0].Error, "sk-secret") || strings.Contains(events[0].Error, "Bearer ") {
				t.Fatalf("error leaked token: %q", events[0].Error)
			}
		})
	}
}

func TestStreamCompletionEmitsStreamErrorObject(t *testing.T) {
	provider := newTestProviderWithKey(t, "sk-secret", func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"error":{"message":"stream failed sk-secret","type":"server_error"}}`)
	})

	events := collectProviderEvents(t, provider)
	if len(events) != 1 || events[0].Type != zeroruntime.StreamEventError {
		t.Fatalf("events = %#v, want one error", events)
	}
	if !strings.HasPrefix(events[0].Error, "provider error:") {
		t.Fatalf("error = %q, want provider error prefix", events[0].Error)
	}
	if strings.Contains(events[0].Error, "sk-secret") {
		t.Fatalf("error leaked token: %q", events[0].Error)
	}
}

func TestStreamCompletionEmitsErrorForMalformedJSON(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":`)
	})

	events := collectProviderEvents(t, provider)
	if len(events) != 1 || events[0].Type != zeroruntime.StreamEventError {
		t.Fatalf("events = %#v, want one error", events)
	}
	if !strings.HasPrefix(events[0].Error, "provider stream error: malformed JSON") {
		t.Fatalf("error = %q, want malformed JSON provider stream error", events[0].Error)
	}
}

func TestStreamCompletionEmitsErrorWhenContextCancels(t *testing.T) {
	requestStarted := make(chan struct{})
	release := make(chan struct{})
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-release
	})
	ctx, cancel := context.WithCancel(context.Background())

	stream, err := provider.StreamCompletion(ctx, zeroruntime.CompletionRequest{})
	if err != nil {
		t.Fatalf("StreamCompletion returned setup error: %v", err)
	}
	<-requestStarted
	cancel()
	close(release)

	events := readAll(stream)
	if len(events) != 1 || events[0].Type != zeroruntime.StreamEventError {
		t.Fatalf("events = %#v, want context error", events)
	}
	if !strings.Contains(events[0].Error, "context canceled") {
		t.Fatalf("error = %q, want context canceled", events[0].Error)
	}
}

func newTestProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	return newTestProviderWithKey(t, "", handler)
}

func newTestProviderWithKey(t *testing.T, apiKey string, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := New(Options{
		APIKey:  apiKey,
		BaseURL: server.URL,
		Model:   "gpt-test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return provider
}

func collectProviderEvents(t *testing.T, provider *Provider) []zeroruntime.StreamEvent {
	t.Helper()
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{})
	if err != nil {
		t.Fatalf("StreamCompletion returned setup error: %v", err)
	}
	return readAll(stream)
}

func readAll(stream <-chan zeroruntime.StreamEvent) []zeroruntime.StreamEvent {
	events := []zeroruntime.StreamEvent{}
	for event := range stream {
		events = append(events, event)
	}
	return events
}

func drain(stream <-chan zeroruntime.StreamEvent) {
	for range stream {
	}
}

func writeSSE(w http.ResponseWriter, payload string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte("data: " + payload + "\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func assertEvent(t *testing.T, event zeroruntime.StreamEvent, eventType zeroruntime.StreamEventType, content string) {
	t.Helper()
	if event.Type != eventType || event.Content != content {
		t.Fatalf("event = %#v, want %s %q", event, eventType, content)
	}
}

func eventsOfType(events []zeroruntime.StreamEvent, eventType zeroruntime.StreamEventType) []zeroruntime.StreamEvent {
	matching := []zeroruntime.StreamEvent{}
	for _, event := range events {
		if event.Type == eventType {
			matching = append(matching, event)
		}
	}
	return matching
}

func TestStreamCompletionDoesNotHangOnEOFWithOpenToolCall(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]}}]}`)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := provider.StreamCompletion(ctx, zeroruntime.CompletionRequest{})
	if err != nil {
		t.Fatalf("StreamCompletion returned setup error: %v", err)
	}
	events := []zeroruntime.StreamEvent{}
	for {
		select {
		case event, ok := <-stream:
			if !ok {
				if len(eventsOfType(events, zeroruntime.StreamEventToolCallEnd)) != 1 {
					t.Fatalf("events = %#v, want one tool-call-end on EOF", events)
				}
				if len(eventsOfType(events, zeroruntime.StreamEventDone)) != 1 {
					t.Fatalf("events = %#v, want done on EOF", events)
				}
				return
			}
			events = append(events, event)
		case <-ctx.Done():
			t.Fatal("stream did not close")
		}
	}
}
