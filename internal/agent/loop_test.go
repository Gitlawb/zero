package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
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

	result, err := Run(context.Background(), "write notes", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeAsk,
		Autonomy:       string(sandbox.AutonomyMedium),
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
			WorkspaceRoot: root,
			Policy:        sandbox.DefaultPolicy(),
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
	lookup, err := store.Lookup("write_file", sandbox.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	if !lookup.Matched || lookup.Grant.Decision != sandbox.GrantAllow {
		t.Fatalf("expected persistent allow grant, got %#v", lookup)
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
