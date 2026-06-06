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
	kind   rowKind
	text   string
	tool   string       // tool name, for tool call/result rows
	status tools.Status // result status, for tool result rows
	detail string       // raw multi-line output (e.g. a diff to render as a card)
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
	next := append([]transcriptRow{}, rows...)
	next = append(next, row)
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
