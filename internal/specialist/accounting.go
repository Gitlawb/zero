package specialist

import (
	"encoding/json"
	"strings"

	"github.com/Gitlawb/zero/internal/background"
	"github.com/Gitlawb/zero/internal/sessions"
)

const specialistAccountingSource = "specialist"

type specialistAccountingInput struct {
	ParentSessionID string
	ChildSessionID  string
	SpecialistName  string
	Description     string
	ToolCallID      string
	Mode            string
	Background      bool
	PID             int
}

func (executor Executor) recordSpecialistStart(input specialistAccountingInput) {
	payload := baseSpecialistPayload(input)
	_, _ = appendSpecialistSessionEvent(executor.SessionStore, input.ParentSessionID, sessions.EventSpecialistStart, payload)
}

func (executor Executor) recordSpecialistStop(input specialistAccountingInput, summary StreamResult, status string, exitCode int, runErr error, usageRolledUp bool) {
	store := accountingStore(executor.SessionStore)
	if specialistEventExists(store, input.ParentSessionID, sessions.EventSpecialistStop, input.ChildSessionID, summary.RunID) {
		return
	}
	payload := baseSpecialistPayload(input)
	if summary.RunID != "" {
		payload["runId"] = summary.RunID
	}
	payload["status"] = specialistStopStatus(status, exitCode, runErr)
	payload["exitCode"] = exitCode
	payload["usageRolledUp"] = usageRolledUp
	if runErr != nil {
		payload["error"] = runErr.Error()
	}
	if len(summary.Errors) > 0 {
		payload["errors"] = append([]string(nil), summary.Errors...)
	}
	_, _ = appendSpecialistSessionEvent(store, input.ParentSessionID, sessions.EventSpecialistStop, payload)
}

func (executor Executor) rollUpSpecialistUsage(input specialistAccountingInput, summary StreamResult) bool {
	rolledUp, _ := appendSpecialistUsageRollup(executor.SessionStore, input, summary)
	return rolledUp
}

func (executor Executor) recordBackgroundTaskAccounting(task background.Task, summary StreamResult) {
	if task.Status == background.StatusRunning {
		return
	}
	input := specialistAccountingInput{
		ParentSessionID: strings.TrimSpace(task.ParentID),
		ChildSessionID:  strings.TrimSpace(task.ID),
		SpecialistName:  strings.TrimSpace(task.SpecialistName),
		Description:     strings.TrimSpace(task.Description),
		Mode:            "background",
		Background:      true,
		PID:             task.PID,
	}
	rolledUp := executor.rollUpSpecialistUsage(input, summary)
	executor.recordSpecialistStop(input, summary, string(task.Status), task.ExitCode, nil, rolledUp)
}

func appendSpecialistUsageRollup(store *sessions.Store, input specialistAccountingInput, summary StreamResult) (bool, error) {
	if !summary.Usage.HasUsage() {
		return false, nil
	}
	if strings.TrimSpace(input.ParentSessionID) == "" || strings.TrimSpace(input.ChildSessionID) == "" || !sessions.ValidSessionID(input.ParentSessionID) {
		return false, nil
	}
	store = accountingStore(store)
	if specialistEventExists(store, input.ParentSessionID, sessions.EventUsage, input.ChildSessionID, summary.RunID) {
		return false, nil
	}
	payload := baseSpecialistPayload(input)
	if summary.RunID != "" {
		payload["runId"] = summary.RunID
	}
	payload["promptTokens"] = summary.Usage.PromptTokens
	payload["completionTokens"] = summary.Usage.CompletionTokens
	payload["totalTokens"] = summary.Usage.EffectiveTotalTokens()
	payload["usageEvents"] = summary.Usage.Events
	if _, err := appendSpecialistSessionEvent(store, input.ParentSessionID, sessions.EventUsage, payload); err != nil {
		return false, err
	}
	return true, nil
}

func appendSpecialistSessionEvent(store *sessions.Store, parentSessionID string, eventType sessions.EventType, payload map[string]any) (sessions.Event, error) {
	parentSessionID = strings.TrimSpace(parentSessionID)
	if parentSessionID == "" || !sessions.ValidSessionID(parentSessionID) {
		return sessions.Event{}, nil
	}
	store = accountingStore(store)
	return store.AppendEvent(parentSessionID, sessions.AppendEventInput{Type: eventType, Payload: payload})
}

func specialistEventExists(store *sessions.Store, parentSessionID string, eventType sessions.EventType, childSessionID string, runID string) bool {
	parentSessionID = strings.TrimSpace(parentSessionID)
	childSessionID = strings.TrimSpace(childSessionID)
	runID = strings.TrimSpace(runID)
	if parentSessionID == "" || childSessionID == "" || !sessions.ValidSessionID(parentSessionID) {
		return false
	}
	events, err := store.ReadEvents(parentSessionID)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		if specialistPayloadString(payload, "source") != specialistAccountingSource {
			continue
		}
		if specialistPayloadString(payload, "childSessionId") != childSessionID && specialistPayloadString(payload, "taskId") != childSessionID {
			continue
		}
		if runID == "" || specialistPayloadString(payload, "runId") == runID {
			return true
		}
	}
	return false
}

func baseSpecialistPayload(input specialistAccountingInput) map[string]any {
	childSessionID := strings.TrimSpace(input.ChildSessionID)
	payload := map[string]any{
		"source":         specialistAccountingSource,
		"childSessionId": childSessionID,
		"taskId":         childSessionID,
		"mode":           strings.TrimSpace(input.Mode),
		"background":     input.Background,
	}
	if specialistName := strings.TrimSpace(input.SpecialistName); specialistName != "" {
		payload["specialist"] = specialistName
	}
	if description := strings.TrimSpace(input.Description); description != "" {
		payload["description"] = description
	}
	if toolCallID := strings.TrimSpace(input.ToolCallID); toolCallID != "" {
		payload["toolCallId"] = toolCallID
	}
	if input.PID > 0 {
		payload["pid"] = input.PID
	}
	return payload
}

func specialistStopStatus(status string, exitCode int, runErr error) string {
	status = strings.TrimSpace(status)
	if status != "" {
		return status
	}
	if runErr != nil || exitCode != 0 {
		return "error"
	}
	return "success"
}

func specialistPayloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func accountingStore(store *sessions.Store) *sessions.Store {
	if store != nil {
		return store
	}
	return sessions.NewStore(sessions.StoreOptions{})
}
