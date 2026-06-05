package tui

import (
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/modelregistry"
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
		"Effort",
		"active effort: " + m.effortDisplay(),
		"model: " + displayValue(m.modelName, "none"),
	}
	efforts := m.availableReasoningEfforts()
	if len(efforts) == 0 {
		lines = append(lines, "available: none for active model")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "available: "+joinReasoningEfforts(efforts))
	lines = append(lines, "Use /effort <value> or /effort auto.")
	return strings.Join(lines, "\n")
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
	return strings.Join([]string{
		"Style",
		"active style: " + m.responseStyle,
		"available: " + strings.Join(responseStyles, ", "),
		"Use /style <value> to update this TUI session.",
	}, "\n")
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

func (m model) compactText(requested bool) string {
	lines := []string{
		"Compact",
		"status: " + m.compactionStatus(),
		fmt.Sprintf("visible transcript rows: %d", len(m.transcript)),
	}
	if requested {
		lines = append(lines, "request recorded for future compaction backend.")
	} else {
		lines = append(lines, "backend: not wired yet")
	}
	return strings.Join(lines, "\n")
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
	normalized, _, err := usage.Normalize(event)
	if err != nil {
		return m, []transcriptRow{{kind: rowError, text: "usage: " + err.Error()}}
	}
	if _, err := m.usageTracker.Record(usage.RecordInput{
		ModelID: modelID,
		Usage:   event,
		Source:  "tui",
	}); err != nil {
		m.unpricedRequests++
		m.unpricedTokens += normalized.TotalTokens
		return m, nil
	}
	return m, nil
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
