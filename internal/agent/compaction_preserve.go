package agent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Compaction preservation.
//
// A plain prose summary loses structured state the model needs to keep working:
// the active plan, loaded deferred-tool schemas, loaded skills, and project
// instruction blocks. When those turns fall into the elided middle, Compact
// appends that state to the injected summary as JSON so it survives exactly
// rather than being paraphrased away.

const (
	toolNameUpdatePlan = "update_plan"
	toolNameToolSearch = "tool_search"
	toolNameSkill      = "skill"
)

const (
	projectInstructionsHeadingPrefix = "# "
	projectInstructionsHeadingMarker = " instructions for "
	projectInstructionsOpenTag       = "<INSTRUCTIONS>"
	projectInstructionsCloseTag      = "</INSTRUCTIONS>"
)

// preservedStateLabel heads the preserved-state block. Keep this label stable so
// summaries created by earlier builds remain parseable; the JSON body may carry
// more fields than the historical label names.
//
// The block body is a single line of JSON (see formatPreservedState): JSON
// escapes everything, so markdown headings, code fences, or quotes round-trip
// losslessly across repeated compactions.
const preservedStateLabel = "## Preserved state (active plan + loaded skills; carried across compaction)"

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
			Step    string `json:"step"`
			Status  string `json:"status"`
			Notes   string `json:"notes"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &parsed); err != nil {
		return ""
	}
	lines := make([]string, 0, len(parsed.Plan))
	for _, item := range parsed.Plan {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			content = strings.TrimSpace(item.Step)
		}
		if content == "" {
			continue
		}
		status := strings.TrimSpace(item.Status)
		if status == "" {
			status = "pending"
		}
		line := "- [" + status + "] " + content
		if notes := strings.TrimSpace(item.Notes); notes != "" {
			line += "\n  Notes: " + notes
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// skillEntry is a named preserved body. It began as loaded-skill state and is
// reused for loaded tools and project instruction blocks.
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

// loadedToolSchemas returns tool_search-loaded schemas from their normal tool
// result text. ToolResult.Meta is not part of zeroruntime.Message history, so the
// rendered "Loaded N tools" output is the durable transcript format.
func loadedToolSchemas(messages []zeroruntime.Message) []skillEntry {
	toolSearchIDs := map[string]bool{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Name == toolNameToolSearch && call.ID != "" {
				toolSearchIDs[call.ID] = true
			}
		}
	}
	if len(toolSearchIDs) == 0 {
		return nil
	}

	bodyByName := map[string]string{}
	nameOrder := make([]string, 0)
	for _, message := range messages {
		if message.Role != zeroruntime.MessageRoleTool || !toolSearchIDs[message.ToolCallID] {
			continue
		}
		for _, entry := range loadedToolEntriesFromOutput(message.Content) {
			if _, seen := bodyByName[entry.name]; !seen {
				nameOrder = append(nameOrder, entry.name)
			}
			bodyByName[entry.name] = entry.body
		}
	}

	entries := make([]skillEntry, 0, len(nameOrder))
	for _, name := range nameOrder {
		entries = append(entries, skillEntry{name: name, body: bodyByName[name]})
	}
	return entries
}

func loadedToolEntriesFromOutput(output string) []skillEntry {
	output = strings.TrimSpace(output)
	if !strings.HasPrefix(output, "Loaded ") || !strings.Contains(output, "Full schemas follow") {
		return nil
	}
	lines := strings.Split(output, "\n")
	var entries []skillEntry
	for i := 0; i < len(lines); i++ {
		name, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "## ")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		start := i
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				end = j
				break
			}
		}
		entries = append(entries, skillEntry{name: name, body: capBody(strings.TrimSpace(strings.Join(lines[start:end], "\n")))})
		i = end - 1
	}
	return entries
}

func projectInstructionEntries(messages []zeroruntime.Message) []skillEntry {
	bodyBySource := map[string]string{}
	sourceOrder := make([]string, 0)
	for _, message := range messages {
		if message.Role != zeroruntime.MessageRoleUser {
			continue
		}
		source, body := projectInstructionBlock(message.Content)
		if body == "" {
			continue
		}
		if _, seen := bodyBySource[source]; !seen {
			sourceOrder = append(sourceOrder, source)
		}
		bodyBySource[source] = body
	}

	entries := make([]skillEntry, 0, len(sourceOrder))
	for _, source := range sourceOrder {
		entries = append(entries, skillEntry{name: source, body: bodyBySource[source]})
	}
	return entries
}

func projectInstructionBlock(content string) (string, string) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, projectInstructionsHeadingPrefix) {
		return "", ""
	}
	firstLineEnd := strings.IndexByte(content, '\n')
	if firstLineEnd < 0 {
		return "", ""
	}
	heading := strings.TrimSpace(content[:firstLineEnd])
	if !strings.Contains(heading, projectInstructionsHeadingMarker) {
		return "", ""
	}
	open := strings.Index(content, projectInstructionsOpenTag)
	close := strings.Index(content, projectInstructionsCloseTag)
	if open < 0 || close < open {
		return "", ""
	}
	close += len(projectInstructionsCloseTag)

	source := strings.TrimPrefix(heading, "# ")
	body := strings.TrimSpace(heading + "\n\n" + strings.TrimSpace(content[open:close]))
	return source, body
}

// skillNameFromArguments pulls the "name" field from a skill tool call's JSON
// arguments ({"name":"..."}). Returns "" on malformed arguments.
func skillNameFromArguments(arguments string) string {
	var parsed struct {
		Name  string `json:"name"`
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &parsed); err != nil {
		return ""
	}
	if name := strings.TrimSpace(parsed.Name); name != "" {
		return name
	}
	return strings.TrimSpace(parsed.Skill)
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

// preservedState is the JSON shape of the carried-across-compaction block.
type preservedState struct {
	Plan                string                 `json:"plan,omitempty"`
	Tools               []preservedTool        `json:"tools,omitempty"`
	Skills              []preservedSkill       `json:"skills,omitempty"`
	ProjectInstructions []preservedInstruction `json:"project_instructions,omitempty"`
}

type preservedTool struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

type preservedSkill struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

type preservedInstruction struct {
	Source string `json:"source"`
	Body   string `json:"body"`
}

// appendPreservedState appends active structured state to a compaction summary
// as a single JSON block. middle is the slice being summarized away.
//
// It is robust across repeated compactions: after the first compaction the state
// may live only inside the injected summary message, which on a later compaction
// lands in middle with no real tool calls left to extract. Fresh tool calls and
// instruction blocks override the carried-forward copy by name/source.
func appendPreservedState(summary string, middle []zeroruntime.Message) string {
	priorState := parsePreservedStateBlock(latestSummaryContent(middle))

	// Plan: a fresh update_plan in middle is authoritative; otherwise carry the
	// plan preserved by an earlier compaction.
	plan := extractLatestPlan(middle)
	if plan == "" {
		plan = priorState.Plan
	}

	// Tools: preserve deferred tool_search schemas from the transcript. Fresh
	// loads override older carried copies by name.
	tools := mergeSkillEntries(preservedToolsToEntries(priorState.Tools), loadedToolSchemas(middle))

	// Skills: merge skills preserved earlier (older) with fresh loads (newer wins
	// per name), so a loaded skill survives repeated compactions.
	skills := mergeSkillEntries(preservedSkillsToEntries(priorState.Skills), loadedSkills(middle))

	instructions := mergeSkillEntries(
		preservedInstructionsToEntries(priorState.ProjectInstructions),
		projectInstructionEntries(middle),
	)

	if block := formatPreservedState(plan, tools, skills, instructions); block != "" {
		summary += "\n\n" + block
	}
	return summary
}

// formatPreservedState renders state as the labelled, single-line
// JSON block. Returns "" when there is nothing to preserve.
func formatPreservedState(plan string, tools, skills, instructions []skillEntry) string {
	if plan == "" && len(tools) == 0 && len(skills) == 0 && len(instructions) == 0 {
		return ""
	}
	state := preservedState{Plan: plan}
	for _, t := range tools {
		state.Tools = append(state.Tools, preservedTool{Name: t.name, Body: t.body})
	}
	for _, s := range skills {
		state.Skills = append(state.Skills, preservedSkill{Name: s.name, Body: s.body})
	}
	for _, i := range instructions {
		state.ProjectInstructions = append(state.ProjectInstructions, preservedInstruction{Source: i.name, Body: i.body})
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	return preservedStateLabel + "\n" + string(encoded)
}

// parsePreservedState recovers the plan + skills from a prior summary's preserved
// block. JSON escaping makes this lossless even when a skill body contains
// markdown headings, code fences, or quotes. Returns ("", nil) when absent or
// malformed.
func parsePreservedState(summaryContent string) (string, []skillEntry) {
	state := parsePreservedStateBlock(summaryContent)
	return state.Plan, preservedSkillsToEntries(state.Skills)
}

func parsePreservedStateBlock(summaryContent string) preservedState {
	idx := strings.LastIndex(summaryContent, preservedStateLabel)
	if idx < 0 {
		return preservedState{}
	}
	rest := strings.TrimPrefix(summaryContent[idx+len(preservedStateLabel):], "\n")
	// The JSON is a single line (json.Marshal escapes newlines).
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return preservedState{}
	}
	var state preservedState
	if err := json.Unmarshal([]byte(rest), &state); err != nil {
		return preservedState{}
	}
	return state
}

func preservedToolsToEntries(tools []preservedTool) []skillEntry {
	entries := make([]skillEntry, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			continue
		}
		entries = append(entries, skillEntry{name: t.Name, body: t.Body})
	}
	return entries
}

func preservedSkillsToEntries(skills []preservedSkill) []skillEntry {
	entries := make([]skillEntry, 0, len(skills))
	for _, s := range skills {
		if s.Name == "" {
			continue
		}
		entries = append(entries, skillEntry{name: s.Name, body: s.Body})
	}
	return entries
}

func preservedInstructionsToEntries(instructions []preservedInstruction) []skillEntry {
	entries := make([]skillEntry, 0, len(instructions))
	for _, i := range instructions {
		if i.Source == "" {
			continue
		}
		entries = append(entries, skillEntry{name: i.Source, body: i.Body})
	}
	return entries
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
