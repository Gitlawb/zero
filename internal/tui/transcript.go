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
			return fmt.Sprintf("%d:%s", row.kind, row.permission.ToolCallID)
		}
	}
	return ""
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
