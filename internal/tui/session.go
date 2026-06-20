package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

// appendSessionEventsTo persists events into a specific (non-active) session —
// the late flush of a run cancelled before a /resume switched sessions. The
// active session's in-memory metadata is deliberately untouched.
func (m model) appendSessionEventsTo(sessionID string, events []pendingSessionEvent) []transcriptRow {
	rows := []transcriptRow{}
	if m.sessionStore == nil || sessionID == "" {
		return rows
	}
	for _, event := range events {
		if _, err := m.sessionStore.AppendEvent(sessionID, sessions.AppendEventInput{
			Type:    event.Type,
			Payload: event.Payload,
		}); err != nil {
			rows = append(rows, transcriptRow{kind: rowError, text: "session record error: " + err.Error()})
		}
	}
	return rows
}

// flushableSessionEvents selects the events worth persisting from a run that was
// cancelled mid-flight. The cancel path already records a single "Run cancelled."
// error, so the goroutine's trailing EventError (the ctx-cancellation error) is
// dropped to avoid a duplicate; everything else it accumulated before the cancel
// — tool calls/results, permission events, usage, and the EventSessionCheckpoint
// blobs that /rewind depends on — is kept.
func flushableSessionEvents(events []pendingSessionEvent) []pendingSessionEvent {
	flushable := make([]pendingSessionEvent, 0, len(events))
	for _, event := range events {
		if event.Type == sessions.EventError {
			continue
		}
		flushable = append(flushable, event)
	}
	return flushable
}

func tuiSessionTitle(prompt string) string {
	// cutRunes keeps the cut on a rune boundary — a bare byte slice could split
	// a multi-byte rune and persist invalid UTF-8 into the session metadata.
	title := cutRunes(strings.Join(strings.Fields(prompt), " "), tuiSessionTitleLimit)
	if title == "" {
		return "Zero TUI session"
	}
	return title
}

func (m model) handleResumeCommand(args string) (model, string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return m, m.resumeText()
	}

	session, err := m.resolveResumeSession(args)
	if err != nil {
		return m, "Sessions\n" + err.Error()
	}
	events, err := m.resumeEvents(session.SessionID)
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
	rows = appendRow(rows, rowSystem, m.formatResumeSummary(*session, len(events)))
	rows = appendTranscriptRowsDedup(rows, transcriptRowsFromSessionEvents(events))
	m.transcript = rows
	// Every rehydrated row is settled by construction, so resetting the flush
	// frontier sends the whole resumed history to native scrollback in one
	// batch — scrollable, selectable, and O(1) for every later frame.
	m.resetFlushFrontier("· resumed ·")
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
		// Latest *resumable* conversation, so "latest" never lands on a child or
		// spec sub-run. An explicit `/resume <id>` below still resolves any session.
		latest, err := m.sessionStore.LatestResumable()
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

// resumeEvents reads a session's events for resume, preferring the rehydrated
// (compaction-aware) view so a resumed session honors a prior /compact — matching
// the CLI's `zero exec --resume` (readExecContextEvents) and the in-TUI /compact
// reload. Falls back to the raw log if rehydration fails.
func (m model) resumeEvents(sessionID string) ([]sessions.Event, error) {
	events, err := m.sessionStore.ReadRehydratedEvents(sessionID)
	if err == nil {
		return events, nil
	}
	raw, rawErr := m.sessionStore.ReadEvents(sessionID)
	if rawErr != nil {
		// Surface the raw-read failure (the actual fallback error), not the earlier
		// rehydration error, so the caller sees why the fallback itself failed.
		return nil, rawErr
	}
	return raw, nil
}

// formatResumeSummary reports what the resumed conversation will actually continue
// with (the active model/provider), noting the session's recorded model/provider
// when it differs — resume keeps the current model rather than switching.
func (m model) formatResumeSummary(session sessions.Metadata, eventCount int) string {
	modelLine := "model: " + displayValue(m.modelName, "none")
	if recorded := strings.TrimSpace(session.ModelID); recorded != "" && !strings.EqualFold(recorded, m.modelName) {
		modelLine += "  (recorded: " + recorded + ")"
	}
	providerLine := "provider: " + displayValue(m.providerName, "none")
	if recorded := strings.TrimSpace(session.Provider); recorded != "" && !strings.EqualFold(recorded, m.providerName) {
		providerLine += "  (recorded: " + recorded + ")"
	}
	return renderCommandOutput(commandOutput{
		Title:  "Resumed Zero session",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "Session",
			Lines: []string{
				"id: " + session.SessionID,
				"title: " + displayValue(session.Title, "untitled"),
				modelLine,
				providerLine,
				fmt.Sprintf("events: %d", eventCount),
			},
		}},
	})
}

// sessionWhen formats a session's RFC3339 timestamp for the picker: a precise
// clock time (with seconds) for today so same-minute sessions stay distinct, the
// month/day and time earlier this year, else the date. Empty on a parse error.
func sessionWhen(timestamp string, now time.Time) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(timestamp))
	if err != nil {
		return ""
	}
	parsed, now = parsed.Local(), now.Local()
	switch {
	case parsed.Year() == now.Year() && parsed.YearDay() == now.YearDay():
		return parsed.Format("15:04:05")
	case parsed.Year() == now.Year():
		return parsed.Format("Jan _2 15:04")
	default:
		return parsed.Format("2006-01-02")
	}
}

// newSessionPicker builds the interactive /resume picker (mirrors /model & /provider):
// one row per resumable session — title (Label) + id and relative age (Meta). Returns
// nil when there are no resumable sessions so the caller falls back to the text path.
func (m model) newSessionPicker() *commandPicker {
	if m.sessionStore == nil {
		return nil
	}
	metas, err := m.sessionStore.ListResumable()
	if err != nil || len(metas) == 0 {
		return nil
	}
	now := m.now()
	items := make([]pickerItem, 0, len(metas))
	for _, meta := range metas {
		// Skip empty/failed runs (no assistant output, no tool calls) — e.g. the
		// same prompt retried while the model wasn't responding. They have nothing
		// to resume and otherwise flood the list with identical rows. Still on disk.
		if !m.sessionHasResumableContent(meta.SessionID) {
			continue
		}
		// Lead with the timestamp so same-titled sessions (e.g. the same first
		// prompt run several times) are visually distinct; the id (right, faint)
		// stays for reference and is what selection actually resolves.
		label := displayValue(meta.Title, "untitled")
		if when := sessionWhen(meta.UpdatedAt, now); when != "" {
			label = when + "  " + label
		}
		items = append(items, pickerItem{
			Label: label,
			Value: meta.SessionID,
			Meta:  meta.SessionID,
		})
	}
	if len(items) == 0 {
		return nil // every resumable session was an empty/failed run
	}
	return &commandPicker{
		kind:     pickerSession,
		title:    "Resume a session",
		items:    items,
		allItems: append([]pickerItem{}, items...),
		selected: 0,
	}
}

// sessionHasResumableContent reports whether a session has anything worth
// resuming: a tool call/result, or a non-user message with real content (not the
// no-output guardrail stop). Empty/failed runs return false and are hidden from
// the picker (they stay on disk). Errors fail open (the session is kept).
func (m model) sessionHasResumableContent(sessionID string) bool {
	events, err := m.sessionStore.ReadEvents(sessionID)
	if err != nil {
		return true
	}
	return eventsHaveResumableContent(events)
}

// eventsHaveResumableContent reports whether already-loaded events contain
// anything worth resuming: a tool call/result, or a non-user message with real
// content (not the no-output guardrail stop). It is the pure core of
// sessionHasResumableContent so callers that already hold the events (e.g. the
// /retitle scan) don't re-read them.
func eventsHaveResumableContent(events []sessions.Event) bool {
	for _, event := range events {
		switch event.Type {
		case sessions.EventToolCall, sessions.EventToolResult:
			return true
		case sessions.EventMessage:
			payload := sessionPayload(event)
			if strings.EqualFold(payloadString(payload, "role"), "user") {
				continue
			}
			content := strings.TrimSpace(payloadString(payload, "content"))
			if content != "" && !agent.IsNoProgressStop(content) {
				return true
			}
		}
	}
	return false
}

// openSessionPicker opens the /resume picker; ok is false when there is nothing to
// resume (the caller then falls back to the text list / "none" message).
func (m model) openSessionPicker() (model, bool) {
	picker := m.newSessionPicker()
	if picker == nil {
		return m, false
	}
	m.picker = picker
	return m, true
}

func transcriptRowsFromSessionEvents(events []sessions.Event) []transcriptRow {
	rows := []transcriptRow{}
	// Rehydrated rows all carry runID 0, so repeated provider tool-call ids
	// (e.g. Gemini's per-turn gemini_tool_N) get the same per-occurrence
	// disambiguation the live runner applies — without it, dedup would drop
	// every tool card after the first occurrence of an id.
	callSeq := map[string]int{}
	// Pre-pass: collect the tool-call ids of Task delegations that actually started
	// a specialist (each renders as a card below). Only those Task tool-call/result
	// rows are redundant and skipped; a Task that failed before a specialist started
	// has no card, so its rows are kept — otherwise the failed delegation vanishes
	// on resume (M10).
	specialistToolCalls := map[string]bool{}
	for _, event := range events {
		if event.Type == sessions.EventSpecialistStart {
			if id := payloadString(sessionPayload(event), "toolCallId"); id != "" {
				specialistToolCalls[id] = true
			}
		}
	}
	for _, event := range events {
		payload := sessionPayload(event)
		switch event.Type {
		case sessions.EventMessage:
			role := strings.ToLower(payloadString(payload, "role"))
			switch role {
			case "ask_user":
				rows = append(rows, askUserTranscriptRow(askUserRequestFromPayload(payload)))
				continue
			case "ask_user_answers":
				if text := askUserAnswersText(payload); text != "" {
					rows = append(rows, transcriptRow{kind: rowSystem, text: text})
				}
				continue
			}
			content := payloadString(payload, "content")
			if content == "" {
				continue
			}
			switch role {
			case "user":
				rows = append(rows, transcriptRow{kind: rowUser, text: content})
			case "assistant":
				// A persisted assistant message was a turn's final answer. Tool/timing
				// counters were not recorded; the completion line omits those segments.
				rows = append(rows, transcriptRow{kind: rowAssistant, text: content, final: true})
			default:
				rows = append(rows, transcriptRow{kind: rowSystem, text: content})
			}
		case sessions.EventToolCall:
			name := payloadString(payload, "name")
			if name == "" {
				name = "unknown"
			}
			id := payloadString(payload, "id")
			if name == "Task" && specialistToolCalls[id] {
				// A specialist card renders this delegation; skip the redundant
				// "tool call: Task" row. A Task with no specialist (it failed before
				// one started) keeps its row so the failure stays visible (M10).
				continue
			}
			callSeq[id]++
			rows = append(rows, transcriptRow{
				kind:   rowToolCall,
				id:     effectiveToolRowID(id, callSeq[id]),
				text:   "tool call: " + name,
				tool:   name,
				detail: argHint(payloadString(payload, "arguments")),
				arg:    argHintSecondary(payloadString(payload, "arguments")),
			})
		case sessions.EventPermission, sessions.EventPermissionRequest, sessions.EventPermissionDecision:
			rows = append(rows, permissionTranscriptRow(permissionEventFromPayload(payload)))
		case sessions.EventToolResult:
			name := payloadString(payload, "name")
			if name == "" {
				name = "unknown"
			}
			id := firstNonEmptyString(payloadString(payload, "toolCallId"), payloadString(payload, "id"))
			if name == "Task" && specialistToolCalls[id] {
				// The specialist card carries this Task's result; skip the redundant
				// "tool result: Task" row. A Task with no specialist keeps its result
				// row so a failed delegation's error stays visible (M10).
				continue
			}
			status := tools.Status(payloadString(payload, "status"))
			if status == "" {
				status = tools.StatusOK
			}
			output := payloadString(payload, "output")
			rows = append(rows, transcriptRow{
				kind:   rowToolResult,
				id:     effectiveToolRowID(id, callSeq[id]),
				text:   fmt.Sprintf("tool result: %s %s %s", name, status, truncateTUIOutput(output, tuiToolOutputLimit)),
				tool:   name,
				status: status,
				detail: output,
			})
		case sessions.EventError:
			if message := payloadString(payload, "message"); message != "" {
				rows = append(rows, transcriptRow{kind: rowError, text: message})
			}
		case sessions.EventCompaction:
			if summary := payloadString(payload, "summary"); summary != "" {
				rows = append(rows, transcriptRow{kind: rowSystem, text: summary})
			}
		case sessions.EventSessionFork:
			parentID := payloadString(payload, "parentSessionId")
			if parentID != "" {
				rows = append(rows, transcriptRow{kind: rowSystem, text: "forked from session: " + parentID})
			}
		case sessions.EventSpecialistStart:
			info := specialistInfoFromPayload(payload)
			if info != nil {
				rows = append(rows, transcriptRow{kind: rowSpecialist, specialistInfo: info})
			}
		case sessions.EventSpecialistStop:
			info := specialistInfoFromPayload(payload)
			if info != nil {
				// Reconcile: update the existing Start row with the same
				// childSessionID instead of appending a duplicate. On resume
				// this prevents two cards per specialist (running + completed).
				found := false
				for i := range rows {
					if rows[i].kind == rowSpecialist && rows[i].specialistInfo != nil &&
						rows[i].specialistInfo.childSessionID == info.childSessionID {
						rows[i].specialistInfo = info
						found = true
						break
					}
				}
				if !found {
					rows = append(rows, transcriptRow{kind: rowSpecialist, specialistInfo: info})
				}
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
		DecisionAction:    agent.PermissionDecisionAction(payloadString(payload, "decisionAction")),
		Permission:        payloadString(payload, "permission"),
		PermissionGranted: payloadBool(payload, "permissionGranted"),
		PermissionMode:    agent.PermissionMode(payloadString(payload, "permissionMode")),
		Autonomy:          payloadString(payload, "autonomy"),
		SideEffect:        payloadString(payload, "sideEffect"),
		Reason:            payloadString(payload, "reason"),
		Scope:             payloadString(payload, "scope"),
		DecisionReason:    payloadString(payload, "decisionReason"),
		GrantMatched:      payloadBool(payload, "grantMatched"),
	}
	if risk, ok := payloadMap(payload, "risk"); ok {
		event.Risk = sandbox.Risk{
			Level:  sandbox.RiskLevel(payloadString(risk, "level")),
			Reason: payloadString(risk, "reason"),
		}
	}
	if block, ok := payloadMap(payload, "block"); ok {
		event.Block = &sandbox.Block{
			Code:        sandbox.BlockCode(payloadString(block, "code")),
			ToolName:    payloadString(block, "toolName"),
			Action:      sandbox.Action(payloadString(block, "action")),
			Risk:        event.Risk,
			Path:        payloadString(block, "path"),
			Reason:      payloadString(block, "reason"),
			Recoverable: payloadBool(block, "recoverable"),
		}
		if nestedRisk, ok := payloadMap(block, "risk"); ok {
			event.Block.Risk = sandbox.Risk{
				Level:  sandbox.RiskLevel(payloadString(nestedRisk, "level")),
				Reason: payloadString(nestedRisk, "reason"),
			}
		}
	}
	return event
}

// askUserRequestFromPayload rebuilds the request persisted by
// askUserSessionPayload, so ask_user exchanges survive /resume instead of
// silently vanishing from rehydrated history.
func askUserRequestFromPayload(payload map[string]any) agent.AskUserRequest {
	request := agent.AskUserRequest{
		ToolCallID: payloadString(payload, "toolCallId"),
		Header:     payloadString(payload, "header"),
	}
	raw, ok := payload["questions"].([]any)
	if !ok {
		return request
	}
	for _, entry := range raw {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		question := agent.AskUserQuestion{
			Question:    payloadString(fields, "question"),
			MultiSelect: payloadBool(fields, "multiSelect"),
		}
		if options, ok := fields["options"].([]any); ok {
			for _, option := range options {
				if text, ok := option.(string); ok {
					question.Options = append(question.Options, text)
				}
			}
		}
		request.Questions = append(request.Questions, question)
	}
	return request
}

// askUserAnswersText renders persisted ask_user answers for rehydration.
func askUserAnswersText(payload map[string]any) string {
	raw, ok := payload["answers"].([]any)
	if !ok {
		return ""
	}
	lines := make([]string, 0, len(raw))
	for index, entry := range raw {
		text, _ := entry.(string)
		if strings.TrimSpace(text) == "" {
			text = "(skipped)"
		}
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, text))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Answers\n" + strings.Join(lines, "\n")
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

// specialistInfoFromPayload builds a specialistInfo from a specialist_start or
// specialist_stop session event payload. Returns nil if the payload lacks a
// childSessionId (the minimum required field).
func specialistInfoFromPayload(payload map[string]any) *specialistInfo {
	childSessionID := payloadString(payload, "childSessionId")
	if childSessionID == "" {
		return nil
	}
	info := &specialistInfo{
		name:           payloadString(payload, "specialist"),
		description:    payloadString(payload, "description"),
		childSessionID: childSessionID,
	}
	statusStr := payloadString(payload, "status")
	switch statusStr {
	case "running":
		info.status = specialistRunning
	case "success":
		info.status = specialistCompleted
	case "completed":
		info.status = specialistCompleted
	default:
		info.status = parseSpecialistStatus(statusStr)
	}
	if errMsg := payloadString(payload, "error"); errMsg != "" {
		info.errorMsg = errMsg
	}
	return info
}
