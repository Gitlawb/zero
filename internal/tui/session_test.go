package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type scriptedProvider struct {
	scripts  [][]zeroruntime.StreamEvent
	requests []zeroruntime.CompletionRequest
	calls    int
}

func (provider *scriptedProvider) StreamCompletion(
	ctx context.Context,
	request zeroruntime.CompletionRequest,
) (<-chan zeroruntime.StreamEvent, error) {
	provider.requests = append(provider.requests, request)
	if len(provider.scripts) == 0 {
		ch := make(chan zeroruntime.StreamEvent)
		close(ch)
		return ch, nil
	}
	index := provider.calls
	provider.calls++
	if index >= len(provider.scripts) {
		index = len(provider.scripts) - 1
	}
	ch := make(chan zeroruntime.StreamEvent, len(provider.scripts[index]))
	for _, event := range provider.scripts[index] {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestPromptSubmitPersistsTUISessionEvents(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "saved"},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 4}},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newModel(context.Background(), Options{
		Cwd:          "repo",
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: store,
	})
	m.input.SetValue("inspect repo")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}

	updated, _ = next.Update(cmd())
	next = updated.(model)

	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one persisted TUI session, got %d", len(list))
	}
	session := list[0]
	if session.Title != "inspect repo" || session.Cwd != "repo" || session.ModelID != "gpt-4.1" || session.Provider != "openai" {
		t.Fatalf("unexpected session metadata: %#v", session)
	}

	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if got := eventTypes(events); !equalEventTypes(got, []sessions.EventType{
		sessions.EventMessage,
		sessions.EventUsage,
		sessions.EventMessage,
	}) {
		t.Fatalf("unexpected event sequence: %#v", got)
	}
	assertPayloadField(t, events[0], "role", "user")
	assertPayloadField(t, events[0], "content", "inspect repo")
	assertPayloadField(t, events[1], "promptTokens", float64(10))
	assertPayloadField(t, events[1], "completionTokens", float64(4))
	assertPayloadField(t, events[1], "totalTokens", float64(14))
	assertPayloadField(t, events[2], "role", "assistant")
	assertPayloadField(t, events[2], "content", "saved")
	if !transcriptContains(next.transcript, "saved") {
		t.Fatalf("expected persisted run to still render assistant text, got %#v", next.transcript)
	}
}

func TestPromptWithoutProviderDoesNotCreateSession(t *testing.T) {
	store := testSessionStore(t)
	m := newModel(context.Background(), Options{SessionStore: store})
	m.input.SetValue("hello")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected missing provider prompt to stay synchronous")
	}
	if !transcriptContains(next.transcript, "No provider configured.") {
		t.Fatalf("expected missing provider message, got %#v", next.transcript)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected missing provider prompt not to create sessions, got %#v", list)
	}
}

func TestPromptSubmitPersistsToolSessionEvents(t *testing.T) {
	store := testSessionStore(t)
	root := t.TempDir()
	writeTestFile(t, root, "notes.txt", "file contents")
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "read_file"},
			{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{"path":"notes.txt"}`},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call_1"},
			{Type: zeroruntime.StreamEventDone},
		},
		{
			{Type: zeroruntime.StreamEventText, Content: "read complete"},
			{Type: zeroruntime.StreamEventDone},
		},
	}}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	m := newModel(context.Background(), Options{
		Cwd:          root,
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     registry,
		SessionStore: store,
	})
	m.input.SetValue("read notes")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	_, _ = next.Update(cmd())

	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one session, got %d", len(list))
	}
	events, err := store.ReadEvents(list[0].SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if got := eventTypes(events); !equalEventTypes(got, []sessions.EventType{
		sessions.EventMessage,
		sessions.EventToolCall,
		sessions.EventToolResult,
		sessions.EventMessage,
	}) {
		t.Fatalf("unexpected event sequence: %#v", got)
	}
	assertPayloadField(t, events[1], "id", "call_1")
	assertPayloadField(t, events[1], "name", "read_file")
	assertPayloadField(t, events[1], "arguments", `{"path":"notes.txt"}`)
	assertPayloadField(t, events[2], "toolCallId", "call_1")
	assertPayloadField(t, events[2], "name", "read_file")
	assertPayloadField(t, events[2], "status", "ok")
	assertPayloadFieldContains(t, events[2], "output", "file contents")
	assertPayloadField(t, events[3], "content", "read complete")
}

func TestPromptSubmitPersistsPermissionSessionEvents(t *testing.T) {
	store := testSessionStore(t)
	root := t.TempDir()
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call_write", ToolName: "write_file"},
			{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call_write", ArgumentsFragment: `{"path":"notes.txt","content":"hello"}`},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call_write"},
			{Type: zeroruntime.StreamEventDone},
		},
		{
			{Type: zeroruntime.StreamEventText, Content: "write blocked"},
			{Type: zeroruntime.StreamEventDone},
		},
	}}
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))
	runtimeMessages := []tea.Msg{}
	m := newModel(context.Background(), Options{
		Cwd:            root,
		ProviderName:   "openai",
		ModelName:      "gpt-4.1",
		Provider:       provider,
		Registry:       registry,
		SessionStore:   store,
		PermissionMode: agent.PermissionModeAsk,
		RuntimeMessageSink: func(msg tea.Msg) {
			runtimeMessages = append(runtimeMessages, msg)
		},
		AgentOptions: agent.Options{
			Autonomy: string(sandbox.AutonomyMedium),
			Sandbox: sandbox.NewEngine(sandbox.EngineOptions{
				WorkspaceRoot: root,
				Policy:        sandbox.DefaultPolicy(),
			}),
		},
	})
	m.input.SetValue("write notes")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	finalMsg := cmd()
	if len(runtimeMessages) != 3 {
		t.Fatalf("expected live tool call, permission, and result messages, got %#v", runtimeMessages)
	}
	for _, runtimeMsg := range runtimeMessages {
		updated, _ = next.Update(runtimeMsg)
		next = updated.(model)
	}
	if !next.pending {
		t.Fatal("expected live runtime rows to leave run pending until final response")
	}
	for _, want := range []string{"tool call: write_file", "permission: write_file prompt", "tool result: write_file error"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected live transcript to contain %q, got %#v", want, next.transcript)
		}
	}
	updated, _ = next.Update(finalMsg)
	next = updated.(model)

	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected prompt-gated write to stay blocked, stat error: %v", err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one session, got %d", len(list))
	}
	events, err := store.ReadEvents(list[0].SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if got := eventTypes(events); !equalEventTypes(got, []sessions.EventType{
		sessions.EventMessage,
		sessions.EventToolCall,
		sessions.EventPermission,
		sessions.EventToolResult,
		sessions.EventMessage,
	}) {
		t.Fatalf("unexpected event sequence: %#v", got)
	}
	assertPayloadField(t, events[2], "toolCallId", "call_write")
	assertPayloadField(t, events[2], "name", "write_file")
	assertPayloadField(t, events[2], "action", "prompt")
	assertPayloadField(t, events[2], "permission", "prompt")
	assertPayloadField(t, events[2], "permissionMode", "ask")
	assertPayloadField(t, events[2], "sideEffect", "write")
	assertPayloadField(t, events[4], "content", "write blocked")
	if !transcriptContains(next.transcript, "permission: write_file prompt") {
		t.Fatalf("expected permission row in transcript, got %#v", next.transcript)
	}
	if countTranscriptRows(next.transcript, rowPermission) != 1 {
		t.Fatalf("expected final response to avoid duplicate permission rows, got %#v", next.transcript)
	}
}

func TestResumeCommandHydratesSessionTranscript(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "Hydrate me", Cwd: "repo", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	appendTestEvent(t, store, session.SessionID, sessions.EventMessage, map[string]any{"role": "user", "content": "previous request"})
	appendTestEvent(t, store, session.SessionID, sessions.EventToolCall, map[string]any{"id": "call_1", "name": "grep", "arguments": `{"pattern":"Zero"}`})
	appendTestEvent(t, store, session.SessionID, sessions.EventPermission, map[string]any{
		"toolCallId":     "call_1",
		"name":           "grep",
		"action":         "allow",
		"permission":     "allow",
		"permissionMode": "auto",
		"sideEffect":     "read",
		"risk":           map[string]any{"level": "low"},
	})
	appendTestEvent(t, store, session.SessionID, sessions.EventToolResult, map[string]any{"toolCallId": "call_1", "name": "grep", "status": "error", "output": "matches"})
	appendTestEvent(t, store, session.SessionID, sessions.EventMessage, map[string]any{"role": "assistant", "content": "previous answer"})
	appendTestEvent(t, store, session.SessionID, sessions.EventError, map[string]any{"message": "old error"})

	m := newModel(context.Background(), Options{SessionStore: store})
	m.input.SetValue("/resume " + session.SessionID)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /resume to hydrate synchronously")
	}
	for _, want := range []string{"Resumed Zero session", session.SessionID, "previous request", "tool call: grep", "permission: grep allow", "tool result: grep error matches", "previous answer", "old error"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected resumed transcript to contain %q, got %#v", want, next.transcript)
		}
	}
	toolCall, ok := findTranscriptRow(next.transcript, rowToolCall)
	if !ok || toolCall.tool != "grep" || toolCall.detail != "Zero" {
		t.Fatalf("expected hydrated tool call metadata, got ok=%v row=%#v", ok, toolCall)
	}
	permissionRow, ok := findTranscriptRow(next.transcript, rowPermission)
	if !ok || permissionRow.tool != "grep" || permissionRow.permission == nil || permissionRow.permission.Action != agent.PermissionActionAllow {
		t.Fatalf("expected hydrated permission metadata, got ok=%v row=%#v", ok, permissionRow)
	}
	toolResult, ok := findTranscriptRow(next.transcript, rowToolResult)
	if !ok || toolResult.tool != "grep" || toolResult.status != tools.StatusError || toolResult.detail != "matches" {
		t.Fatalf("expected hydrated tool result metadata, got ok=%v row=%#v", ok, toolResult)
	}
	if transcriptContains(next.transcript, "zero exec --resume") {
		t.Fatalf("resume should not show headless-only guidance after hydration, got %#v", next.transcript)
	}
}

func TestResumeCommandIsBlockedWhileRunPending(t *testing.T) {
	store := testSessionStore(t)
	active, err := store.Create(sessions.CreateInput{Title: "Active"})
	if err != nil {
		t.Fatalf("Create active returned error: %v", err)
	}
	other, err := store.Create(sessions.CreateInput{Title: "Other"})
	if err != nil {
		t.Fatalf("Create other returned error: %v", err)
	}
	m := newModel(context.Background(), Options{SessionStore: store})
	m.activeSession = active
	m.pending = true
	m.input.SetValue("/resume " + other.SessionID)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected pending /resume to be handled without a command")
	}
	if next.activeSession.SessionID != active.SessionID {
		t.Fatalf("expected pending /resume not to replace active session, got %#v", next.activeSession)
	}
	if !transcriptContains(next.transcript, "Cannot resume sessions while a run is active") {
		t.Fatalf("expected pending resume error, got %#v", next.transcript)
	}
}

func TestResumeLatestHydratesNewestSession(t *testing.T) {
	store := testSessionStore(t)
	older, err := store.Create(sessions.CreateInput{Title: "Older"})
	if err != nil {
		t.Fatalf("Create older returned error: %v", err)
	}
	appendTestEvent(t, store, older.SessionID, sessions.EventMessage, map[string]any{"role": "assistant", "content": "old answer"})
	newer, err := store.Create(sessions.CreateInput{Title: "Newer"})
	if err != nil {
		t.Fatalf("Create newer returned error: %v", err)
	}
	appendTestEvent(t, store, newer.SessionID, sessions.EventMessage, map[string]any{"role": "assistant", "content": "new answer"})

	m := newModel(context.Background(), Options{SessionStore: store})
	m.input.SetValue("/resume latest")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if !transcriptContains(next.transcript, "Newer") || !transcriptContains(next.transcript, "new answer") {
		t.Fatalf("expected latest session to hydrate, got %#v", next.transcript)
	}
	if transcriptContains(next.transcript, "old answer") {
		t.Fatalf("expected /resume latest to skip older session transcript, got %#v", next.transcript)
	}
}

func TestEscCancelRecordsSessionError(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "ignored"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newModel(context.Background(), Options{
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: store,
	})
	m.input.SetValue("cancel me")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(model)

	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one session, got %d", len(list))
	}
	events, err := store.ReadEvents(list[0].SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if got := eventTypes(events); !equalEventTypes(got, []sessions.EventType{
		sessions.EventMessage,
		sessions.EventError,
	}) {
		t.Fatalf("unexpected event sequence after cancel: %#v", got)
	}
	assertPayloadField(t, events[1], "message", "Run cancelled.")
	if next.pending {
		t.Fatal("expected Esc to clear pending state")
	}
}

func TestResumedPromptIncludesSessionContext(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "Existing", Cwd: "repo", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	appendTestEvent(t, store, session.SessionID, sessions.EventMessage, map[string]any{"role": "user", "content": "previous request"})
	appendTestEvent(t, store, session.SessionID, sessions.EventMessage, map[string]any{"role": "assistant", "content": "previous answer"})
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "continued"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	m := newModel(context.Background(), Options{
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: store,
	})
	m.input.SetValue("/resume " + session.SessionID)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	next.input.SetValue("continue")

	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if cmd == nil {
		t.Fatal("expected resumed prompt to start an agent run")
	}
	_, _ = next.Update(cmd())

	if len(provider.requests) != 1 {
		t.Fatalf("expected one provider request, got %d", len(provider.requests))
	}
	messages := provider.requests[0].Messages
	if len(messages) == 0 {
		t.Fatal("expected provider request to include messages")
	}
	prompt := messages[len(messages)-1].Content
	for _, want := range []string{"Continuing Zero session", session.SessionID, "previous request", "previous answer", "Current user request:", "continue"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected resumed prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestResumeCommandReportsMissingSession(t *testing.T) {
	m := newModel(context.Background(), Options{SessionStore: testSessionStore(t)})
	m.input.SetValue("/resume missing_session")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if !transcriptContains(next.transcript, "zero session not found: missing_session") {
		t.Fatalf("expected missing session error, got %#v", next.transcript)
	}
}

func appendTestEvent(t *testing.T, store *sessions.Store, sessionID string, eventType sessions.EventType, payload any) {
	t.Helper()

	if _, err := store.AppendEvent(sessionID, sessions.AppendEventInput{Type: eventType, Payload: payload}); err != nil {
		t.Fatalf("AppendEvent(%s) returned error: %v", eventType, err)
	}
}

func writeTestFile(t *testing.T, root string, name string, content string) {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func eventTypes(events []sessions.Event) []sessions.EventType {
	types := make([]sessions.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func equalEventTypes(left []sessions.EventType, right []sessions.EventType) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func assertPayloadField(t *testing.T, event sessions.Event, key string, want any) {
	t.Helper()

	payload := map[string]any{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("failed to decode payload for %s: %v", event.Type, err)
	}
	if payload[key] != want {
		t.Fatalf("expected payload %s=%#v, got %#v in %#v", key, want, payload[key], payload)
	}
}

func assertPayloadFieldContains(t *testing.T, event sessions.Event, key string, want string) {
	t.Helper()

	payload := map[string]any{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("failed to decode payload for %s: %v", event.Type, err)
	}
	text, ok := payload[key].(string)
	if !ok || !strings.Contains(text, want) {
		t.Fatalf("expected payload %s to contain %q, got %#v in %#v", key, want, payload[key], payload)
	}
}
