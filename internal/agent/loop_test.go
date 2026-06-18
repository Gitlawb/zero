package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/specmode"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type mockProvider struct {
	turns    [][]zeroruntime.StreamEvent
	requests []zeroruntime.CompletionRequest
}

func (provider *mockProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	provider.requests = append(provider.requests, request)

	events := []zeroruntime.StreamEvent{{Type: zeroruntime.StreamEventDone}}
	if len(provider.turns) >= len(provider.requests) {
		events = provider.turns[len(provider.requests)-1]
	}

	ch := make(chan zeroruntime.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestIsRetriableToolError(t *testing.T) {
	cases := []struct {
		name   string
		result ToolResult
		want   bool
	}{
		{"success", ToolResult{Status: tools.StatusOK}, false},
		{"bad arguments", ToolResult{Status: tools.StatusError, Output: "Error: Failed to parse arguments for x: bad json"}, true},
		{"execution failure", ToolResult{Status: tools.StatusError, Output: "Error: read foo.txt: no such file"}, true},
		{"disabled tool", ToolResult{Status: tools.StatusError, Output: `Error: Tool "x" is not enabled for this run.`}, false},
		{"permission denied (meta)", ToolResult{Status: tools.StatusError, Output: "Error: Permission denied for x: needs approval", Meta: map[string]string{"permission_action": "deny"}}, false},
		{"permission required", ToolResult{Status: tools.StatusError, Output: "Error: Permission required for x: approve first"}, false},
		{"sandbox violation", ToolResult{Status: tools.StatusError, Output: "Error: Sandbox violation: outside_workspace"}, false},
		{"sandbox approval", ToolResult{Status: tools.StatusError, Output: "Error: Sandbox approval required for x: network"}, false},
		// Structured denial categories are authoritative regardless of message text.
		{"denial: filtered", ToolResult{Status: tools.StatusError, Output: "anything", DenialReason: DenialFiltered}, false},
		{"denial: permission", ToolResult{Status: tools.StatusError, Output: "anything", DenialReason: DenialPermissionDenied}, false},
		{"denial: approval canceled", ToolResult{Status: tools.StatusError, Output: "anything", DenialReason: DenialApprovalCanceled}, false},
		{"denial: sandbox", ToolResult{Status: tools.StatusError, Output: "anything", DenialReason: DenialSandboxViolation}, false},
	}
	for _, c := range cases {
		if got := isRetriableToolError(c.result); got != c.want {
			t.Errorf("%s: isRetriableToolError = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRunReturnsProviderText(t *testing.T) {
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "hello"},
			{Type: zeroruntime.StreamEventText, Content: " zero"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	result, err := Run(context.Background(), "say hi", provider, Options{
		Registry: tools.NewRegistry(),
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "hello zero" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one provider turn, got %d", len(provider.requests))
	}
	assertMessage(t, provider.requests[0].Messages[0], zeroruntime.MessageRoleSystem, "")
	assertMessage(t, provider.requests[0].Messages[1], zeroruntime.MessageRoleUser, "say hi")
}

func TestRunDoesNotPersistReasoningAsAssistantText(t *testing.T) {
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventReasoning, Content: "private reasoning"},
			{Type: zeroruntime.StreamEventText, Content: "public answer"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}
	var reasoning []string

	result, err := Run(context.Background(), "say hi", provider, Options{
		Registry:    tools.NewRegistry(),
		OnReasoning: func(delta string) { reasoning = append(reasoning, delta) },
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "public answer" {
		t.Fatalf("final answer = %q, want public answer", result.FinalAnswer)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected persisted messages")
	}
	last := result.Messages[len(result.Messages)-1]
	if last.Role != zeroruntime.MessageRoleAssistant || last.Content != "public answer" {
		t.Fatalf("assistant message = %#v, want answer-only assistant content", last)
	}
	if len(reasoning) != 1 || reasoning[0] != "private reasoning" {
		t.Fatalf("reasoning callbacks = %#v", reasoning)
	}
}

func TestRunReportsTruncationFinishReason(t *testing.T) {
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "partial answer"},
			{Type: zeroruntime.StreamEventDone, FinishReason: zeroruntime.FinishReasonLength},
		}},
	}

	result, err := Run(context.Background(), "write a long thing", provider, Options{
		Registry: tools.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "partial answer" {
		t.Fatalf("final answer = %q", result.FinalAnswer)
	}
	if result.FinishReason != zeroruntime.FinishReasonLength {
		t.Fatalf("FinishReason = %q, want %q", result.FinishReason, zeroruntime.FinishReasonLength)
	}
	if !result.Truncated() {
		t.Fatal("Truncated() = false, want true for a length-capped response")
	}
	if result.TruncationNotice() == "" {
		t.Fatal("TruncationNotice() empty for a truncated response")
	}
}

func TestRunNormalCompletionIsNotTruncated(t *testing.T) {
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "done"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	result, err := Run(context.Background(), "hi", provider, Options{Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Truncated() || result.TruncationNotice() != "" {
		t.Fatalf("normal completion reported as truncated: reason=%q", result.FinishReason)
	}
}

func TestResultTruncationNotice(t *testing.T) {
	cases := map[string]struct {
		reason     string
		wantNotice bool
	}{
		"length":         {zeroruntime.FinishReasonLength, true},
		"content_filter": {zeroruntime.FinishReasonContentFilter, true},
		"unknown":        {"weird_reason", true},
		"normal":         {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			notice := Result{FinishReason: tc.reason}.TruncationNotice()
			if (notice != "") != tc.wantNotice {
				t.Fatalf("notice = %q, wantNotice = %v", notice, tc.wantNotice)
			}
		})
	}
}

func TestRunEmitsTextDeltas(t *testing.T) {
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "hello"},
			{Type: zeroruntime.StreamEventText, Content: " zero"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	var deltas []string
	_, err := Run(context.Background(), "say hi", provider, Options{
		Registry: tools.NewRegistry(),
		OnText:   func(delta string) { deltas = append(deltas, delta) },
	})

	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "|") != "hello| zero" {
		t.Fatalf("expected text deltas, got %#v", deltas)
	}
}

func TestRunEmitsUsageEvents(t *testing.T) {
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{PromptTokens: 12, CompletionTokens: 5, CachedInputTokens: 2}},
			{Type: zeroruntime.StreamEventText, Content: "done"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	var usages []zeroruntime.Usage
	_, err := Run(context.Background(), "track usage", provider, Options{
		Registry: tools.NewRegistry(),
		OnUsage:  func(usage zeroruntime.Usage) { usages = append(usages, usage) },
	})

	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 1 {
		t.Fatalf("expected one usage event, got %#v", usages)
	}
	if usages[0].PromptTokens != 12 || usages[0].CompletionTokens != 5 || usages[0].CachedInputTokens != 2 {
		t.Fatalf("unexpected usage event: %#v", usages[0])
	}
}

func TestRunAdvertisesRuntimeToolDefinitions(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "done"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	_, err := Run(context.Background(), "what tools exist?", provider, Options{
		Registry: registry,
	})

	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 1 {
		t.Fatalf("expected one advertised tool, got %#v", provider.requests[0].Tools)
	}

	toolDefinition := provider.requests[0].Tools[0]
	if toolDefinition.Name != "read_file" {
		t.Fatalf("expected read_file definition, got %#v", toolDefinition)
	}
	parameters := toolDefinition.Parameters
	if parameters["type"] != "object" {
		t.Fatalf("expected object schema, got %#v", parameters)
	}
	if parameters["additionalProperties"] != false {
		t.Fatalf("expected additionalProperties=false, got %#v", parameters["additionalProperties"])
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %#v", parameters["properties"])
	}
	pathProperty, ok := properties["path"].(map[string]any)
	if !ok {
		t.Fatalf("expected path property map, got %#v", properties["path"])
	}
	if pathProperty["type"] != "string" || pathProperty["description"] == "" {
		t.Fatalf("unexpected path property schema: %#v", pathProperty)
	}
	required, ok := parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "path" {
		t.Fatalf("unexpected required fields: %#v", parameters["required"])
	}
}

func TestRunAdvertisesWebFetchInAutoMode(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewWebFetchTool())
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "done"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	_, err := Run(context.Background(), "what tools exist?", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAuto,
	})

	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 1 || provider.requests[0].Tools[0].Name != "web_fetch" {
		t.Fatalf("expected web_fetch to be advertised in auto mode, got %#v", provider.requests[0].Tools)
	}
}

func TestRunAdvertisesAllowedWebSearchInAutoMode(t *testing.T) {
	t.Setenv("ZERO_WEBSEARCH_BASE_URL", "https://search.example/api")
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreNetworkTools() {
		if tool.Name() == "web_search" {
			registry.Register(tool)
		}
	}
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "done"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	_, err := Run(context.Background(), "what tools exist?", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAuto,
	})

	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 1 || provider.requests[0].Tools[0].Name != "web_search" {
		t.Fatalf("expected web_search to be advertised in auto mode, got %#v", provider.requests[0].Tools)
	}
}

func TestRunFiltersAdvertisedTools(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	registry.Register(tools.NewGrepTool(root))
	registry.Register(tools.NewWriteFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "done"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	_, err := Run(context.Background(), "what tools exist?", provider, Options{
		Registry:      registry,
		EnabledTools:  []string{"read_file", "grep"},
		DisabledTools: []string{"grep"},
	})

	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 1 || provider.requests[0].Tools[0].Name != "read_file" {
		t.Fatalf("expected only read_file to be advertised, got %#v", provider.requests[0].Tools)
	}
}

func TestRunRejectsFilteredToolCalls(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "read_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{"path":"README.md"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call_1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "read", provider, Options{
		Registry:      registry,
		DisabledTools: []string{"read_file"},
		MaxTurns:      2,
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer after filtered tool result, got %q", result.FinalAnswer)
	}
	lastMessage := result.Messages[len(result.Messages)-2]
	if !strings.Contains(lastMessage.Content, "not enabled") {
		t.Fatalf("expected filtered tool error message, got %#v", result.Messages)
	}
}

func TestRunRejectsToolCallsOutsideEnabledList(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "read_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{"path":"README.md"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call_1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "read", provider, Options{
		Registry:     registry,
		EnabledTools: []string{"grep"},
		MaxTurns:     2,
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer after filtered tool result, got %q", result.FinalAnswer)
	}
	lastMessage := result.Messages[len(result.Messages)-2]
	if !strings.Contains(lastMessage.Content, "not enabled") {
		t.Fatalf("expected filtered tool error message, got %#v", result.Messages)
	}
}

func TestRunExecutesToolCallThroughRegistry(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, filepath.Join(root, "notes.txt"), "alpha\nbeta\n")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "read_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "read done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	var toolResults []ToolResult
	result, err := Run(context.Background(), "read notes", provider, Options{
		Registry:     registry,
		OnToolResult: func(result ToolResult) { toolResults = append(toolResults, result) },
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "read done" {
		t.Fatalf("expected final answer from second turn, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected provider to be called twice, got %d", len(provider.requests))
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	assertMessage(t, lastMessage, zeroruntime.MessageRoleTool, "alpha")
	if lastMessage.ToolCallID != "call-1" {
		t.Fatalf("expected tool_call_id call-1, got %q", lastMessage.ToolCallID)
	}
	if len(toolResults) != 1 || toolResults[0].Status != tools.StatusOK {
		t.Fatalf("expected one ok tool result, got %#v", toolResults)
	}
}

func TestRunSanitizesMalformedToolCallArgumentsBeforeRetry(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "read_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"README.md"}{"path":"AGENTS.md"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "recovered"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "read files", provider, Options{
		Registry: registry,
		MaxTurns: 2,
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "recovered" {
		t.Fatalf("expected recovery turn final answer, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected retry request after malformed tool args, got %d requests", len(provider.requests))
	}

	var assistantCall *zeroruntime.ToolCall
	var toolParseError string
	for _, message := range provider.requests[1].Messages {
		if message.Role == zeroruntime.MessageRoleAssistant && len(message.ToolCalls) > 0 {
			assistantCall = &message.ToolCalls[0]
		}
		if message.Role == zeroruntime.MessageRoleTool && strings.Contains(message.Content, "Failed to parse arguments for read_file") {
			toolParseError = message.Content
		}
	}
	if assistantCall == nil {
		t.Fatalf("expected retry request to include assistant tool call history, got %#v", provider.requests[1].Messages)
	}
	if !json.Valid([]byte(assistantCall.Arguments)) {
		t.Fatalf("assistant tool-call arguments must be valid JSON for provider replay, got %q", assistantCall.Arguments)
	}
	if assistantCall.Arguments != "{}" {
		t.Fatalf("malformed arguments should be sanitized to an empty JSON object, got %q", assistantCall.Arguments)
	}
	if toolParseError == "" {
		t.Fatalf("expected model-visible tool result to keep the parse error, messages: %#v", provider.requests[1].Messages)
	}
}

func TestRunDefersSelfCorrectFeedbackUntilAfterToolBatch(t *testing.T) {
	// A single assistant turn with two tool calls where the first mutates and
	// self-correct fails must keep both tool_results contiguous, with the feedback
	// appended only after the whole batch — otherwise a user message interleaves
	// between tool_results and breaks strict provider replay.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	registry.Register(tools.NewReadFileTool(root))

	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "write_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt","content":"hello"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-2", ToolName: "read_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-2", ArgumentsFragment: `{"path":"existing.txt"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-2"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	corrector := NewSelfCorrector(root, erroringChecker{err: errors.New("lsp boom")}, nil, SelfCorrectConfig{
		Enabled:    true,
		IncludeLSP: true,
		Autonomy:   "high",
	})

	if _, err := Run(context.Background(), "go", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		SelfCorrect:    corrector,
		OnPermissionRequest: func(_ context.Context, _ PermissionRequest) (PermissionDecision, error) {
			return PermissionDecision{Action: PermissionDecisionAllow, Reason: "test"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a second completion request, got %d", len(provider.requests))
	}

	msgs := provider.requests[1].Messages
	var toolIdx []int
	feedbackIdx := -1
	for i, m := range msgs {
		switch m.Role {
		case zeroruntime.MessageRoleTool:
			toolIdx = append(toolIdx, i)
		case zeroruntime.MessageRoleUser:
			if strings.Contains(m.Content, "Verification failed after your edit") {
				feedbackIdx = i
			}
		}
	}
	if len(toolIdx) != 2 {
		t.Fatalf("expected 2 tool_result messages, got %d: %#v", len(toolIdx), msgs)
	}
	if feedbackIdx == -1 {
		t.Fatalf("expected a self-correct feedback message, messages: %#v", msgs)
	}
	if toolIdx[1] != toolIdx[0]+1 {
		t.Fatalf("tool_results must be contiguous, got indices %v: %#v", toolIdx, msgs)
	}
	if feedbackIdx < toolIdx[1] {
		t.Fatalf("self-correct feedback (idx %d) must come after both tool_results %v", feedbackIdx, toolIdx)
	}
}

func TestRunBatchesSelfCorrectOncePerTurn(t *testing.T) {
	// Two mutating tool calls in one assistant turn must trigger a single
	// AfterEdit over the union of changed files — not one per call — so the per-run
	// attempt budget isn't consumed twice and an intermediate edit isn't verified
	// after a later call in the same turn supersedes it.
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))

	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "write_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"a.txt","content":"hello"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-2", ToolName: "write_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-2", ArgumentsFragment: `{"path":"b.txt","content":"hello"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-2"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	corrector := NewSelfCorrector(root, erroringChecker{err: errors.New("boom")}, nil, SelfCorrectConfig{
		Enabled:    true,
		IncludeLSP: true,
		Autonomy:   "high",
	})

	if _, err := Run(context.Background(), "go", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		SelfCorrect:    corrector,
		OnPermissionRequest: func(_ context.Context, _ PermissionRequest) (PermissionDecision, error) {
			return PermissionDecision{Action: PermissionDecisionAllow, Reason: "test"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	// One AfterEdit for the whole turn -> exactly one corrective attempt consumed
	// (the old per-call path would have consumed two).
	if corrector.attempts != 1 {
		t.Fatalf("AfterEdit should run once per turn (attempts=1), got %d", corrector.attempts)
	}
	feedbackCount := 0
	for _, m := range provider.requests[1].Messages {
		if m.Role == zeroruntime.MessageRoleUser && strings.Contains(m.Content, "Verification failed after your edit") {
			feedbackCount++
		}
	}
	if feedbackCount != 1 {
		t.Fatalf("expected exactly one self-correct feedback message, got %d", feedbackCount)
	}
}

func TestRunDeniesPromptToolWithoutUnsafePermission(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write denied")
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "write denied" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected denied write to leave file missing, stat error: %v", err)
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if !strings.Contains(lastMessage.Content, "Permission required for write_file") {
		t.Fatalf("expected permission denial tool result, got %q", lastMessage.Content)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.ToolCallID != "call-1" || event.ToolName != "write_file" || event.Action != PermissionActionPrompt {
		t.Fatalf("unexpected permission event: %#v", event)
	}
	if event.Permission != string(tools.PermissionPrompt) || event.PermissionMode != PermissionModeAsk || event.SideEffect != string(tools.SideEffectWrite) {
		t.Fatalf("unexpected permission metadata: %#v", event)
	}
	if !strings.Contains(event.Reason, "Creates or overwrites files") {
		t.Fatalf("expected tool safety reason in permission event, got %#v", event)
	}
}

func TestRunRequestsPromptToolPermissionBeforeExecution(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write approved")
	var requests []PermissionRequest
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			requests = append(requests, request)
			return PermissionDecision{Action: PermissionDecisionAllow, Reason: "approved for test"}, nil
		},
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "write approved" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("expected approved write content, got %q", content)
	}
	if len(requests) != 1 {
		t.Fatalf("expected one permission request, got %#v", requests)
	}
	request := requests[0]
	if request.ToolCallID != "call-1" || request.ToolName != "write_file" || request.Action != PermissionActionPrompt {
		t.Fatalf("unexpected permission request: %#v", request)
	}
	if request.PermissionMode != PermissionModeAsk || request.SideEffect != string(tools.SideEffectWrite) || request.Autonomy != string(sandbox.AutonomyMedium) {
		t.Fatalf("unexpected request metadata: %#v", request)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one final permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.Action != PermissionActionAllow || !event.PermissionGranted {
		t.Fatalf("expected final allow event after approval, got %#v", event)
	}
	if event.DecisionReason != "approved for test" {
		t.Fatalf("expected decision reason in final event, got %#v", event)
	}
}

func TestRunAllowsWorkspaceWriteWithoutPromptWhenSandboxPolicyPermits(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write done")
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        sandbox.DefaultPolicy(),
		}),
		OnPermissionRequest: func(context.Context, PermissionRequest) (PermissionDecision, error) {
			t.Fatal("workspace write should not request permission")
			return PermissionDecision{}, nil
		},
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "write done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("expected workspace write content, got %q", content)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one auto permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.Action != PermissionActionAllow || event.PermissionGranted || event.GrantMatched {
		t.Fatalf("expected sandbox policy allow without user grant, got %#v", event)
	}
	if event.Reason != "workspace write permitted by sandbox policy" {
		t.Fatalf("expected workspace-write reason, got %#v", event)
	}
}

func TestRunDeniesPromptToolWhenPermissionRequestDenied(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write denied")
	var requests []PermissionRequest
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			requests = append(requests, request)
			return PermissionDecision{Action: PermissionDecisionDeny, Reason: "not this command"}, nil
		},
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "write denied" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected denied write to leave file missing, stat error: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected one permission request, got %#v", requests)
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if !strings.Contains(lastMessage.Content, "Permission denied for write_file") || !strings.Contains(lastMessage.Content, "not this command") {
		t.Fatalf("expected denied permission tool result, got %q", lastMessage.Content)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one final permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.Action != PermissionActionDeny || event.PermissionGranted {
		t.Fatalf("expected final deny event, got %#v", event)
	}
	if event.DecisionReason != "not this command" {
		t.Fatalf("expected denial reason in final event, got %#v", event)
	}
}

func TestRunAbortsWhenPermissionRequestCanceled(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write should not continue")
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		OnPermissionRequest: func(context.Context, PermissionRequest) (PermissionDecision, error) {
			return PermissionDecision{Action: PermissionDecisionCancel, Reason: "redirect needed"}, nil
		},
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if !errors.Is(err, errPermissionApprovalCanceled) {
		t.Fatalf("expected permission approval cancel error, got %v", err)
	}
	if result.FinalAnswer != "" {
		t.Fatalf("canceled run must not continue to final answer, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("canceled permission prompt should stop after first turn, got %d provider requests", len(provider.requests))
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected canceled write to leave file missing, stat error: %v", err)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one cancel permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.Action != PermissionActionCancel || event.DecisionAction != PermissionDecisionCancel || event.PermissionGranted {
		t.Fatalf("expected final cancel event, got %#v", event)
	}
	if event.DecisionReason != "redirect needed" {
		t.Fatalf("expected cancel reason in final event, got %#v", event)
	}
}

func TestAvailablePermissionDecisionsSplitDenyAndCancel(t *testing.T) {
	options := Options{Sandbox: sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: t.TempDir(), Policy: sandbox.DefaultPolicy()})}
	cases := []struct {
		name string
		tool string
		want []PermissionDecisionAction
	}{
		{
			name: "bash keeps recoverable deny and explicit cancel",
			tool: "bash",
			want: []PermissionDecisionAction{
				PermissionDecisionAllow,
				PermissionDecisionAllowForSession,
				PermissionDecisionDeny,
				PermissionDecisionCancel,
			},
		},
		{
			name: "apply patch uses cancel without recoverable deny",
			tool: "apply_patch",
			want: []PermissionDecisionAction{
				PermissionDecisionAllow,
				PermissionDecisionAllowForSession,
				PermissionDecisionCancel,
			},
		},
		{
			name: "file write keeps recoverable deny",
			tool: "write_file",
			want: []PermissionDecisionAction{
				PermissionDecisionAllow,
				PermissionDecisionAllowForSession,
				PermissionDecisionDeny,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := availablePermissionDecisions(PermissionEvent{
				ToolName: tc.tool,
				Action:   PermissionActionPrompt,
			}, options)
			if !equalPermissionDecisions(got, tc.want) {
				t.Fatalf("decisions = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRunPersistsAlwaysAllowPermissionDecision(t *testing.T) {
	root := t.TempDir()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write approved")
	var permissionEvents []PermissionEvent
	policy := sandbox.DefaultPolicy()
	policy.EnforceWorkspace = false

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        policy,
			Store:         store,
		}),
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			if request.Risk.Level == "" {
				t.Fatalf("expected request risk to be populated: %#v", request)
			}
			return PermissionDecision{Action: PermissionDecisionAlwaysAllow, Reason: "trust write_file for this workspace"}, nil
		},
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "write approved" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	// The grant is scoped to exactly the file the call wrote, anchored to the
	// workspace — not a blanket tool-wide allow.
	notesPath := filepath.Join(root, "notes.txt")
	lookup, err := store.Lookup("write_file", notesPath, sandbox.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	if !lookup.Matched || lookup.Grant.Decision != sandbox.GrantAllow {
		t.Fatalf("expected persistent allow grant, got %#v", lookup)
	}
	if lookup.Grant.ScopeKind != sandbox.ScopeFile || lookup.Grant.Scope != notesPath {
		t.Fatalf("expected file-scoped grant for %q, got %#v", notesPath, lookup.Grant)
	}
	// A different file in the same workspace is NOT covered by that grant.
	other, err := store.Lookup("write_file", filepath.Join(root, "other.txt"), sandbox.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	if other.Matched {
		t.Fatalf("a sibling file must not be covered by a file-scoped grant: %#v", other)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one final permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.Action != PermissionActionAllow || !event.PermissionGranted || event.Grant == nil {
		t.Fatalf("expected allow event with persisted grant, got %#v", event)
	}
	if event.Grant.Decision != sandbox.GrantAllow || event.Grant.ToolName != "write_file" {
		t.Fatalf("unexpected persisted grant in event: %#v", event)
	}
}

func TestRunSessionAllowSkipsMatchingPromptWithoutPersistentGrant(t *testing.T) {
	root := t.TempDir()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "write_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt","content":"first","overwrite":true}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-2", ToolName: "write_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-2", ArgumentsFragment: `{"path":"notes.txt","content":"second","overwrite":true}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-2"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}
	var requests []PermissionRequest
	var permissionEvents []PermissionEvent
	policy := sandbox.DefaultPolicy()
	policy.EnforceWorkspace = false

	result, err := Run(context.Background(), "write notes twice", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        policy,
			Store:         store,
		}),
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			requests = append(requests, request)
			return PermissionDecision{Action: PermissionDecisionAllowForSession, Reason: "trust this file for the session"}, nil
		},
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "second" {
		t.Fatalf("expected second write content, got %q", content)
	}
	if len(requests) != 1 {
		t.Fatalf("expected one permission request, got %#v", requests)
	}
	if len(permissionEvents) != 2 {
		t.Fatalf("expected two permission events, got %#v", permissionEvents)
	}
	if permissionEvents[0].DecisionAction != PermissionDecisionAllowForSession || permissionEvents[0].Grant == nil || !permissionEvents[0].Grant.Session {
		t.Fatalf("expected first event to carry session grant, got %#v", permissionEvents[0])
	}
	if !permissionEvents[1].GrantMatched || permissionEvents[1].Grant == nil || !permissionEvents[1].Grant.Session {
		t.Fatalf("expected second event to be session-grant matched, got %#v", permissionEvents[1])
	}
	lookup, err := store.Lookup("write_file", filepath.Join(root, "notes.txt"), sandbox.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.Matched {
		t.Fatalf("session approval must not persist a grant, got %#v", lookup)
	}
}

func TestRunCommandPrefixApprovalSkipsLaterMatchingBashPrompt(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "bash"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"command":"echo prefix-ok"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-2", ToolName: "bash"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-2", ArgumentsFragment: `{"command":"echo prefix-ok again"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-2"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}
	var requests []PermissionRequest
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "run twice", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        sandbox.DefaultPolicy(),
			Backend:       sandbox.Backend{Name: sandbox.BackendPolicyOnly, Message: "policy-only fallback"},
		}),
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			requests = append(requests, request)
			if !containsPermissionDecision(request.AvailableDecisions, PermissionDecisionAllowPrefix) {
				t.Fatalf("bash prompt missing prefix approval decision: %#v", request.AvailableDecisions)
			}
			if !equalStringSlices(request.CommandPrefix, []string{"echo", "prefix-ok"}) {
				t.Fatalf("request command prefix = %#v", request.CommandPrefix)
			}
			return PermissionDecision{Action: PermissionDecisionAllowPrefix, Reason: "trust this command prefix"}, nil
		},
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if len(requests) != 1 {
		t.Fatalf("expected only the first bash call to prompt, got %#v", requests)
	}
	if len(permissionEvents) != 2 {
		t.Fatalf("expected two bash permission events, got %#v", permissionEvents)
	}
	for index, event := range permissionEvents {
		if event.DecisionAction != PermissionDecisionAllowPrefix || !event.PermissionGranted {
			t.Fatalf("event %d = %#v, want prefix allow", index, event)
		}
	}
}

func TestRunPersistentCommandPrefixApprovalSkipsFutureSessionPrompt(t *testing.T) {
	root := t.TempDir()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))
	policy := sandbox.DefaultPolicy()
	policy.Network = sandbox.NetworkAllow

	firstProvider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "bash"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"command":"echo persist-ok"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "first done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}
	var firstRequests []PermissionRequest
	var firstEvents []PermissionEvent
	if _, err := Run(context.Background(), "run once", firstProvider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        policy,
			Store:         store,
			Backend:       sandbox.Backend{Name: sandbox.BackendPolicyOnly, Message: "policy-only fallback"},
		}),
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			firstRequests = append(firstRequests, request)
			if !containsPermissionDecision(request.AvailableDecisions, PermissionDecisionAlwaysAllowPrefix) {
				t.Fatalf("bash prompt missing persistent prefix approval decision: %#v", request.AvailableDecisions)
			}
			return PermissionDecision{Action: PermissionDecisionAlwaysAllowPrefix, Reason: "trust this command prefix"}, nil
		},
		OnPermission: func(event PermissionEvent) {
			firstEvents = append(firstEvents, event)
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(firstRequests) != 1 {
		t.Fatalf("expected first run to prompt once, got %#v", firstRequests)
	}
	if len(firstEvents) != 1 || firstEvents[0].DecisionAction != PermissionDecisionAlwaysAllowPrefix {
		t.Fatalf("expected persistent prefix event, got %#v", firstEvents)
	}
	prefixes, err := store.ListCommandPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 || !equalStringSlices(prefixes[0].Prefix, []string{"echo", "persist-ok"}) {
		t.Fatalf("persisted prefixes = %#v, want echo persist-ok", prefixes)
	}

	secondProvider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-2", ToolName: "bash"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-2", ArgumentsFragment: `{"command":"echo persist-ok again"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-2"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "second done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}
	var secondEvents []PermissionEvent
	if _, err := Run(context.Background(), "run again", secondProvider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        policy,
			Store:         store,
			Backend:       sandbox.Backend{Name: sandbox.BackendPolicyOnly, Message: "policy-only fallback"},
		}),
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			t.Fatalf("persistent command prefix should skip prompt, got %#v", request)
			return PermissionDecision{}, nil
		},
		OnPermission: func(event PermissionEvent) {
			secondEvents = append(secondEvents, event)
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(secondEvents) != 1 || secondEvents[0].DecisionAction != PermissionDecisionAlwaysAllowPrefix || !secondEvents[0].PermissionGranted {
		t.Fatalf("expected persistent prefix match event, got %#v", secondEvents)
	}
}

func TestRunPersistentCommandPrefixDoesNotBypassNetworkDeny(t *testing.T) {
	root := t.TempDir()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GrantCommandPrefix(sandbox.CommandPrefixInput{ToolName: "bash", Prefix: []string{"curl"}}); err != nil {
		t.Fatalf("seed command prefix: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "bash"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"command":"curl https://example.com"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}
	var events []PermissionEvent
	if _, err := Run(context.Background(), "curl", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        sandbox.DefaultPolicy(),
			Store:         store,
			Backend:       sandbox.Backend{Name: sandbox.BackendPolicyOnly, Message: "policy-only fallback"},
		}),
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			t.Fatalf("matched persistent command prefix should not prompt before sandbox denial, got %#v", request)
			return PermissionDecision{}, nil
		},
		OnPermission: func(event PermissionEvent) {
			events = append(events, event)
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != PermissionActionDeny || events[0].Violation == nil || events[0].Violation.Code != sandbox.ViolationNetwork {
		t.Fatalf("expected network sandbox denial despite prefix match, got %#v", events)
	}
}

func TestRunDoesNotOfferPrefixApprovalForUnsafeBashCommand(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "bash"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"command":"echo hi && echo bye","prefix_rule":["echo"]}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	_, err := Run(context.Background(), "run unsafe", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        sandbox.DefaultPolicy(),
			Backend:       sandbox.Backend{Name: sandbox.BackendPolicyOnly, Message: "policy-only fallback"},
		}),
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			if len(request.CommandPrefix) != 0 {
				t.Fatalf("unsafe command should not have a command prefix: %#v", request.CommandPrefix)
			}
			if containsPermissionDecision(request.AvailableDecisions, PermissionDecisionAllowPrefix) {
				t.Fatalf("unsafe command should not offer prefix approval: %#v", request.AvailableDecisions)
			}
			return PermissionDecision{Action: PermissionDecisionDeny, Reason: "deny unsafe"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunAlwaysAllowWithoutSandboxStillAllowsCall(t *testing.T) {
	// Choosing "always allow" with NO sandbox engine configured must still allow
	// THIS call (there is just nowhere to persist a grant for future calls). The
	// prior code denied it because persistPermissionGrant errors when Sandbox==nil.
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write approved")
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox:        nil, // no sandbox engine configured
		OnPermissionRequest: func(_ context.Context, _ PermissionRequest) (PermissionDecision, error) {
			return PermissionDecision{Action: PermissionDecisionAlwaysAllow, Reason: "trust it"}, nil
		},
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "write approved" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	// The tool actually ran: the file exists. Under the bug it would be denied
	// and never written.
	if _, statErr := os.Stat(filepath.Join(root, "notes.txt")); statErr != nil {
		t.Fatalf("write_file should have run under always-allow with no sandbox: %v", statErr)
	}
	if len(permissionEvents) != 1 || permissionEvents[0].Action != PermissionActionAllow || !permissionEvents[0].PermissionGranted {
		t.Fatalf("expected one allow event with permission granted, got %#v", permissionEvents)
	}
}

func containsPermissionDecision(decisions []PermissionDecisionAction, want PermissionDecisionAction) bool {
	for _, decision := range decisions {
		if decision == want {
			return true
		}
	}
	return false
}

// cancelMidStreamProvider cancels the run while the provider stream is open and
// never sends a terminal event, so CollectStream returns via ctx.Done().
type cancelMidStreamProvider struct{ cancel context.CancelFunc }

func (p cancelMidStreamProvider) StreamCompletion(_ context.Context, _ zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.cancel()
	return make(chan zeroruntime.StreamEvent), nil
}

func TestRunCancellationPreservesContextCanceledIdentity(t *testing.T) {
	// On cancellation the collected error is the stringified ctx error; the loop
	// must return ctx.Err() itself so errors.Is(err, context.Canceled) holds for
	// callers that branch on it.
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Run(ctx, "hi", cancelMidStreamProvider{cancel: cancel}, Options{Registry: tools.NewRegistry()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected errors.Is(err, context.Canceled), got %v (%T)", err, err)
	}
}

func TestRunGrantsPromptToolInUnsafeMode(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write done")
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeUnsafe,
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "write done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("expected written content, got %q", content)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.Action != PermissionActionAllow || !event.PermissionGranted {
		t.Fatalf("expected unsafe approval permission event, got %#v", event)
	}
	if event.ToolName != "write_file" || event.PermissionMode != PermissionModeUnsafe {
		t.Fatalf("unexpected unsafe approval metadata: %#v", event)
	}
}

func TestRunEmitsPermissionEventForPersistentSandboxGrant(t *testing.T) {
	root := t.TempDir()
	store, err := sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.Grant(sandbox.GrantInput{
		ToolName:    "write_file",
		Decision:    sandbox.GrantAllow,
		MaxAutonomy: sandbox.AutonomyHigh,
		Reason:      "trusted workspace edits",
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write done")
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        sandbox.DefaultPolicy(),
			Store:         store,
		}),
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "write done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("expected written content, got %q", content)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.Action != PermissionActionAllow || event.PermissionGranted {
		t.Fatalf("expected grant-backed allow without unsafe permission, got %#v", event)
	}
	if !event.GrantMatched || event.Grant == nil || event.Grant.ToolName != grant.ToolName || event.Grant.Decision != sandbox.GrantAllow {
		t.Fatalf("expected persistent grant details, got %#v", event)
	}
}

func TestRunAppliesSandboxEvenInUnsafeMode(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWritePathThenAnswer(outside, "sandbox handled")
	var permissionEvents []PermissionEvent

	result, err := Run(context.Background(), "write outside", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeUnsafe,
		Autonomy:       string(sandbox.AutonomyHigh),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        sandbox.DefaultPolicy(),
		}),
		OnPermission: func(event PermissionEvent) {
			permissionEvents = append(permissionEvents, event)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "sandbox handled" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("expected sandbox to prevent outside write, stat error: %v", err)
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if !strings.Contains(lastMessage.Content, "Sandbox violation") || !strings.Contains(lastMessage.Content, "outside_workspace") {
		t.Fatalf("expected sandbox violation tool result, got %q", lastMessage.Content)
	}
	if len(permissionEvents) != 1 {
		t.Fatalf("expected one permission event, got %#v", permissionEvents)
	}
	event := permissionEvents[0]
	if event.Action != PermissionActionDeny {
		t.Fatalf("expected denied permission event, got %#v", event)
	}
	if event.Violation == nil || event.Violation.Code != sandbox.ViolationOutsideWorkspace {
		t.Fatalf("expected outside_workspace violation in permission event, got %#v", event)
	}
	if event.Risk.Level != sandbox.RiskCritical {
		t.Fatalf("expected critical risk in permission event, got %#v", event)
	}
}

func TestRunStopsAfterMaxTurns(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, filepath.Join(root, "notes.txt"), "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "read_file"},
			{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt"}`},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	result, err := Run(context.Background(), "loop", provider, Options{
		Registry: registry,
		MaxTurns: 1,
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "Agent reached maximum number of turns without a final answer." {
		t.Fatalf("expected max-turns answer, got %q", result.FinalAnswer)
	}
	if result.Turns != 1 {
		t.Fatalf("expected one turn, got %d", result.Turns)
	}
}

func TestRunRequestsFinalAnswerAfterMaxTurns(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, filepath.Join(root, "notes.txt"), "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "read_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "I read notes.txt and found alpha."},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "loop", provider, Options{
		Registry: registry,
		MaxTurns: 1,
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "I read notes.txt and found alpha." {
		t.Fatalf("expected final answer from finalization turn, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected final no-tools request after max turns, got %d requests", len(provider.requests))
	}
	finalRequest := provider.requests[1]
	if len(finalRequest.Tools) != 0 {
		t.Fatalf("finalization request must not advertise tools, got %#v", finalRequest.Tools)
	}
	lastMessage := finalRequest.Messages[len(finalRequest.Messages)-1]
	if lastMessage.Role != zeroruntime.MessageRoleUser || !strings.Contains(lastMessage.Content, "tool-turn limit") {
		t.Fatalf("expected max-turns finalization prompt, got %#v", lastMessage)
	}
	if result.Turns != 1 {
		t.Fatalf("expected tool turns to remain 1, got %d", result.Turns)
	}
}

func providerCallingWriteFileThenAnswer(answer string) *mockProvider {
	return providerCallingWritePathThenAnswer("notes.txt", answer)
}

func providerCallingWritePathThenAnswer(path string, answer string) *mockProvider {
	return &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "write_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":` + quoteJSONString(path) + `,"content":"hello"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: answer},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}
}

func quoteJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func equalPermissionDecisions(a, b []PermissionDecisionAction) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRunAppendsConfirmationPolicyToSystemPrompt(t *testing.T) {
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "ok"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	if _, err := Run(context.Background(), "do work", provider, Options{
		Registry: tools.NewRegistry(),
	}); err != nil {
		t.Fatal(err)
	}

	if len(provider.requests) == 0 {
		t.Fatal("expected at least one provider request")
	}
	system := provider.requests[0].Messages[0]
	if system.Role != zeroruntime.MessageRoleSystem {
		t.Fatalf("expected first message to be system, got %s", system.Role)
	}
	// The overhauled core prompt: identity + the mandatory testing gate.
	for _, marker := range []string{"You are Zero", "Testing gate"} {
		if !strings.Contains(system.Content, marker) {
			t.Fatalf("system prompt missing core marker %q: %q", marker, system.Content)
		}
	}
	// Key markers from CONFIRMATION_POLICY.md must be present so the model self-polices.
	for _, marker := range []string{"Confirmation Modes", "BLOCKED", "ALWAYS CONFIRM"} {
		if !strings.Contains(system.Content, marker) {
			t.Fatalf("system prompt missing confirmation policy marker %q", marker)
		}
	}
}

func TestSystemPromptEmbedsConfirmationPolicy(t *testing.T) {
	prompt := buildSystemPrompt(Options{})
	if !strings.HasPrefix(prompt, "You are Zero") {
		t.Fatalf("system prompt should start with the core instructions, got %q", prompt)
	}
	if !strings.Contains(prompt, "Confirmation Modes") {
		t.Fatalf("embedded confirmation policy missing from system prompt")
	}
	// No workspace context without a cwd (keeps headless/test runs deterministic).
	if strings.Contains(prompt, "<environment>") {
		t.Fatalf("system prompt should omit the environment block when cwd is unset")
	}
}

func TestGitBranchForPromptResolvesRelativeWorktreeGitdir(t *testing.T) {
	root := t.TempDir()
	// The real gitdir for the worktree, where HEAD lives.
	gitdir := filepath.Join(root, "realgit", "worktrees", "wt")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/feature-x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The worktree checkout: a .git FILE pointing at the gitdir via a RELATIVE path.
	worktree := filepath.Join(root, "checkout")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: ../realgit/worktrees/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitBranchForPrompt(worktree); got != "feature-x" {
		t.Fatalf("gitBranchForPrompt = %q, want feature-x (relative worktree gitdir resolved against cwd)", got)
	}
}

func TestBuildSystemPromptInjectsWorkspaceContext(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("Always run `make lint` before committing."), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt(Options{Cwd: cwd})
	if !strings.Contains(prompt, "<environment>") || !strings.Contains(prompt, "Working directory: "+cwd) {
		t.Fatalf("expected environment block with cwd, got %q", prompt)
	}
	if !strings.Contains(prompt, "Project guidelines (AGENTS.md)") || !strings.Contains(prompt, "make lint") {
		t.Fatalf("expected AGENTS.md project guidelines injected, got %q", prompt)
	}
}

func TestBuildSystemPromptInjectsHostShellContext(t *testing.T) {
	prompt := buildSystemPrompt(Options{Cwd: t.TempDir()})
	if !strings.Contains(prompt, "Operating system: "+runtime.GOOS) {
		t.Fatalf("expected operating system in environment block, got %q", prompt)
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(prompt, "Windows cmd.exe syntax") || !strings.Contains(prompt, "cwd argument") {
			t.Fatalf("expected Windows cmd.exe shell guidance in prompt, got %q", prompt)
		}
	} else if !strings.Contains(prompt, "/bin/sh syntax") {
		t.Fatalf("expected POSIX shell guidance in prompt, got %q", prompt)
	}
}

func TestBuildSystemPromptInjectsRepoMap(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "internal", "service"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "internal", "service", "service.go"), []byte("package service\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "node_modules", "leftpad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "node_modules", "leftpad", "index.js"), []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt := buildSystemPrompt(Options{Cwd: cwd})
	for _, want := range []string{
		"## Repo map",
		"Important files: go.mod",
		"Languages:",
		"internal/service/service.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected repo map marker %q in prompt, got %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "node_modules/leftpad") {
		t.Fatalf("repo map should omit ignored dependency dirs, got %q", prompt)
	}
}

func TestBuildSystemPromptAllowsSpecModeOverride(t *testing.T) {
	prompt := buildSystemPrompt(Options{SystemPrompt: specmode.DraftSystemPrompt})
	if !strings.HasPrefix(prompt, "Specification drafting is active.") {
		t.Fatalf("expected spec prompt override, got %q", prompt)
	}
	if !strings.Contains(prompt, "Confirmation Modes") {
		t.Fatalf("expected confirmation policy to remain appended")
	}
}

func TestSpecDraftAdvertisesOnlySafeDraftTools(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(root) {
		registry.Register(tool)
	}
	specmode.RegisterDraftTools(registry, root, nil)
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventText, Content: "done"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	_, err := Run(context.Background(), "draft", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeSpecDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, definition := range provider.requests[0].Tools {
		names[definition.Name] = true
	}
	for _, want := range []string{"read_file", "list_directory", "glob", "grep", "skill", "ask_user", specmode.SubmitToolName} {
		if !names[want] {
			t.Fatalf("spec draft tools missing %q from %#v", want, names)
		}
	}
	for _, denied := range []string{"write_file", "edit_file", "apply_patch", "bash", "update_plan", "web_fetch"} {
		if names[denied] {
			t.Fatalf("spec draft advertised denied tool %q in %#v", denied, names)
		}
	}
}

func TestSpecDraftDeniesHiddenToolCalls(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("done")

	result, err := Run(context.Background(), "draft", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeSpecDraft,
		MaxTurns:       2,
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer after denial, got %q", result.FinalAnswer)
	}
	var denied string
	for _, message := range result.Messages {
		if message.Role == zeroruntime.MessageRoleTool {
			denied = message.Content
			break
		}
	}
	if !strings.Contains(denied, "not available in spec-draft mode") {
		t.Fatalf("expected spec-draft denial, got %q", denied)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("write_file should not have written notes.txt, stat err=%v", err)
	}
}

func TestSpecDraftDeniesBashToolCalls(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "bash"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"command":"printf ran > ran.txt"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "done"},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "draft", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeSpecDraft,
		MaxTurns:       2,
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer after denial, got %q", result.FinalAnswer)
	}
	var denied string
	for _, message := range result.Messages {
		if message.Role == zeroruntime.MessageRoleTool {
			denied = message.Content
			break
		}
	}
	if !strings.Contains(denied, "not available in spec-draft mode") {
		t.Fatalf("expected spec-draft bash denial, got %q", denied)
	}
	if _, err := os.Stat(filepath.Join(root, "ran.txt")); !os.IsNotExist(err) {
		t.Fatalf("bash should not have written ran.txt, stat err=%v", err)
	}
}

func TestRunStopsWhenSubmitSpecReturnsReviewControl(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	specmode.RegisterDraftTools(registry, root, nil)
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "exit-1", ToolName: specmode.SubmitToolName},
			{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "exit-1", ArgumentsFragment: `{"title":"Implementation Plan","plan":"# Goal\n\nAdd implementation plan."}`},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "exit-1"},
			{Type: zeroruntime.StreamEventDone},
		}},
	}

	result, err := Run(context.Background(), "draft", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeSpecDraft,
		MaxTurns:       3,
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonSpecReviewRequired {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, StopReasonSpecReviewRequired)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected run to stop after submit_spec, got %d requests", len(provider.requests))
	}
	if !strings.Contains(result.FinalAnswer, ".zero/specs/") {
		t.Fatalf("final answer should mention saved spec, got %q", result.FinalAnswer)
	}
}

func assertMessage(t *testing.T, message zeroruntime.Message, role zeroruntime.MessageRole, contentContains string) {
	t.Helper()

	if message.Role != role {
		t.Fatalf("expected role %s, got %s", role, message.Role)
	}
	if contentContains != "" && !strings.Contains(message.Content, contentContains) {
		t.Fatalf("expected message content to contain %q, got %q", contentContains, message.Content)
	}
}

func writeAgentTestFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunRetriesOnDroppedToolCall(t *testing.T) {
	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventText, Content: "Let me write the files."},
				{Type: zeroruntime.StreamEventToolCallDropped},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "All done."},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "build it", provider, Options{Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected the loop to retry (2 turns), got %d", len(provider.requests))
	}
	if result.FinalAnswer != "All done." {
		t.Fatalf("expected final answer from retry turn, got %q", result.FinalAnswer)
	}
	// The retry turn must carry synthetic feedback to the model.
	var fedback bool
	for _, m := range provider.requests[1].Messages {
		if m.Role == zeroruntime.MessageRoleUser && strings.Contains(strings.ToLower(m.Content), "tool name") {
			fedback = true
		}
	}
	if !fedback {
		t.Fatalf("expected a synthetic tool-error message on the retry turn, messages: %+v", provider.requests[1].Messages)
	}
}

func TestRunSurfacesDroppedToolCallAlongsideValidCall(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))

	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				// One valid tool call AND a dropped (nameless) call in the same turn.
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "read_file"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt"}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: zeroruntime.StreamEventToolCallDropped},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "All done."},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "do it", provider, Options{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "All done." {
		t.Fatalf("expected final answer from retry turn, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected the loop to continue (2 turns), got %d", len(provider.requests))
	}
	// The valid tool call must still have executed (a tool result for call-1).
	var sawToolResult bool
	for _, m := range provider.requests[1].Messages {
		if m.Role == zeroruntime.MessageRoleTool && m.ToolCallID == "call-1" {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Fatalf("expected the valid tool call to execute, messages: %+v", provider.requests[1].Messages)
	}
	// The dropped call must ALSO be surfaced via the malformed-call notice.
	var sawDroppedNotice bool
	for _, m := range provider.requests[1].Messages {
		if m.Role == zeroruntime.MessageRoleUser && strings.Contains(strings.ToLower(m.Content), "malformed") {
			sawDroppedNotice = true
		}
	}
	if !sawDroppedNotice {
		t.Fatalf("expected a malformed-call notice for the dropped call, messages: %+v", provider.requests[1].Messages)
	}
}

// TestRunAppendsAbortedPlaceholderForUnexecutedToolCallsOnGuardStop verifies
// that when a turn carries multiple tool calls and the repeated-failure guard
// halts the run on a call that is NOT the last, every advertised tool_use still
// gets a matching tool_result: the executed call gets its real result and the
// remaining (unexecuted) calls get aborted-placeholder results, so the recorded
// messages stay structurally valid for a strict provider replay.
func TestRunAppendsAbortedPlaceholderForUnexecutedToolCallsOnGuardStop(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(alwaysFailingTool{})

	// Three prior single-call failures prime the streak to one below the stop
	// cap, so the FIRST call of the next multi-call turn is the 4th failure and
	// trips outcome.Stop.
	primingTurns := repeatedFlakyTurns(toolFailureStopAt - 1)

	// The halting turn carries TWO tool calls: the first (flaky-stop) trips the
	// guard before the second (flaky-2) is executed.
	haltingTurn := []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "flaky-stop", ToolName: "flaky"},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "flaky-stop"},
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "flaky-2", ToolName: "flaky"},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "flaky-2"},
		{Type: zeroruntime.StreamEventDone},
	}

	provider := &mockProvider{turns: append(primingTurns, haltingTurn)}

	result, err := Run(context.Background(), "go", provider, Options{Registry: registry, MaxTurns: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.FinalAnswer, "flaky") || !strings.Contains(result.FinalAnswer, "failed") {
		t.Fatalf("expected repeated-failure stop answer, got %q", result.FinalAnswer)
	}

	// Both tool calls must have a matching tool_result message.
	toolResultIDs := map[string]string{}
	for _, message := range result.Messages {
		if message.Role == zeroruntime.MessageRoleTool {
			toolResultIDs[message.ToolCallID] = message.Content
		}
	}
	if _, ok := toolResultIDs["flaky-stop"]; !ok {
		t.Fatalf("expected a tool result for the executed call flaky-stop, messages: %+v", result.Messages)
	}
	placeholder, ok := toolResultIDs["flaky-2"]
	if !ok {
		t.Fatalf("expected an aborted-placeholder tool result for the unexecuted call flaky-2, messages: %+v", result.Messages)
	}
	if !strings.Contains(strings.ToLower(placeholder), "aborted") {
		t.Fatalf("expected the placeholder result to mark the call as aborted, got %q", placeholder)
	}

	// Every tool_use in the final assistant message must have a matching result.
	for _, message := range result.Messages {
		if message.Role != zeroruntime.MessageRoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if _, ok := toolResultIDs[call.ID]; !ok {
				t.Fatalf("tool_use %q (%s) has no matching tool_result", call.ID, call.Name)
			}
		}
	}
}

type secretEmittingTool struct{ output string }

func (t secretEmittingTool) Name() string        { return "leak" }
func (t secretEmittingTool) Description() string { return "emits text for testing" }
func (t secretEmittingTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false}
}
func (t secretEmittingTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow}
}
func (t secretEmittingTool) Run(_ context.Context, _ map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK, Output: t.output}
}

func TestRunScrubsSecretsFromToolOutput(t *testing.T) {
	secret := "sk-proj-ABCDEFGHIJKLMNOP1234567890"
	registry := tools.NewRegistry()
	registry.Register(secretEmittingTool{output: "the token is " + secret + " ok"})

	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "leak"},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c1"},
			{Type: zeroruntime.StreamEventDone},
		},
		{
			{Type: zeroruntime.StreamEventText, Content: "done"},
			{Type: zeroruntime.StreamEventDone},
		},
	}}

	var captured ToolResult
	_, err := Run(context.Background(), "go", provider, Options{
		Registry:     registry,
		OnToolResult: func(r ToolResult) { captured = r },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(captured.Output, secret) {
		t.Fatalf("secret leaked into tool result output: %q", captured.Output)
	}
	if !captured.Redacted {
		t.Error("expected Redacted=true when a secret was scrubbed")
	}
	if !strings.Contains(strings.ToLower(captured.Output), "redacted") {
		t.Errorf("expected a redaction reminder, got %q", captured.Output)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected a second turn carrying the tool result")
	}
	for _, m := range provider.requests[1].Messages {
		if strings.Contains(m.Content, secret) {
			t.Fatalf("secret leaked into model message: %q", m.Content)
		}
	}
}

func TestRunDoesNotFlagCleanToolOutput(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(secretEmittingTool{output: "perfectly ordinary output"})
	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "leak"},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c1"},
			{Type: zeroruntime.StreamEventDone},
		},
		{{Type: zeroruntime.StreamEventText, Content: "done"}, {Type: zeroruntime.StreamEventDone}},
	}}
	var captured ToolResult
	if _, err := Run(context.Background(), "go", provider, Options{Registry: registry, OnToolResult: func(r ToolResult) { captured = r }}); err != nil {
		t.Fatal(err)
	}
	if captured.Redacted {
		t.Error("clean output should not be flagged Redacted")
	}
	if strings.Contains(strings.ToLower(captured.Output), "redacted") {
		t.Errorf("clean output should not get a reminder, got %q", captured.Output)
	}
}
