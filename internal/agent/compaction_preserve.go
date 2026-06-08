package agent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

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
	return formatSkills(loadedSkills(messages))
}

// skillEntry is one loaded skill: its name and (capped) body.
type skillEntry struct {
	name string
	body string
}

// loadedSkills returns the skills loaded via the skill tool in messages — the
// latest body per name, in first-seen order — matching each skill tool call to
// its tool result by id.
func loadedSkills(messages []zeroruntime.Message) []skillEntry {
	nameByID := map[string]string{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Name == toolNameSkill && call.ID != "" {
				nameByID[call.ID] = skillNameFromArguments(call.Arguments)
			}
		}
	}
	if len(nameByID) == 0 {
		return nil
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

	entries := make([]skillEntry, 0, len(nameOrder))
	for _, name := range nameOrder {
		entries = append(entries, skillEntry{name: name, body: bodyByName[name]})
	}
	return entries
}

// formatSkills renders skill entries as "### name\nbody" blocks.
func formatSkills(entries []skillEntry) string {
	if len(entries) == 0 {
		return ""
	}
	sections := make([]string, 0, len(entries))
	for _, e := range entries {
		sections = append(sections, "### "+e.name+"\n"+e.body)
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

// truncationNote is appended to a capped skill body. Its length is reserved
// within the byte budget so the result never exceeds maxPreservedSkillBytes.
const truncationNote = "\n… (truncated; re-load the skill for the full body)"

// capBody truncates an over-long skill body to maxPreservedSkillBytes BYTES,
// cutting on a UTF-8 rune boundary (never splitting a multibyte rune) and
// reserving room for the note so the result stays within the byte budget. The
// note is added only when the body is actually truncated.
func capBody(body string) string {
	if len(body) <= maxPreservedSkillBytes {
		return body
	}
	limit := maxPreservedSkillBytes - len(truncationNote)
	if limit < 0 {
		limit = 0
	}
	// Walk back to the start of a rune so a multibyte sequence is never split.
	for limit > 0 && !utf8.RuneStart(body[limit]) {
		limit--
	}
	return body[:limit] + truncationNote
}

// appendPreservedState appends the active plan and loaded-skill sections to a
// compaction summary so structured state survives verbatim. middle is the slice
// being summarized away.
//
// It is robust across REPEATED compactions: after the first compaction the plan
// and skills live only as text inside the injected summary message, which on a
// later compaction lands in middle with no real tool calls left to extract. So
// when middle has no fresh update_plan / skill tool calls, the preserved
// sections are carried forward from the prior summary instead of being lost.
// Fresh tool calls always override the carried-forward copy.
func appendPreservedState(summary string, middle []zeroruntime.Message) string {
	prior := latestSummaryContent(middle)

	// Plan: a fresh update_plan in middle is authoritative; otherwise carry
	// forward the plan section preserved by an earlier compaction.
	plan := extractLatestPlan(middle)
	if plan == "" {
		plan = sectionText(prior, planPreserveLabel)
	}
	if plan != "" {
		summary += "\n\n" + planPreserveLabel + "\n" + plan
	}

	// Skills: merge skills preserved by an earlier compaction (older) with fresh
	// skill loads in middle (newer wins per name), so a loaded skill survives
	// repeated compactions even when the summarizer drops the section.
	skills := mergeSkillEntries(parseSkillSection(sectionText(prior, skillsPreserveLabel)), loadedSkills(middle))
	if formatted := formatSkills(skills); formatted != "" {
		summary += "\n\n" + skillsPreserveLabel + "\n" + formatted
	}
	return summary
}

// latestSummaryContent returns the content of the most recent injected summary
// message in messages (a user message beginning with summaryLabel), or "".
func latestSummaryContent(messages []zeroruntime.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == zeroruntime.MessageRoleUser && strings.HasPrefix(strings.TrimSpace(m.Content), summaryLabel) {
			return m.Content
		}
	}
	return ""
}

// sectionText returns the body of the preserved section introduced by label in
// content (from the LAST occurrence — the authoritative copy this code appended
// — up to the next "## " heading or the end), or "".
func sectionText(content, label string) string {
	idx := strings.LastIndex(content, label)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimPrefix(content[idx+len(label):], "\n")
	if next := strings.Index(rest, "\n## "); next >= 0 {
		rest = rest[:next]
	}
	return strings.TrimSpace(rest)
}

// parseSkillSection parses a preserved skills section ("### name\nbody" blocks)
// back into ordered skill entries.
func parseSkillSection(text string) []skillEntry {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var entries []skillEntry
	for _, block := range strings.Split(text, "### ") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		name, body := block, ""
		if nl := strings.IndexByte(block, '\n'); nl >= 0 {
			name = strings.TrimSpace(block[:nl])
			body = strings.TrimSpace(block[nl+1:])
		}
		if name != "" {
			entries = append(entries, skillEntry{name: name, body: body})
		}
	}
	return entries
}

// mergeSkillEntries overlays newer skill loads onto older preserved entries by
// name (newer body wins), keeping the older order and appending genuinely-new
// skills after.
func mergeSkillEntries(older, newer []skillEntry) []skillEntry {
	if len(newer) == 0 {
		return older
	}
	newBody := make(map[string]string, len(newer))
	for _, e := range newer {
		newBody[e.name] = e.body
	}
	merged := make([]skillEntry, 0, len(older)+len(newer))
	seen := make(map[string]bool, len(older))
	for _, e := range older {
		if b, ok := newBody[e.name]; ok {
			e.body = b
		}
		merged = append(merged, e)
		seen[e.name] = true
	}
	for _, e := range newer {
		if !seen[e.name] {
			merged = append(merged, e)
		}
	}
	return merged
}
