package agent

import (
	"encoding/json"
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Compaction preservation.
//
// A plain prose summary loses two things the model needs to keep working: the
// active plan (issued via the update_plan tool) and any skill instructions it
// loaded (via the skill tool). When those turns fall into the elided middle,
// the plan and skill bodies vanish from context. To prevent that, Compact
// appends them VERBATIM to the injected summary so structured state survives a
// compaction exactly rather than being paraphrased away.

const (
	toolNameUpdatePlan = "update_plan"
	toolNameSkill      = "skill"
)

// planPreserveLabel / skillsPreserveLabel head the preserved sections so they
// are unmistakable in the transcript (and so tests can assert on them).
const planPreserveLabel = "## Active plan (preserved across compaction)"
const skillsPreserveLabel = "## Loaded skills (preserved across compaction)"

// maxPreservedSkillBytes caps how much of each loaded skill body is carried
// across a compaction, so a large skill can't defeat the compaction it is part
// of. The skill's name and head survive; the model can re-load it in full if it
// needs the rest.
const maxPreservedSkillBytes = 2 << 10 // 2 KiB

// extractLatestPlan returns a formatted view of the most recent update_plan tool
// call in messages, so an in-progress plan survives when its turns are elided by
// compaction. Returns "" when no plan was issued.
func extractLatestPlan(messages []zeroruntime.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		calls := messages[i].ToolCalls
		for j := len(calls) - 1; j >= 0; j-- {
			if calls[j].Name != toolNameUpdatePlan {
				continue
			}
			if formatted := formatPlanArguments(calls[j].Arguments); formatted != "" {
				return formatted
			}
		}
	}
	return ""
}

// formatPlanArguments renders an update_plan tool call's JSON arguments
// ({"plan":[{content,status,...}]}) as terse status-tagged bullet lines. Returns
// "" on malformed arguments or an empty plan.
func formatPlanArguments(arguments string) string {
	var parsed struct {
		Plan []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &parsed); err != nil {
		return ""
	}
	lines := make([]string, 0, len(parsed.Plan))
	for _, item := range parsed.Plan {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		status := strings.TrimSpace(item.Status)
		if status == "" {
			status = "pending"
		}
		lines = append(lines, "- ["+status+"] "+content)
	}
	return strings.Join(lines, "\n")
}

// extractLoadedSkills returns the names and bodies of skills loaded via the skill
// tool in messages, so loaded skill instructions survive compaction. Each skill
// tool call is matched to its tool result by id; the latest body per skill wins,
// and bodies are capped at maxPreservedSkillBytes. Returns "" when none loaded.
func extractLoadedSkills(messages []zeroruntime.Message) string {
	nameByID := map[string]string{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Name == toolNameSkill && call.ID != "" {
				nameByID[call.ID] = skillNameFromArguments(call.Arguments)
			}
		}
	}
	if len(nameByID) == 0 {
		return ""
	}

	bodyByName := map[string]string{}
	nameOrder := make([]string, 0, len(nameByID))
	for _, message := range messages {
		if message.Role != zeroruntime.MessageRoleTool || message.ToolCallID == "" {
			continue
		}
		name, ok := nameByID[message.ToolCallID]
		if !ok {
			continue
		}
		if name == "" {
			name = "(unnamed)"
		}
		body := strings.TrimSpace(message.Content)
		if body == "" {
			continue
		}
		if _, seen := bodyByName[name]; !seen {
			nameOrder = append(nameOrder, name)
		}
		bodyByName[name] = capBody(body)
	}
	if len(nameOrder) == 0 {
		return ""
	}

	sections := make([]string, 0, len(nameOrder))
	for _, name := range nameOrder {
		sections = append(sections, "### "+name+"\n"+bodyByName[name])
	}
	return strings.Join(sections, "\n\n")
}

// skillNameFromArguments pulls the "name" field from a skill tool call's JSON
// arguments ({"name":"..."}). Returns "" on malformed arguments.
func skillNameFromArguments(arguments string) string {
	var parsed struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Name)
}

// capBody truncates an over-long skill body at a rune boundary with a note.
func capBody(body string) string {
	if len(body) <= maxPreservedSkillBytes {
		return body
	}
	runes := []rune(body)
	if len(runes) > maxPreservedSkillBytes {
		runes = runes[:maxPreservedSkillBytes]
	}
	return string(runes) + "\n… (truncated; re-load the skill for the full body)"
}

// appendPreservedState appends the active plan and loaded-skill sections found
// in the elided messages to a compaction summary, so structured state survives
// verbatim. middle is the slice being summarized away.
func appendPreservedState(summary string, middle []zeroruntime.Message) string {
	if plan := extractLatestPlan(middle); plan != "" {
		summary += "\n\n" + planPreserveLabel + "\n" + plan
	}
	if skills := extractLoadedSkills(middle); skills != "" {
		summary += "\n\n" + skillsPreserveLabel + "\n" + skills
	}
	return summary
}
