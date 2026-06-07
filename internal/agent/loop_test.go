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
	if !strings.Contains(system.Content, defaultSystemPrompt) {
		t.Fatalf("system prompt lost base instruction: %q", system.Content)
	}
	// Key markers from CONFIRMATION_POLICY.md must be present so the model self-polices.
	for _, marker := range []string{"Confirmation Modes", "BLOCKED", "ALWAYS CONFIRM"} {
		if !strings.Contains(system.Content, marker) {
			t.Fatalf("system prompt missing confirmation policy marker %q", marker)
		}
	}
}

func TestSystemPromptEmbedsConfirmationPolicy(t *testing.T) {
	prompt := buildSystemPrompt()
	if !strings.HasPrefix(prompt, defaultSystemPrompt) {
		t.Fatalf("system prompt should start with the base instruction, got %q", prompt)
	}
	if !strings.Contains(prompt, "Confirmation Modes") {
		t.Fatalf("embedded confirmation policy missing from system prompt")
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
