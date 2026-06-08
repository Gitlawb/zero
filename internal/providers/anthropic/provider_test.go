package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestStreamCompletionPostsMessagesRequest(t *testing.T) {
	var gotPath string
	var gotAPIKey string
	var gotVersion string
	var gotUserAgent string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotUserAgent = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writeSSEEvent(w, "message_start", `{"type":"message_start","message":{"usage":{"input_tokens":3}}}`)
		writeSSEEvent(w, "message_stop", `{"type":"message_stop"}`)
	}))
	defer server.Close()

	provider, err := New(Options{
		APIKey:    "sk-ant",
		BaseURL:   server.URL + "/",
		Model:     "claude-test",
		MaxTokens: 64_000,
		UserAgent: "zero-test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleSystem, Content: "You are Zero."},
			{Role: zeroruntime.MessageRoleUser, Content: "Read the file."},
			{
				Role:    zeroruntime.MessageRoleAssistant,
				Content: "I will inspect it.",
				ToolCalls: []zeroruntime.ToolCall{{
					ID:        "toolu_1",
					Name:      "read_file",
					Arguments: `{"path":"src/index.ts"}`,
				}},
			},
			{Role: zeroruntime.MessageRoleTool, Content: "file contents", ToolCallID: "toolu_1"},
		},
		Tools: []zeroruntime.ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion returned error: %v", err)
	}
	drain(stream)

	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", gotPath)
	}
	if gotAPIKey != "sk-ant" {
		t.Fatalf("x-api-key = %q, want key", gotAPIKey)
	}
	if gotVersion != defaultVersion {
		t.Fatalf("anthropic-version = %q, want %q", gotVersion, defaultVersion)
	}
	if gotUserAgent != "zero-test" {
		t.Fatalf("User-Agent = %q, want zero-test", gotUserAgent)
	}
	if gotBody["model"] != "claude-test" || gotBody["stream"] != true || gotBody["max_tokens"] != float64(64_000) {
		t.Fatalf("unexpected model/stream/max_tokens: %#v", gotBody)
	}
	// Prompt caching: system is sent as a cacheable text block, not a bare string.
	system := gotBody["system"].([]any)
	sysBlock := system[0].(map[string]any)
	if sysBlock["type"] != "text" || sysBlock["text"] != "You are Zero." {
		t.Fatalf("unexpected system block: %#v", gotBody["system"])
	}
	if cc, _ := sysBlock["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Fatalf("system block must carry ephemeral cache_control, got %#v", sysBlock["cache_control"])
	}
	messages := gotBody["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %#v, want user, assistant, tool-result user", messages)
	}
	assistant := messages[1].(map[string]any)
	assistantBlocks := assistant["content"].([]any)
	toolUse := assistantBlocks[1].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["id"] != "toolu_1" || toolUse["name"] != "read_file" {
		t.Fatalf("unexpected tool_use block: %#v", toolUse)
	}
	input := toolUse["input"].(map[string]any)
	if input["path"] != "src/index.ts" {
		t.Fatalf("tool input = %#v, want path", input)
	}
	toolResult := messages[2].(map[string]any)
	toolResultBlocks := toolResult["content"].([]any)
	if toolResultBlocks[0].(map[string]any)["tool_use_id"] != "toolu_1" {
		t.Fatalf("unexpected tool result: %#v", toolResultBlocks[0])
	}
	tools := gotBody["tools"].([]any)
	lastTool := tools[len(tools)-1].(map[string]any)
	if lastTool["input_schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("unexpected tool schema: %#v", tools)
	}
	// The last tool carries the cache breakpoint so the whole tool block is cached.
	if cc, _ := lastTool["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Fatalf("last tool must carry ephemeral cache_control, got %#v", lastTool["cache_control"])
	}
}

func TestStreamCompletionAppliesCustomAuthAndHeaders(t *testing.T) {
	var gotDefaultAuth string
	var gotCustomAuth string
	var gotTenant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDefaultAuth = r.Header.Get("x-api-key")
		gotCustomAuth = r.Header.Get("Authorization")
		gotTenant = r.Header.Get("X-Tenant")
		writeSSEEvent(w, "message_stop", `{"type":"message_stop"}`)
	}))
	defer server.Close()

	provider, err := New(Options{
		APIKey:        "sk-ant",
		BaseURL:       server.URL,
		Model:         "claude-test",
		AuthHeader:    "Authorization",
		AuthScheme:    "Bearer",
		CustomHeaders: map[string]string{"X-Tenant": "zero"},
	})
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

	if gotDefaultAuth != "" {
		t.Fatalf("x-api-key = %q, want empty when custom auth header is used", gotDefaultAuth)
	}
	if gotCustomAuth != "Bearer sk-ant" {
		t.Fatalf("Authorization = %q, want bearer token", gotCustomAuth)
	}
	if gotTenant != "zero" {
		t.Fatalf("X-Tenant = %q, want custom header", gotTenant)
	}
}

func TestStreamCompletionEmitsTextUsageAndDone(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSEEvent(w, "message_start", `{"type":"message_start","message":{"usage":{"input_tokens":25}}}`)
		writeSSEEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
		writeSSEEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" Zero"}}`)
		writeSSEEvent(w, "message_delta", `{"type":"message_delta","usage":{"output_tokens":15}}`)
		writeSSEEvent(w, "message_stop", `{"type":"message_stop"}`)
	})

	events := collectProviderEvents(t, provider)
	want := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "Hello"},
		{Type: zeroruntime.StreamEventText, Content: " Zero"},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 25, OutputTokens: 15, PromptTokens: 25, CompletionTokens: 15}},
		{Type: zeroruntime.StreamEventDone},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestStreamCompletionReportsCacheTokens(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSEEvent(w, "message_start", `{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":200,"cache_creation_input_tokens":40}}}`)
		writeSSEEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
		writeSSEEvent(w, "message_delta", `{"type":"message_delta","usage":{"output_tokens":5}}`)
		writeSSEEvent(w, "message_stop", `{"type":"message_stop"}`)
	})

	var usage *zeroruntime.Usage
	for _, event := range collectProviderEvents(t, provider) {
		if event.Type == zeroruntime.StreamEventUsage {
			u := event.Usage
			usage = &u
		}
	}
	if usage == nil {
		t.Fatal("expected a usage event")
	}
	// InputTokens is the full prompt (uncached + cache_read + cache_creation);
	// CachedInputTokens is the cache-hit subset.
	if usage.CachedInputTokens != 200 {
		t.Fatalf("CachedInputTokens = %d, want 200", usage.CachedInputTokens)
	}
	if usage.InputTokens != 250 {
		t.Fatalf("InputTokens = %d, want 250 (10 input + 200 cache_read + 40 cache_creation)", usage.InputTokens)
	}
	if usage.OutputTokens != 5 {
		t.Fatalf("OutputTokens = %d, want 5", usage.OutputTokens)
	}
}

func TestStreamCompletionEmitsToolUseBlocks(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSEEvent(w, "content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`)
		writeSSEEvent(w, "content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`)
		writeSSEEvent(w, "content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"src/index.ts\"}"}}`)
		writeSSEEvent(w, "content_block_stop", `{"type":"content_block_stop","index":1}`)
		writeSSEEvent(w, "message_stop", `{"type":"message_stop"}`)
	})

	events := collectProviderEvents(t, provider)
	want := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "toolu_1", ToolName: "read_file"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "toolu_1", ArgumentsFragment: `{"path":`},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "toolu_1", ArgumentsFragment: `"src/index.ts"}`},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "toolu_1"},
		{Type: zeroruntime.StreamEventDone},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestStreamCompletionClosesOpenToolCallOnEOF(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSEEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"grep","input":{}}}`)
		writeSSEEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"pattern\":\"Zero\"}"}}`)
	})

	events := collectProviderEvents(t, provider)
	if len(eventsOfType(events, zeroruntime.StreamEventToolCallEnd)) != 1 {
		t.Fatalf("events = %#v, want one tool-call-end on EOF", events)
	}
	if len(eventsOfType(events, zeroruntime.StreamEventDone)) != 1 {
		t.Fatalf("events = %#v, want done on EOF", events)
	}
}

func TestStreamCompletionClassifiesHTTPErrorsAndRedactsToken(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantPrefix string
	}{
		{"auth", http.StatusUnauthorized, `{"error":{"message":"bad key sk-ant"}}`, "auth error:"},
		{"rate limit", http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`, "rate limit error:"},
		{"overloaded", http.StatusServiceUnavailable, `{"error":{"message":"overloaded"}}`, "rate limit error:"},
		{"bad request", http.StatusBadRequest, `{"error":{"message":"bad request"}}`, "provider request error:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := newTestProviderWithKey(t, "sk-ant", func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, tc.body, tc.status)
			})
			stream, err := provider.StreamCompletion(context.Background(), validRequest())
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
			if strings.Contains(events[0].Error, "sk-ant") {
				t.Fatalf("error leaked token: %q", events[0].Error)
			}
		})
	}
}

func TestStreamCompletionEmitsStreamErrorObject(t *testing.T) {
	provider := newTestProviderWithKey(t, "sk-ant", func(w http.ResponseWriter, r *http.Request) {
		writeSSEEvent(w, "error", `{"type":"error","error":{"message":"stream failed sk-ant","type":"overloaded_error"}}`)
	})

	events := collectProviderEvents(t, provider)
	if len(events) != 1 || events[0].Type != zeroruntime.StreamEventError {
		t.Fatalf("events = %#v, want one error", events)
	}
	if !strings.HasPrefix(events[0].Error, "provider error:") {
		t.Fatalf("error = %q, want provider error prefix", events[0].Error)
	}
	if strings.Contains(events[0].Error, "sk-ant") {
		t.Fatalf("error leaked token: %q", events[0].Error)
	}
}

func TestStreamCompletionRejectsMalformedHistoryBeforeDispatch(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("provider should not dispatch malformed history")
	})

	_, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleTool, Content: "missing id"}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires toolCallId") {
		t.Fatalf("error = %v, want missing toolCallId", err)
	}

	_, err = provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleUser, Content: "call tool"},
			{
				Role:      zeroruntime.MessageRoleAssistant,
				ToolCalls: []zeroruntime.ToolCall{{ID: "toolu_1", Name: "read_file", Arguments: `"src/index.ts"`}},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires tool arguments for read_file to be a JSON object") {
		t.Fatalf("error = %v, want non-object tool argument error", err)
	}
}

func TestNewRequiresModelAndPositiveMaxTokens(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New without model returned nil error")
	}
	if _, err := New(Options{Model: "claude-test", MaxTokens: -1}); err == nil {
		t.Fatal("New with negative max tokens returned nil error")
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
		Model:   "claude-test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return provider
}

func collectProviderEvents(t *testing.T, provider *Provider) []zeroruntime.StreamEvent {
	t.Helper()
	stream, err := provider.StreamCompletion(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("StreamCompletion returned setup error: %v", err)
	}
	return readAll(stream)
}

func validRequest() zeroruntime.CompletionRequest {
	return zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hello"}},
	}
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

func writeSSEEvent(w http.ResponseWriter, eventName string, payload string) {
	w.Header().Set("Content-Type", "text/event-stream")
	if eventName != "" {
		_, _ = w.Write([]byte("event: " + eventName + "\n"))
	}
	_, _ = w.Write([]byte("data: " + payload + "\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
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
