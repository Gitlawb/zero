package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

type mockProvider struct {
	turns    [][]StreamEvent
	requests []CompletionRequest
}

func (provider *mockProvider) StreamCompletion(ctx context.Context, request CompletionRequest) (<-chan StreamEvent, error) {
	provider.requests = append(provider.requests, request)

	events := []StreamEvent{{Type: EventDone}}
	if len(provider.turns) >= len(provider.requests) {
		events = provider.turns[len(provider.requests)-1]
	}

	ch := make(chan StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestRunReturnsProviderText(t *testing.T) {
	provider := &mockProvider{
		turns: [][]StreamEvent{{
			{Type: EventText, Content: "hello"},
			{Type: EventText, Content: " zero"},
			{Type: EventDone},
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
	assertMessage(t, provider.requests[0].Messages[0], RoleSystem, "")
	assertMessage(t, provider.requests[0].Messages[1], RoleUser, "say hi")
}

func TestRunEmitsTextDeltas(t *testing.T) {
	provider := &mockProvider{
		turns: [][]StreamEvent{{
			{Type: EventText, Content: "hello"},
			{Type: EventText, Content: " zero"},
			{Type: EventDone},
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
		turns: [][]StreamEvent{{
			{Type: EventUsage, PromptTokens: 12, CompletionTokens: 5},
			{Type: EventText, Content: "done"},
			{Type: EventDone},
		}},
	}

	var usages []Usage
	_, err := Run(context.Background(), "track usage", provider, Options{
		Registry: tools.NewRegistry(),
		OnUsage:  func(usage Usage) { usages = append(usages, usage) },
	})

	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 1 {
		t.Fatalf("expected one usage event, got %#v", usages)
	}
	if usages[0].PromptTokens != 12 || usages[0].CompletionTokens != 5 {
		t.Fatalf("unexpected usage event: %#v", usages[0])
	}
}

func TestRunExecutesToolCallThroughRegistry(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, filepath.Join(root, "notes.txt"), "alpha\nbeta\n")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]StreamEvent{
			{
				{Type: EventToolCallStart, ToolCallID: "call-1", ToolName: "read_file"},
				{Type: EventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt"}`},
				{Type: EventToolCallEnd, ToolCallID: "call-1"},
				{Type: EventDone},
			},
			{
				{Type: EventText, Content: "read done"},
				{Type: EventDone},
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
	assertMessage(t, lastMessage, RoleTool, "alpha")
	if lastMessage.ToolCallID != "call-1" {
		t.Fatalf("expected tool_call_id call-1, got %q", lastMessage.ToolCallID)
	}
	if len(toolResults) != 1 || toolResults[0].Status != tools.StatusOK {
		t.Fatalf("expected one ok tool result, got %#v", toolResults)
	}
}

func TestRunDeniesPromptToolWithoutUnsafePermission(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write denied")

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
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
}

func TestRunGrantsPromptToolInUnsafeMode(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	provider := providerCallingWriteFileThenAnswer("write done")

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeUnsafe,
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
}

func TestRunStopsAfterMaxTurns(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, filepath.Join(root, "notes.txt"), "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{
		turns: [][]StreamEvent{{
			{Type: EventToolCallStart, ToolCallID: "call-1", ToolName: "read_file"},
			{Type: EventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt"}`},
			{Type: EventToolCallEnd, ToolCallID: "call-1"},
			{Type: EventDone},
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

func providerCallingWriteFileThenAnswer(answer string) *mockProvider {
	return &mockProvider{
		turns: [][]StreamEvent{
			{
				{Type: EventToolCallStart, ToolCallID: "call-1", ToolName: "write_file"},
				{Type: EventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"path":"notes.txt","content":"hello"}`},
				{Type: EventToolCallEnd, ToolCallID: "call-1"},
				{Type: EventDone},
			},
			{
				{Type: EventText, Content: answer},
				{Type: EventDone},
			},
		},
	}
}

func assertMessage(t *testing.T, message Message, role Role, contentContains string) {
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
