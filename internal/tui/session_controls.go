package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/usage"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

var responseStyles = []string{"balanced", "concise", "explanatory", "review"}

func (m model) handleEffortCommand(args string) (model, string) {
	args = strings.TrimSpace(strings.ToLower(args))
	if args == "" || args == "list" {
		return m, m.effortText()
	}
	if args == "auto" {
		m.reasoningEffort = ""
		return m, strings.Join([]string{
			"Effort",
			"active effort: auto",
			"Reasoning effort selection will follow the active model/provider defaults.",
		}, "\n")
	}

	requested := modelregistry.ReasoningEffort(args)
	if !modelregistry.ValidReasoningEffort(requested) {
		return m, "Effort\nUnknown reasoning effort: " + args
	}
	efforts := m.availableReasoningEfforts()
	if len(efforts) == 0 {
		return m, "Effort\nActive model does not expose reasoning effort controls."
	}
	if !reasoningEffortAllowed(efforts, requested) {
		return m, fmt.Sprintf("Effort\nReasoning effort %q is not supported by %s.", requested, displayValue(m.modelName, "the active model"))
	}

	m.reasoningEffort = requested
	return m, strings.Join([]string{
		"Effort",
		"active effort: " + string(requested),
		"model: " + displayValue(m.modelName, "none"),
		"Reasoning effort preference is stored for this TUI session.",
	}, "\n")
}

func (m model) effortText() string {
	lines := []string{
		"active effort: " + m.effortDisplay(),
		"model: " + displayValue(m.modelName, "none"),
	}
	efforts := m.availableReasoningEfforts()
	if len(efforts) == 0 {
		lines = append(lines, "available: none for active model")
		return renderCommandOutput(commandOutput{
			Title:    "Effort",
			Status:   commandStatusWarning,
			Sections: []commandSection{{Title: "State", Lines: lines}},
		})
	}
	lines = append(lines, "available: "+joinReasoningEfforts(efforts))
	return renderCommandOutput(commandOutput{
		Title:    "Effort",
		Status:   commandStatusOK,
		Sections: []commandSection{{Title: "State", Lines: lines}},
		Hints:    []string{"use /effort <value> or /effort auto"},
	})
}

func (m model) availableReasoningEfforts() []modelregistry.ReasoningEffort {
	if strings.TrimSpace(m.modelName) == "" {
		return nil
	}
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return nil
	}
	return registry.ReasoningEfforts(m.modelName)
}

func (m model) effortDisplay() string {
	if m.reasoningEffort == "" {
		return "auto"
	}
	return string(m.reasoningEffort)
}

func reasoningEffortAllowed(efforts []modelregistry.ReasoningEffort, want modelregistry.ReasoningEffort) bool {
	for _, effort := range efforts {
		if effort == want {
			return true
		}
	}
	return false
}

func joinReasoningEfforts(efforts []modelregistry.ReasoningEffort) string {
	values := make([]string, 0, len(efforts))
	for _, effort := range efforts {
		values = append(values, string(effort))
	}
	return strings.Join(values, ", ")
}

func (m model) handleStyleCommand(args string) (model, string) {
	args = strings.TrimSpace(strings.ToLower(args))
	if args == "" || args == "list" {
		return m, m.styleText()
	}
	if !responseStyleAllowed(args) {
		return m, "Style\nUnknown response style: " + args
	}
	m.responseStyle = args
	return m, strings.Join([]string{
		"Style",
		"active style: " + m.responseStyle,
		"Style preference is stored for this TUI session.",
	}, "\n")
}

func (m model) styleText() string {
	return renderCommandOutput(commandOutput{
		Title:  "Style",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "State",
			Lines: []string{
				"active style: " + m.responseStyle,
				"available: " + strings.Join(responseStyles, ", "),
			},
		}},
		Hints: []string{"use /style <value> to update this TUI session"},
	})
}

func defaultedResponseStyle(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if responseStyleAllowed(value) {
		return value
	}
	return defaultResponseStyle
}

func responseStyleAllowed(value string) bool {
	for _, style := range responseStyles {
		if value == style {
			return true
		}
	}
	return false
}

func (m model) handleCompactCommand(args string) (model, string) {
	args = strings.TrimSpace(strings.ToLower(args))
	if args == "status" {
		return m, m.compactText(false)
	}
	if args != "" {
		return m, "Compact\nusage: /compact [status]"
	}
	m.compactRequests++
	return m, m.compactText(true)
}

// handleRewindCommand restores workspace files to a checkpoint and truncates the
// session log. "/rewind" or "/rewind latest" undoes the most recent checkpoint;
// "/rewind <n>" rewinds to a specific event sequence.
func (m model) handleRewindCommand(args string) (model, string) {
	if m.sessionStore == nil || m.activeSession.SessionID == "" {
		return m, "Rewind\nno active session to rewind."
	}
	if m.pending {
		return m, "Rewind\ncannot rewind while a run is in progress."
	}
	arg := strings.TrimSpace(strings.ToLower(args))
	target, err := m.resolveRewindTarget(arg)
	if err != nil {
		return m, "Rewind\n" + err.Error()
	}
	report, err := m.sessionStore.ApplyRewind(m.activeSession.SessionID, m.cwd, target)
	if err != nil {
		return m, "Rewind\n" + err.Error()
	}

	// ApplyRewind truncated the persisted event log, restored files, and appended a
	// rewind marker — so reload the in-memory session state. Otherwise the dropped
	// events would still (a) be re-sent to the agent as ContextEvents on the next
	// prompt (sessionPrompt sends m.sessionEvents) and (b) linger in the transcript.
	// A reload FAILURE must be surfaced (and the stale in-memory context dropped),
	// not ignored — silently keeping m.sessionEvents would re-send rewound-away
	// events on the next prompt, defeating the rewind.
	if meta, getErr := m.sessionStore.Get(m.activeSession.SessionID); getErr == nil {
		m.activeSession = *meta
	}
	events, readErr := m.sessionStore.ReadEvents(m.activeSession.SessionID)
	if readErr != nil {
		m.sessionEvents = nil // drop stale context so it can't reach the next prompt
		return m, fmt.Sprintf("Rewind\nrewound to sequence %d, but reloading the session failed (in-memory context cleared): %s", target, readErr.Error())
	}
	m.sessionEvents = append([]sessions.Event{}, events...)
	rows := initialTranscript()
	for _, row := range transcriptRowsFromSessionEvents(events) {
		rows = appendTranscriptRow(rows, row)
	}
	m.transcript = rows

	summary := fmt.Sprintf("Rewound to sequence %d\n%d file(s) restored, %d deleted, %d skipped.",
		target, report.FilesRestored, report.FilesDeleted, len(report.Skipped))
	if len(report.Skipped) > 0 {
		summary += "\nskipped (not recoverable): " + strings.Join(report.Skipped, ", ")
	}
	return m, summary
}

// resolveRewindTarget maps a /rewind argument to a keep-through event sequence.
func (m model) resolveRewindTarget(arg string) (int, error) {
	events, err := m.sessionStore.ReadEvents(m.activeSession.SessionID)
	if err != nil {
		return 0, err
	}
	if arg == "" || arg == "latest" {
		lastCheckpoint := 0
		for _, ev := range events {
			if ev.Type == sessions.EventSessionCheckpoint && ev.Sequence > lastCheckpoint {
				lastCheckpoint = ev.Sequence
			}
		}
		if lastCheckpoint == 0 {
			return 0, fmt.Errorf("no checkpoints to rewind")
		}
		return lastCheckpoint - 1, nil // undo the most recent checkpoint
	}
	seq, err := strconv.Atoi(arg)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("usage: /rewind [latest|<sequence>]")
	}
	return seq, nil
}

func (m model) compactText(requested bool) string {
	status := commandStatusInfo
	if requested {
		status = commandStatusWarning
	}
	return renderCommandOutput(commandOutput{
		Title:  "Compact",
		Status: status,
		Sections: []commandSection{
			{
				Title: "State",
				Lines: []string{
					"summary: " + m.compactionStatus(),
					"requested: " + boolText(m.compactRequests > 0),
					fmt.Sprintf("visible transcript rows: %d", len(m.transcript)),
				},
			},
			{
				Title: "Backend",
				Lines: []string{"state: pending integration"},
			},
		},
		Hints: []string{"compaction request is tracked for this TUI session"},
	})
}

func (m model) compactionStatus() string {
	if m.compactRequests > 0 {
		return "requested, not yet compacted"
	}
	return "not compacted"
}

func (m model) recordUsageEvent(modelID string, event zeroruntime.Usage) (model, []transcriptRow) {
	if m.usageTracker == nil || strings.TrimSpace(modelID) == "" {
		return m, nil
	}
	normalized, runtimeUsage, err := usage.Normalize(event)
	if err != nil {
		return m, []transcriptRow{{kind: rowError, text: "usage: " + err.Error()}}
	}
	if _, err := m.usageTracker.Record(usage.RecordInput{
		ModelID: modelID,
		Usage:   runtimeUsage,
		Source:  "tui",
	}); err != nil {
		if isUnpricedUsageError(err) {
			m.unpricedRequests++
			m.unpricedTokens += normalized.TotalTokens
			return m, nil
		}
		return m, []transcriptRow{{kind: rowError, text: "usage: " + err.Error()}}
	}
	return m, nil
}

func isUnpricedUsageError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"unknown zero model",
		"missing model input pricing rate",
		"missing model output pricing rate",
		"invalid model cached input pricing rate",
		"no model cost tier covers",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (m model) usageSummaryText() string {
	if m.usageTracker == nil {
		return "usage unavailable"
	}
	summary := m.usageTracker.Summary()
	if summary.RecordCount == 0 && m.unpricedRequests == 0 {
		return "no usage yet"
	}
	if summary.RecordCount == 0 {
		return formatUnpricedUsage(m.unpricedRequests, m.unpricedTokens)
	}
	if m.unpricedRequests == 0 {
		return usage.FormatSummary(summary)
	}
	return usage.FormatSummary(summary) + "; " + formatUnpricedUsage(m.unpricedRequests, m.unpricedTokens)
}

func formatUnpricedUsage(requests int, tokens int) string {
	requestLabel := "requests"
	if requests == 1 {
		requestLabel = "request"
	}
	return fmt.Sprintf("%d %s, %d tokens, cost unavailable", requests, requestLabel, tokens)
}
