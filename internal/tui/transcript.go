package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

type rowKind int

const (
	rowWelcome rowKind = iota
	rowUser
	rowAssistant
	rowReasoning
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
	arg        string       // secondary argument hint (pattern/command), for tool call rows
	runID      int          // owning run, for tool call rows (0 = rehydrated/unknown)
	permission *agent.PermissionEvent
	askUser    *agent.AskUserRequest
	expanded   bool // collapsible transcript rows, e.g. provider thoughts

	// Final-answer metadata, set at append time. Interim assistant text streams
	// through model.streamingText and never lands in the transcript, so a
	// rowAssistant marked final IS the turn's answer — the renderer must not
	// re-parse text to tell the two apart. turnTools/turnElapsed feed the done
	// line; zero values mean "unknown" and the segment is omitted.
	final       bool
	turnTools   int
	turnElapsed time.Duration
}

type transcriptActionKind int

const (
	actionAppendUser transcriptActionKind = iota
	actionAppendAssistant
	actionAppendSystem
	actionAppendError
	actionClear
)

type transcriptAction struct {
	kind transcriptActionKind
	text string
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
	// In-place append is safe: every transcript mutation happens on the Bubble
	// Tea update goroutine (agent goroutines only Send messages), so no other
	// model copy can append into the same backing array concurrently. The old
	// full-slice copy made appends O(n) and rehydration O(n²).
	return append(rows, row)
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

// transcriptRowKey is run-scoped (runID baked into every key): some providers
// synthesize ToolCallIDs that repeat across runs (e.g. Gemini's gemini_tool_N),
// and a bare-id key silently dropped later runs' tool rows as "duplicates".
// Repeats WITHIN one run are disambiguated upstream by the per-run ordinal
// suffix the runner appends to row ids (see effectiveToolRowID).
func transcriptRowKey(row transcriptRow) string {
	switch row.kind {
	case rowToolCall, rowToolResult:
		if row.id != "" {
			return fmt.Sprintf("%d:%d:%s", row.kind, row.runID, row.id)
		}
	case rowReasoning:
		if row.id != "" {
			return fmt.Sprintf("%d:%d:%s", row.kind, row.runID, row.id)
		}
	case rowPermission:
		if row.permission != nil && row.permission.ToolCallID != "" {
			return fmt.Sprintf("%d:%d:%s:%s", row.kind, row.runID, row.permission.ToolCallID, row.permission.Action)
		}
	case rowAskUser:
		// Prefer row.id (set to the ToolCallID): it survives rehydration even when
		// row.askUser is nil, so a reloaded ask_user row still dedupes correctly.
		if row.id != "" {
			return fmt.Sprintf("%d:%d:%s", row.kind, row.runID, row.id)
		}
		if row.askUser != nil && row.askUser.ToolCallID != "" {
			return fmt.Sprintf("%d:%d:%s", row.kind, row.runID, row.askUser.ToolCallID)
		}
	}
	return ""
}

func reasoningTranscriptRow(id string, runID int, text string) (transcriptRow, bool) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return transcriptRow{}, false
	}
	return transcriptRow{kind: rowReasoning, id: id, runID: runID, text: text}, true
}

func previousVisibleTranscriptKind(rows []transcriptRow, before int, rc rowContext) (rowKind, bool) {
	if before > len(rows) {
		before = len(rows)
	}
	for index := before - 1; index >= 0; index-- {
		row := rows[index]
		if row.kind == rowWelcome || rc.skip(row) {
			continue
		}
		return row.kind, true
	}
	return rowWelcome, false
}

// effectiveToolRowID disambiguates a provider tool-call id that repeats within
// a run: the first occurrence keeps the raw id (the common case), repeats get
// an ordinal suffix. Session payloads are unaffected — they persist the
// provider's original ids.
func effectiveToolRowID(id string, seq int) string {
	if id == "" || seq <= 1 {
		return id
	}
	return fmt.Sprintf("%s#%d", id, seq)
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
		Scope:          request.Scope,
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
	// Cut on a rune boundary: a bare byte slice can split a multi-byte UTF-8
	// sequence and emit invalid UTF-8 into the transcript and session log.
	return cutRunes(output, limit) + " [truncated]"
}

// cutRunes truncates text to at most limit bytes without splitting a UTF-8
// rune (the cut lands on the last rune boundary at or before limit).
func cutRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
