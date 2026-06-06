package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
)

const tuiSessionTitleLimit = 80

type pendingSessionEvent struct {
	Type    sessions.EventType
	Payload any
}

func (m model) ensureActiveSession(prompt string) (model, error) {
	if m.activeSession.SessionID != "" {
		return m, nil
	}

	session, err := m.sessionStore.Create(sessions.CreateInput{
		Title:    tuiSessionTitle(prompt),
		Cwd:      m.cwd,
		ModelID:  m.modelName,
		Provider: m.providerName,
	})
	if err != nil {
		return m, err
	}
	m.activeSession = session
	m.sessionEvents = []sessions.Event{}
	return m, nil
}

func (m model) appendSessionEvent(eventType sessions.EventType, payload any) (model, error) {
	if m.activeSession.SessionID == "" {
		return m, nil
	}

	event, err := m.sessionStore.AppendEvent(m.activeSession.SessionID, sessions.AppendEventInput{
		Type:    eventType,
		Payload: payload,
	})
	if err != nil {
		return m, err
	}
	m.activeSession.UpdatedAt = event.CreatedAt
	m.activeSession.EventCount = event.Sequence
	m.activeSession.LastEventType = event.Type
	m.sessionEvents = append(m.sessionEvents, event)
	return m, nil
}

func (m model) appendSessionEvents(events []pendingSessionEvent) (model, []transcriptRow) {
	rows := []transcriptRow{}
	for _, event := range events {
		next, err := m.appendSessionEvent(event.Type, event.Payload)
		if err != nil {
			rows = append(rows, transcriptRow{kind: rowError, text: "session record error: " + err.Error()})
			continue
		}
		m = next
	}
	return m, rows
}

func tuiSessionTitle(prompt string) string {
	title := strings.Join(strings.Fields(prompt), " ")
	if len(title) > tuiSessionTitleLimit {
		title = title[:tuiSessionTitleLimit]
	}
	if title == "" {
		return "Zero TUI session"
	}
	return title
}

func (m model) handleResumeCommand(args string) (model, string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return m, m.resumeText("")
	}

	session, err := m.resolveResumeSession(args)
	if err != nil {
		return m, "Sessions\n" + err.Error()
	}
	events, err := m.sessionStore.ReadEvents(session.SessionID)
	if err != nil {
		return m, "Sessions\nerror: " + err.Error()
	}

	m.activeSession = *session
	m.sessionEvents = append([]sessions.Event{}, events...)
	if m.providerName == "" {
		m.providerName = session.Provider
	}
	if m.modelName == "" {
		m.modelName = session.ModelID
	}

	rows := initialTranscript()
	rows = appendRow(rows, rowSystem, formatResumeSummary(*session, len(events)))
	for _, row := range transcriptRowsFromSessionEvents(events) {
		rows = appendTranscriptRow(rows, row)
	}
	m.transcript = rows
	return m, ""
}

func (m model) sessionPrompt(prompt string) string {
	if m.activeSession.SessionID == "" || len(m.sessionEvents) == 0 {
		return prompt
	}
	return sessions.FormatExecPrompt(prompt, sessions.PreparedExec{
		Mode:          sessions.ModeResume,
		Session:       m.activeSession,
		ContextEvents: append([]sessions.Event{}, m.sessionEvents...),
	})
}

func (m model) resolveResumeSession(args string) (*sessions.Metadata, error) {
	if strings.EqualFold(args, "latest") {
		latest, err := m.sessionStore.Latest()
		if err != nil {
			return nil, err
		}
		if latest == nil {
			return nil, errors.New("no zero sessions available to resume")
		}
		return latest, nil
	}

	session, err := m.sessionStore.Get(args)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("zero session not found: %s", args)
	}
	return session, nil
}

func formatResumeSummary(session sessions.Metadata, eventCount int) string {
	return strings.Join([]string{
		"Resumed Zero session",
		"id: " + session.SessionID,
		"title: " + displayValue(session.Title, "untitled"),
		"model: " + displayValue(session.ModelID, "none"),
		"provider: " + displayValue(session.Provider, "none"),
		fmt.Sprintf("events: %d", eventCount),
	}, "\n")
}

func transcriptRowsFromSessionEvents(events []sessions.Event) []transcriptRow {
	rows := []transcriptRow{}
	for _, event := range events {
		payload := sessionPayload(event)
		switch event.Type {
		case sessions.EventMessage:
			content := payloadString(payload, "content")
			if content == "" {
				continue
			}
			switch strings.ToLower(payloadString(payload, "role")) {
			case "user":
				rows = append(rows, transcriptRow{kind: rowUser, text: content})
			case "assistant":
				rows = append(rows, transcriptRow{kind: rowAssistant, text: content})
			default:
				rows = append(rows, transcriptRow{kind: rowSystem, text: content})
			}
		case sessions.EventToolCall:
			name := payloadString(payload, "name")
			if name == "" {
				name = "unknown"
			}
			rows = append(rows, transcriptRow{
				kind:   rowToolCall,
				id:     payloadString(payload, "id"),
				text:   "tool call: " + name,
				tool:   name,
				detail: argHint(payloadString(payload, "arguments")),
			})
		case sessions.EventPermission:
			rows = append(rows, permissionTranscriptRow(permissionEventFromPayload(payload)))
		case sessions.EventToolResult:
			name := payloadString(payload, "name")
			if name == "" {
				name = "unknown"
			}
			status := tools.Status(payloadString(payload, "status"))
			if status == "" {
				status = tools.StatusOK
			}
			output := payloadString(payload, "output")
			rows = append(rows, transcriptRow{
				kind:   rowToolResult,
				id:     firstNonEmptyString(payloadString(payload, "toolCallId"), payloadString(payload, "id")),
				text:   fmt.Sprintf("tool result: %s %s %s", name, status, truncateTUIOutput(output, tuiToolOutputLimit)),
				tool:   name,
				status: status,
				detail: output,
			})
		case sessions.EventError:
			if message := payloadString(payload, "message"); message != "" {
				rows = append(rows, transcriptRow{kind: rowError, text: message})
			}
		case sessions.EventSessionFork:
			parentID := payloadString(payload, "parentSessionId")
			if parentID != "" {
				rows = append(rows, transcriptRow{kind: rowSystem, text: "forked from session: " + parentID})
			}
		}
	}
	return rows
}

func sessionPayload(event sessions.Event) map[string]any {
	payload := map[string]any{}
	if len(event.Payload) == 0 {
		return payload
	}
	_ = json.Unmarshal(event.Payload, &payload)
	return payload
}

func permissionEventFromPayload(payload map[string]any) agent.PermissionEvent {
	name := payloadString(payload, "name")
	if name == "" {
		name = payloadString(payload, "toolName")
	}
	event := agent.PermissionEvent{
		ToolCallID:        firstNonEmptyString(payloadString(payload, "toolCallId"), payloadString(payload, "id")),
		ToolName:          name,
		Action:            agent.PermissionAction(payloadString(payload, "action")),
		Permission:        payloadString(payload, "permission"),
		PermissionGranted: payloadBool(payload, "permissionGranted"),
		PermissionMode:    agent.PermissionMode(payloadString(payload, "permissionMode")),
		Autonomy:          payloadString(payload, "autonomy"),
		SideEffect:        payloadString(payload, "sideEffect"),
		Reason:            payloadString(payload, "reason"),
		GrantMatched:      payloadBool(payload, "grantMatched"),
	}
	if risk, ok := payloadMap(payload, "risk"); ok {
		event.Risk = sandbox.Risk{
			Level:  sandbox.RiskLevel(payloadString(risk, "level")),
			Reason: payloadString(risk, "reason"),
		}
	}
	if violation, ok := payloadMap(payload, "violation"); ok {
		event.Violation = &sandbox.Violation{
			Code:        sandbox.ViolationCode(payloadString(violation, "code")),
			ToolName:    payloadString(violation, "toolName"),
			Action:      sandbox.Action(payloadString(violation, "action")),
			Risk:        event.Risk,
			Path:        payloadString(violation, "path"),
			Reason:      payloadString(violation, "reason"),
			Recoverable: payloadBool(violation, "recoverable"),
		}
		if nestedRisk, ok := payloadMap(violation, "risk"); ok {
			event.Violation.Risk = sandbox.Risk{
				Level:  sandbox.RiskLevel(payloadString(nestedRisk, "level")),
				Reason: payloadString(nestedRisk, "reason"),
			}
		}
	}
	return event
}

func payloadString(payload map[string]any, key string) string {
	value := payload[key]
	switch typed := value.(type) {
	case string:
		return typed
	case float64, bool:
		return fmt.Sprint(typed)
	case nil:
		return ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func payloadBool(payload map[string]any, key string) bool {
	value := payload[key]
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func payloadMap(payload map[string]any, key string) (map[string]any, bool) {
	value, ok := payload[key].(map[string]any)
	return value, ok
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
