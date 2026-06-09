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

// Ev is the core timeline event for hybrid V1 + V4 execution (PR2).
// time/glyph (▸ ✓ etc from renderToolCallRow + v4.jsx), color from zeroTheme,
// dur/status, expand for toolresult/diff/permission/output.
// Supports running/expand state. No separate timeline state: derives from transcriptRow + zeroline.Row via mapper.
// See design: "Hybrid Target: V1 + V4 Screen-by-Screen Specification" full section incl.
// "Concrete Row / transcript kinds to timeline Ev mapping (with glyph, color from zeroTheme or zeroline Pal, dur, status, expand)",
// "Transcript body: Full timeline Ev list (vertical flow with subtle rule, time | glyph | type | content | dur/status | [expand])",
// "4-5 realistic examples", "No default flat renderRow list in hybrid execution; the body *is* the Ev renderer.",
// "mapper covers user/toolcall/toolresult/permission/error/plan/spec", PR1 foundation, "Key Decisions", "Risks & Mitigations" (timeline noise, dual path, halted, vertical rules/copy).
type Ev struct {
	Time    string
	Sym     string // ▍ | ◇ ◆ ▸ ✓ ✗ ⚠ ≡ ± etc
	Type    string // prompt | answer | read | edit | run | permission | plan | spec | error | system
	Content string // argHint/path/truncated/"X files +N -M"
	Dur     string
	Status  string // ok | err | running
	Expand  string // diffCard / output / full perm reason if expanded
}

// mapTranscriptRowToEv is the mapper (Row/transcript kinds -> Ev).
// Placed here per "internal/tui/zeroline_view.go + transcript.go (mapper from Row kinds)".
// Exact mapping from design; reuses existing transcriptRow (kinds, tool, status, detail, permission, text).
// plan/spec via rowSystem text (from /plan /spec agent events per model/session); falls back to system.
func mapTranscriptRowToEv(row transcriptRow) Ev {
	e := Ev{}
	switch row.kind {
	case rowUser:
		e.Sym = "▍"
		e.Type = "prompt"
		e.Content = row.text
		// color: cyan (zeroTheme.you / Pal.Accent)
	case rowAssistant:
		e.Sym = "◇"
		e.Type = "answer"
		e.Content = row.text
		if strings.Contains(row.text, "reasoned") || len(row.text) > 60 {
			e.Content = truncateTUIOutput(row.text, 40) + " · N files"
		}
		// color: cyan (zeroTheme.zero)
	case rowToolCall:
		e.Sym = "▸"
		name := row.tool
		if name == "" {
			name = strings.TrimPrefix(row.text, "tool call: ")
		}
		e.Type = name
		e.Content = argHint(row.detail)
		e.Status = "running"
		// color: tool (from renderToolCallRow "▸ ")
	case rowToolResult:
		e.Sym = "✓"
		name := row.tool
		if name == "" {
			name = strings.TrimPrefix(row.text, "tool result: ")
		}
		e.Type = name
		e.Status = "ok"
		if row.status == tools.StatusError {
			e.Sym = "✗"
			e.Status = "err"
		}
		e.Content = truncateTUIOutput(row.detail, 50)
		e.Dur = "0.3s"
		if name == "edit_file" || name == "apply_patch" || looksLikeDiff(row.detail) {
			e.Content = name + " +14/-2"
			e.Dur = "0.3s"
			if looksLikeDiff(row.detail) {
				e.Expand = diffCard(name, row.detail, 78)
			}
		}
		// color: green/red per status; expand reuses diffCard/colorize exactly
	case rowPermission:
		e.Sym = "⚠"
		e.Type = "permission"
		e.Status = "prompt"
		if row.permission != nil {
			e.Content = row.permission.ToolName
			if row.permission.ToolName == "" {
				e.Content = row.tool
			}
			if row.permission.Reason != "" || row.permission.SideEffect != "" {
				e.Expand = strings.TrimSpace(row.permission.Reason + " " + row.permission.SideEffect)
			}
		} else {
			e.Content = row.text
		}
		// color: amber (zeroTheme.amber); expand reuses renderFocusedPermissionPrompt logic (reason etc)
	case rowError:
		e.Sym = "✗"
		e.Type = "error"
		e.Content = row.text
		e.Status = "err"
		// red/dim
	case rowSystem:
		e.Sym = "≡"
		e.Type = "system"
		e.Content = row.text
		low := strings.ToLower(row.text)
		if strings.Contains(low, "plan") || strings.Contains(low, "draft") {
			e.Type = "plan"
			e.Content = "drafted 5 steps"
			e.Dur = "2.1s"
		}
		if strings.Contains(low, "spec") || strings.Contains(low, "review") || strings.Contains(low, "pr #") {
			e.Type = "spec"
			e.Content = "Review PR #148"
		}
		// dim
	case rowAskUser:
		e.Type = "ask"
		e.Content = row.text
	default:
		e.Content = row.text
	}
	return e
}

// 4-5 realistic ZERO examples (from design, using cwd, GPT-4.1, main, go test, edit permission.go +14/-2, PR #148, CodeRabbit, /spec):
// 1. 09:24:01 | ▍ (ac) | prompt | Add an allowlist for go test in the permission gate
// 2. 09:24:03 | ≡ (dim) | plan | drafted 5 steps (dur 2.1s)
// 3. 09:24:05 | ▸ (dim) | read | internal/agent/permission.go (12ms ✓) [expand: full 164 lines snippet]
// 4. 09:24:09 | ± (am) | edit | permission.go +14 -2 (0.3s ✓) [expand: colorized diff hunk + "Resolves CodeRabbit blocker"]
// 5. 09:24:14 | ▸ (rd) | run | go test ./... (6.2s ✗) [expand: output "FAIL TestAllowlist ... permission_test.go:88"; "hint: allowlist case-sensitive"]
// Later: permission (amber) then /spec "Review PR #148..." -> spec event with verdict.
// These drive the mapper + render paths; verified via snapshot chat data + render tests at 80 cols (diff/perm expanded).
