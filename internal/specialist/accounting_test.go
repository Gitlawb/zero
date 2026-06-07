package specialist

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/background"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/streamjson"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestExecutorRecordsForegroundLifecycleAndUsageRollup(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	parent, err := store.Create(sessions.CreateInput{SessionID: "parent_session"})
	if err != nil {
		t.Fatalf("Create parent returned error: %v", err)
	}
	zero := 0
	executor := Executor{
		BinaryPath:   "/usr/local/bin/zero",
		SessionStore: store,
		NewSessionID: func() (string, error) { return "child_task", nil },
		Load: func(LoadOptions) (LoadResult, error) {
			return LoadResult{Specialists: []Manifest{{
				Metadata:      Metadata{Name: "worker", Description: "Does focused work"},
				SystemPrompt:  "Work carefully.",
				ResolvedTools: []string{"read_file"},
			}}}, nil
		},
		RunChild: func(context.Context, string, []string) (ChildRunResult, error) {
			return ChildRunResult{
				Events: []streamjson.Event{
					{Type: streamjson.EventRunStart, RunID: "run_1", SessionID: "child_task"},
					{Type: streamjson.EventUsage, RunID: "run_1", PromptTokens: ptrInt(12), CompletionTokens: ptrInt(5), TotalTokens: ptrInt(17)},
					{Type: streamjson.EventFinal, RunID: "run_1", Text: "done"},
					{Type: streamjson.EventRunEnd, RunID: "run_1", Status: "success", ExitCode: &zero},
				},
				ExitCode: 0,
			}, nil
		},
	}

	result, err := executor.Run(context.Background(), TaskParameters{
		Name:        "worker",
		Prompt:      "inspect auth",
		Description: "Auth check",
	}, TaskRunOptions{
		ParentSessionID: parent.SessionID,
		ToolCallID:      "call_1",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Result.Status != tools.StatusOK {
		t.Fatalf("Run status = %s, output=%s", result.Result.Status, result.Result.Output)
	}

	events, err := store.ReadEvents(parent.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if got, want := eventTypes(events), []sessions.EventType{sessions.EventSpecialistStart, sessions.EventUsage, sessions.EventSpecialistStop}; strings.Join(eventTypesString(got), ",") != strings.Join(eventTypesString(want), ",") {
		t.Fatalf("event types = %#v, want %#v", got, want)
	}
	startPayload := eventPayload(t, events[0])
	if payloadString(startPayload, "source") != "specialist" ||
		payloadString(startPayload, "childSessionId") != "child_task" ||
		payloadString(startPayload, "specialist") != "worker" ||
		payloadString(startPayload, "toolCallId") != "call_1" ||
		payloadString(startPayload, "mode") != "foreground" ||
		payloadBool(startPayload, "background") {
		t.Fatalf("start payload = %#v", startPayload)
	}
	usagePayload := eventPayload(t, events[1])
	if payloadString(usagePayload, "source") != "specialist" ||
		payloadString(usagePayload, "childSessionId") != "child_task" ||
		payloadString(usagePayload, "runId") != "run_1" ||
		payloadInt(usagePayload, "promptTokens") != 12 ||
		payloadInt(usagePayload, "completionTokens") != 5 ||
		payloadInt(usagePayload, "totalTokens") != 17 ||
		payloadInt(usagePayload, "usageEvents") != 1 {
		t.Fatalf("usage payload = %#v", usagePayload)
	}
	stopPayload := eventPayload(t, events[2])
	if payloadString(stopPayload, "status") != "success" ||
		payloadInt(stopPayload, "exitCode") != 0 ||
		!payloadBool(stopPayload, "usageRolledUp") {
		t.Fatalf("stop payload = %#v", stopPayload)
	}
}

func TestOutputToolRollsUpCompletedBackgroundUsageOnce(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	parent, err := store.Create(sessions.CreateInput{SessionID: "parent_session"})
	if err != nil {
		t.Fatalf("Create parent returned error: %v", err)
	}
	manager, err := background.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	outputFile, err := manager.Register(background.RegisterInput{
		TaskID:         "child_task",
		Type:           "specialist",
		SpecialistName: "worker",
		Description:    "Auth check",
		ParentID:       parent.SessionID,
	})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if err := os.WriteFile(outputFile, []byte(strings.Join([]string{
		`{"schemaVersion":1,"type":"run_start","runId":"run_1","sessionId":"child_task"}`,
		`{"schemaVersion":1,"type":"usage","runId":"run_1","promptTokens":20,"completionTokens":8,"totalTokens":28}`,
		`{"schemaVersion":1,"type":"final","runId":"run_1","text":"done"}`,
		`{"schemaVersion":1,"type":"run_end","runId":"run_1","status":"success","exitCode":0}`,
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := manager.UpdateStatus("child_task", background.StatusCompleted, 0); err != nil {
		t.Fatalf("UpdateStatus returned error: %v", err)
	}
	tool := NewOutputTool(manager)
	tool.SessionStore = store

	first := tool.Run(context.Background(), map[string]any{"task_id": "child_task"})
	second := tool.Run(context.Background(), map[string]any{"task_id": "child_task"})

	if first.Status != tools.StatusOK || second.Status != tools.StatusOK {
		t.Fatalf("TaskOutput results = %#v %#v", first, second)
	}
	events, err := store.ReadEvents(parent.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if usageEvents := eventsOfType(events, sessions.EventUsage); len(usageEvents) != 1 {
		t.Fatalf("usage events = %#v", events)
	}
	if stopEvents := eventsOfType(events, sessions.EventSpecialistStop); len(stopEvents) != 1 {
		t.Fatalf("stop events = %#v", events)
	}
	usagePayload := eventPayload(t, eventsOfType(events, sessions.EventUsage)[0])
	if payloadString(usagePayload, "mode") != "background" ||
		payloadInt(usagePayload, "promptTokens") != 20 ||
		payloadInt(usagePayload, "completionTokens") != 8 ||
		payloadInt(usagePayload, "totalTokens") != 28 {
		t.Fatalf("background usage payload = %#v", usagePayload)
	}
}

func ptrInt(value int) *int {
	return &value
}

func eventTypes(events []sessions.Event) []sessions.EventType {
	types := make([]sessions.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func eventTypesString(types []sessions.EventType) []string {
	values := make([]string, 0, len(types))
	for _, eventType := range types {
		values = append(values, string(eventType))
	}
	return values
}

func eventsOfType(events []sessions.Event, eventType sessions.EventType) []sessions.Event {
	matches := []sessions.Event{}
	for _, event := range events {
		if event.Type == eventType {
			matches = append(matches, event)
		}
	}
	return matches
}

func eventPayload(t *testing.T, event sessions.Event) map[string]any {
	t.Helper()
	payload := map[string]any{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload returned error: %v", err)
	}
	return payload
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func payloadBool(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func payloadInt(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}
