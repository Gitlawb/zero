package tui

import (
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

type rowKind int

const (
	rowWelcome rowKind = iota
	rowUser
	rowAssistant
	rowToolCall
	rowToolResult
	rowPermission
	rowAskUser
	rowSystem
	rowError
)

type transcriptRow struct {
	kind       rowKind
	id         string
	text       string
	tool       string       // tool name, for tool call/result rows
	status     tools.Status // result status, for tool result rows
	detail     string       // raw multi-line output (e.g. a diff to render as a card)
	permission *agent.PermissionEvent
	askUser    *agent.AskUserRequest
}

type transcriptActionKind int

const (
	actionAppendUser transcriptActionKind = iota
	actionAppendAssistant
	actionAppendToolCall
	actionAppendToolResult
	actionAppendSystem
	actionAppendError
	actionClear
)

type transcriptAction struct {
	kind   transcriptActionKind
	text   string
	name   string
	status tools.Status
}

func initialTranscript() []transcriptRow {
	return []transcriptRow{{
		kind: rowWelcome,
		text: "Welcome to Zero. Type /help for commands.",
	}}
}

func reduceTranscript(rows []transcriptRow, action transcriptAction) []transcriptRow {
	switch action.kind {
	case actionClear:
		return initialTranscript()
	case actionAppendUser:
		return appendRow(rows, rowUser, action.text)
	case actionAppendAssistant:
		return appendRow(rows, rowAssistant, action.text)
	case actionAppendToolCall:
		return appendTranscriptRow(rows, transcriptRow{
			kind: rowToolCall,
			text: fmt.Sprintf("tool call: %s", action.name),
			tool: action.name,
		})
	case actionAppendToolResult:
		status := action.status
		if status == "" {
			status = tools.StatusOK
		}
		return appendTranscriptRow(rows, transcriptRow{
			kind:   rowToolResult,
			text:   fmt.Sprintf("tool result: %s %s %s", action.name, status, action.text),
			tool:   action.name,
			status: status,
			detail: action.text,
		})
	case actionAppendSystem:
		return appendRow(rows, rowSystem, action.text)
	case actionAppendError:
		return appendRow(rows, rowError, action.text)
	default:
		return rows
	}
}

func appendRow(rows []transcriptRow, kind rowKind, text string) []transcriptRow {
	return appendTranscriptRow(rows, transcriptRow{kind: kind, text: text})
}

func appendTranscriptRow(rows []transcriptRow, row transcriptRow) []transcriptRow {
	if hasTranscriptRow(rows, row) {
		return rows
	}
	next := append([]transcriptRow{}, rows...)
	next = append(next, row)
	return next
}

func hasTranscriptRow(rows []transcriptRow, row transcriptRow) bool {
	key := transcriptRowKey(row)
	if key == "" {
		return false
	}
	for _, existing := range rows {
		if transcriptRowKey(existing) == key {
			return true
		}
	}
	return false
}

func transcriptRowKey(row transcriptRow) string {
	switch row.kind {
	case rowToolCall, rowToolResult:
		if row.id != "" {
			return fmt.Sprintf("%d:%s", row.kind, row.id)
		}
	case rowPermission:
		if row.permission != nil && row.permission.ToolCallID != "" {
			return fmt.Sprintf("%d:%s:%s", row.kind, row.permission.ToolCallID, row.permission.Action)
		}
	case rowAskUser:
		// Prefer row.id (set to the ToolCallID): it survives rehydration even when
		// row.askUser is nil, so a reloaded ask_user row still dedupes correctly.
		if row.id != "" {
			return fmt.Sprintf("%d:%s", row.kind, row.id)
		}
		if row.askUser != nil && row.askUser.ToolCallID != "" {
			return fmt.Sprintf("%d:%s", row.kind, row.askUser.ToolCallID)
		}
	}
	return ""
}

func askUserTranscriptRow(request agent.AskUserRequest) transcriptRow {
	return transcriptRow{
		kind:    rowAskUser,
		id:      request.ToolCallID,
		text:    askUserRowText(request),
		detail:  askUserDetailText(request),
		askUser: &request,
	}
}

func askUserRowText(request agent.AskUserRequest) string {
	parts := []string{"ask_user:"}
	if header := strings.TrimSpace(request.Header); header != "" {
		parts = append(parts, header)
	} else {
		parts = append(parts, fmt.Sprintf("%d question(s)", len(request.Questions)))
	}
	return strings.Join(parts, " ")
}

func askUserDetailText(request agent.AskUserRequest) string {
	lines := make([]string, 0, len(request.Questions))
	for index, question := range request.Questions {
		line := fmt.Sprintf("%d. %s", index+1, question.Question)
		if len(question.Options) > 0 {
			line += "  (" + strings.Join(question.Options, ", ") + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func askUserSessionPayload(request agent.AskUserRequest) map[string]any {
	questions := make([]map[string]any, 0, len(request.Questions))
	for _, question := range request.Questions {
		entry := map[string]any{"question": question.Question}
		if len(question.Options) > 0 {
			entry["options"] = question.Options
		}
		if question.MultiSelect {
			entry["multiSelect"] = true
		}
		questions = append(questions, entry)
	}
	payload := map[string]any{
		"role":       "ask_user",
		"toolCallId": request.ToolCallID,
		"questions":  questions,
	}
	if header := strings.TrimSpace(request.Header); header != "" {
		payload["header"] = header
	}
	return payload
}

func permissionTranscriptRow(event agent.PermissionEvent) transcriptRow {
	return transcriptRow{
		kind:       rowPermission,
		id:         event.ToolCallID,
		text:       permissionRowText(event),
		tool:       event.ToolName,
		detail:     permissionDetailText(event),
		permission: &event,
	}
}

func permissionEventFromRequest(request agent.PermissionRequest) agent.PermissionEvent {
	return agent.PermissionEvent{
		ToolCallID:     request.ToolCallID,
		ToolName:       request.ToolName,
		Action:         request.Action,
		Permission:     request.Permission,
		PermissionMode: request.PermissionMode,
		Autonomy:       request.Autonomy,
		SideEffect:     request.SideEffect,
		Reason:         request.Reason,
		Risk:           request.Risk,
		Violation:      request.Violation,
		GrantMatched:   request.GrantMatched,
		Grant:          request.Grant,
	}
}

func permissionRowText(event agent.PermissionEvent) string {
	parts := []string{"permission:"}
	if event.ToolName != "" {
		parts = append(parts, event.ToolName)
	}
	if event.Action != "" {
		parts = append(parts, string(event.Action))
	}
	if event.Risk.Level != "" {
		parts = append(parts, "risk:"+string(event.Risk.Level))
	}
	if event.Violation != nil && event.Violation.Code != "" {
		parts = append(parts, "violation:"+string(event.Violation.Code))
	}
	return strings.Join(parts, " ")
}

func permissionDetailText(event agent.PermissionEvent) string {
	parts := []string{}
	if event.Permission != "" {
		parts = append(parts, "permission="+event.Permission)
	}
	if event.PermissionMode != "" {
		parts = append(parts, "mode="+string(event.PermissionMode))
	}
	if event.Autonomy != "" {
		parts = append(parts, "autonomy="+event.Autonomy)
	}
	if event.SideEffect != "" {
		parts = append(parts, "side_effect="+event.SideEffect)
	}
	if event.Risk.Level != "" {
		parts = append(parts, "risk="+string(event.Risk.Level))
	}
	if event.GrantMatched {
		parts = append(parts, "grant=matched")
	}
	if event.Reason != "" {
		parts = append(parts, event.Reason)
	}
	if event.Violation != nil {
		violation := "violation=" + string(event.Violation.Code)
		if event.Violation.Risk.Level != "" {
			violation += " risk=" + string(event.Violation.Risk.Level)
		}
		if event.Violation.Path != "" {
			violation += " path=" + event.Violation.Path
		}
		if event.Violation.Reason != "" {
			violation += " " + event.Violation.Reason
		}
		parts = append(parts, violation)
	}
	return strings.Join(parts, "  ")
}

func truncateTUIOutput(output string, limit int) string {
	output = strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n"))
	output = strings.ReplaceAll(output, "\n", " ")
	if limit <= 0 || len(output) <= limit {
		return output
	}
	return output[:limit] + " [truncated]"
}
