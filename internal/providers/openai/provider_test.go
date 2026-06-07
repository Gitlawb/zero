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
	streamOpts, ok := gotBody["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing or wrong type: %#v", gotBody["stream_options"])
	}
	if streamOpts["include_usage"] != true {
		t.Fatalf("stream_options.include_usage = %#v, want true", streamOpts["include_usage"])
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
		{"service unavailable", http.StatusServiceUnavailable, `{"error":{"message":"overloaded"}}`, "rate limit error:"},
		{"overloaded 529", 529, `{"error":{"message":"overloaded"}}`, "rate limit error:"},
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

func TestStreamCompletionSkipsNamelessToolCallOnEOF(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"arguments":"{\"path\":\"README.md\"}"}}]}}]}`)
	})

	events := collectProviderEvents(t, provider)
	if len(eventsOfType(events, zeroruntime.StreamEventToolCallStart)) != 0 {
		t.Fatalf("events = %#v, want no start for nameless tool call", events)
	}
	if len(eventsOfType(events, zeroruntime.StreamEventToolCallDelta)) != 0 {
		t.Fatalf("events = %#v, want no delta for nameless tool call", events)
	}
	if len(eventsOfType(events, zeroruntime.StreamEventToolCallEnd)) != 0 {
		t.Fatalf("events = %#v, want no end for nameless tool call", events)
	}
	if len(eventsOfType(events, zeroruntime.StreamEventDone)) != 1 {
		t.Fatalf("events = %#v, want done event", events)
	}
}

func TestStreamCompletionIdleTimeoutAbortsStalledStream(t *testing.T) {
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send one token, then hang without sending [DONE] or closing —
		// simulating a stalled upstream (the freeze in the screenshot).
		writeSSE(w, `{"choices":[{"delta":{"content":"hi"}}]}`)
		select {
		case <-r.Context().Done():
		case <-released:
		}
	}))
	defer server.Close()
	defer close(released)

	provider, err := New(Options{
		BaseURL:           server.URL + "/",
		Model:             "gpt-test",
		StreamIdleTimeout: 80 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{})
	if err != nil {
		t.Fatalf("StreamCompletion returned error: %v", err)
	}

	// Must terminate (channel closes) rather than hang forever.
	done := make(chan []zeroruntime.StreamEvent, 1)
	go func() { done <- readAll(stream) }()
	var events []zeroruntime.StreamEvent
	select {
	case events = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not terminate on idle — it hung")
	}

	var gotText, gotIdleError bool
	for _, e := range events {
		if e.Type == zeroruntime.StreamEventText && e.Content == "hi" {
			gotText = true
		}
		if e.Type == zeroruntime.StreamEventError && strings.Contains(strings.ToLower(e.Error), "idle") {
			gotIdleError = true
		}
	}
	if !gotText {
		t.Error("expected the first token before the stall")
	}
	if !gotIdleError {
		t.Errorf("expected a surfaced idle-timeout error, got events: %+v", events)
	}
}

func TestStreamCompletionSendsMaxCompletionTokens(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writeSSE(w, `[DONE]`)
	}))
	defer server.Close()

	provider, err := New(Options{BaseURL: server.URL + "/", Model: "gpt-test", MaxTokens: 1234})
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

	got, ok := gotBody["max_completion_tokens"]
	if !ok {
		t.Fatalf("max_completion_tokens missing from request: %#v", gotBody)
	}
	if n, _ := got.(float64); int(n) != 1234 {
		t.Fatalf("max_completion_tokens = %#v, want 1234", got)
	}
}

func TestStreamCompletionOmitsMaxCompletionTokensWhenUnset(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writeSSE(w, `[DONE]`)
	}))
	defer server.Close()

	provider, err := New(Options{BaseURL: server.URL + "/", Model: "gpt-test"})
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

	if _, ok := gotBody["max_completion_tokens"]; ok {
		t.Fatalf("max_completion_tokens should be omitted when unset: %#v", gotBody["max_completion_tokens"])
	}
}

// A finish_reason of "length" means the response was truncated at the output cap.
// The provider must surface it on the done event so a clipped answer is not
// mistaken for a complete one.
func TestStreamCompletionSurfacesLengthFinishReason(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{"content":"truncated"}}]}`)
		writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"length"}]}`)
		writeSSE(w, `[DONE]`)
	})

	events := collectProviderEvents(t, provider)
	var doneReason string
	var sawDone bool
	for _, e := range events {
		if e.Type == zeroruntime.StreamEventDone {
			sawDone = true
			doneReason = e.FinishReason
		}
	}
	if !sawDone {
		t.Fatalf("no done event; events: %+v", events)
	}
	if doneReason != zeroruntime.FinishReasonLength {
		t.Fatalf("done FinishReason = %q, want %q", doneReason, zeroruntime.FinishReasonLength)
	}

	// And it round-trips through the runtime collector as Truncated.
	collected := zeroruntime.CollectStream(context.Background(), replay(events))
	if !collected.Truncated() || collected.FinishReason != zeroruntime.FinishReasonLength {
		t.Fatalf("collected = %+v, want truncated length", collected)
	}
}

// A content_filter finish_reason maps to the runtime's content-filter reason.
func TestStreamCompletionSurfacesContentFilterFinishReason(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"content_filter"}]}`)
		writeSSE(w, `[DONE]`)
	})

	events := collectProviderEvents(t, provider)
	var sawDone bool
	for _, e := range events {
		if e.Type == zeroruntime.StreamEventDone {
			sawDone = true
			if e.FinishReason != zeroruntime.FinishReasonContentFilter {
				t.Fatalf("done FinishReason = %q, want %q", e.FinishReason, zeroruntime.FinishReasonContentFilter)
			}
		}
	}
	if !sawDone {
		t.Fatalf("no done event; events: %+v", events)
	}
}

// A normal "stop" finish must leave FinishReason empty on the done event.
func TestStreamCompletionNormalFinishHasNoReason(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)
		writeSSE(w, `[DONE]`)
	})

	events := collectProviderEvents(t, provider)
	var sawDone bool
	for _, e := range events {
		if e.Type == zeroruntime.StreamEventDone {
			sawDone = true
			if e.FinishReason != "" {
				t.Fatalf("normal finish leaked FinishReason %q", e.FinishReason)
			}
		}
	}
	if !sawDone {
		t.Fatalf("no done event; events: %+v", events)
	}
}

// The shared SSE reader must join multi-line "data:" continuation fields into a
// single payload (the OpenAI provider previously parsed one line at a time and
// would drop the continuation, producing malformed JSON).
func TestStreamCompletionJoinsMultiLineDataFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// One SSE event whose JSON payload is split across two data: lines.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":\ndata: {\"content\":\"joined\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	provider, err := New(Options{BaseURL: server.URL + "/", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	events := collectProviderEvents(t, provider)
	var text string
	for _, e := range events {
		if e.Type == zeroruntime.StreamEventText {
			text += e.Content
		}
		if e.Type == zeroruntime.StreamEventError {
			t.Fatalf("multi-line data field produced an error: %q", e.Error)
		}
	}
	if text != "joined" {
		t.Fatalf("text = %q, want %q (continuation data: line dropped?)", text, "joined")
	}
}

func replay(events []zeroruntime.StreamEvent) <-chan zeroruntime.StreamEvent {
	ch := make(chan zeroruntime.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch
}

func TestStreamCompletionEmitsDroppedOnNamelessToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A tool call with arguments + finish_reason but no function name.
		writeSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","function":{"arguments":"{}"}}]}}]}`)
		writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		writeSSE(w, `[DONE]`)
	}))
	defer server.Close()

	provider, err := New(Options{BaseURL: server.URL + "/", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	events := collectProviderEvents(t, provider)

	var dropped, started bool
	for _, e := range events {
		if e.Type == zeroruntime.StreamEventToolCallDropped {
			dropped = true
		}
		if e.Type == zeroruntime.StreamEventToolCallStart {
			started = true
		}
	}
	if started {
		t.Error("a nameless tool call must not start")
	}
	if !dropped {
		t.Errorf("expected a dropped-tool-call signal, got events: %+v", events)
	}
}
