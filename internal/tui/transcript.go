package tui

import (
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/tools"
)

type rowKind int

const (
	rowWelcome rowKind = iota
	rowUser
	rowAssistant
	rowToolCall
	rowToolResult
	rowSystem
	rowError
)

type transcriptRow struct {
	kind rowKind
	text string
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
		return appendRow(rows, rowToolCall, fmt.Sprintf("tool call: %s", action.name))
	case actionAppendToolResult:
		status := action.status
		if status == "" {
			status = tools.StatusOK
		}
		return appendRow(rows, rowToolResult, fmt.Sprintf("tool result: %s %s %s", action.name, status, action.text))
	case actionAppendSystem:
		return appendRow(rows, rowSystem, action.text)
	case actionAppendError:
		return appendRow(rows, rowError, action.text)
	default:
		return rows
	}
}

func appendRow(rows []transcriptRow, kind rowKind, text string) []transcriptRow {
	next := append([]transcriptRow{}, rows...)
	next = append(next, transcriptRow{kind: kind, text: text})
	return next
}

func truncateTUIOutput(output string, limit int) string {
	output = strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n"))
	output = strings.ReplaceAll(output, "\n", " ")
	if limit <= 0 || len(output) <= limit {
		return output
	}
	return output[:limit] + " [truncated]"
}
