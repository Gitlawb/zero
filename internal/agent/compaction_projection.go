package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const (
	compactionUserWordBudget       = 256
	compactionAssistantWordBudget  = 200
	compactionToolCallsPerTurn     = 8
	compactionToolArgumentBytes    = 120
	compactionErrorBytes           = 150
	compactionPreviousSummaryWords = 400
	compactionBriefMaxLines        = 120
)

var compactionWordPattern = regexp.MustCompile(`[\p{L}\p{N}]+`)

// projectCompactionInput removes reconstructible tool output before the model
// summary call. It retains a bounded transcript of user intent, assistant
// decisions, tool actions, and concise errors. Exact durable state is appended
// separately from the original messages after the summary returns.
func projectCompactionInput(messages []zeroruntime.Message) []zeroruntime.Message {
	sections := make([]string, 0, len(messages))
	previousSummary := ""
	for index, message := range messages {
		switch message.Role {
		case zeroruntime.MessageRoleUser:
			content := message.Content
			if marker := strings.Index(content, preservedStateLabel); marker >= 0 {
				content = content[:marker]
			}
			trimmed := strings.TrimSpace(content)
			if strings.HasPrefix(trimmed, summaryLabel) {
				previousSummary = clipInformativeWords(strings.TrimSpace(strings.TrimPrefix(trimmed, summaryLabel)), compactionPreviousSummaryWords)
				continue
			}
			if content = clipInformativeWords(content, compactionUserWordBudget); content != "" {
				sections = append(sections, fmt.Sprintf("[user #%d]\n%s", index, content))
			}
		case zeroruntime.MessageRoleAssistant:
			lines := make([]string, 0, 1+len(message.ToolCalls))
			if content := clipInformativeWords(message.Content, compactionAssistantWordBudget); content != "" {
				lines = append(lines, content)
			}
			calls := message.ToolCalls
			omitted := 0
			if len(calls) > compactionToolCallsPerTurn {
				omitted = len(calls) - compactionToolCallsPerTurn
				calls = calls[omitted:]
			}
			if omitted > 0 {
				lines = append(lines, fmt.Sprintf("* (%d earlier tool calls omitted)", omitted))
			}
			for _, call := range calls {
				lines = append(lines, compactionToolCallLine(call))
			}
			if len(lines) > 0 {
				sections = append(sections, fmt.Sprintf("[assistant #%d]\n%s", index, strings.Join(lines, "\n")))
			}
		case zeroruntime.MessageRoleTool:
			if !isLikelyToolError(message.Content) {
				continue
			}
			name := toolNameForResult(messages, index)
			if name == "" {
				name = "tool"
			}
			sections = append(sections, fmt.Sprintf("[tool_error #%d] %s\n%s", index, name, clipBytes(firstLine(message.Content), compactionErrorBytes)))
		}
	}
	if len(sections) == 0 {
		if previousSummary == "" {
			return nil
		}
	}
	brief := capCompactionBrief(strings.Join(sections, "\n\n"))
	if previousSummary != "" {
		brief = "[previous summary]\n" + previousSummary + "\n\n" + brief
	}
	return []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: strings.TrimSpace(brief)}}
}

func capCompactionBrief(brief string) string {
	lines := strings.Split(brief, "\n")
	if len(lines) <= compactionBriefMaxLines {
		return brief
	}
	kept := lines[len(lines)-compactionBriefMaxLines:]
	for index, line := range kept {
		if strings.HasPrefix(line, "[") {
			kept = kept[index:]
			break
		}
	}
	omitted := len(lines) - len(kept)
	return fmt.Sprintf("...(%d earlier lines omitted)\n\n%s", omitted, strings.Join(kept, "\n"))
}

func compactionToolCallLine(call zeroruntime.ToolCall) string {
	detail := ""
	var arguments map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(call.Arguments)), &arguments) == nil {
		for _, key := range []string{"file_path", "path", "query", "pattern"} {
			if value, ok := arguments[key].(string); ok && strings.TrimSpace(value) != "" {
				detail = value
				break
			}
		}
	}
	if detail == "" {
		detail = commandFromArguments(call.Arguments)
	}
	if detail == "" {
		return "* " + call.Name
	}
	return fmt.Sprintf("* %s %q", call.Name, clipBytes(detail, compactionToolArgumentBytes))
}

func isLikelyToolError(content string) bool {
	prefix := strings.TrimSpace(content)
	if len(prefix) > compactionErrorBytes {
		prefix = prefix[:compactionErrorBytes]
	}
	lower := strings.ToLower(prefix)
	for _, prefix := range []string{"error:", "tool error:", "failed:", "permission denied", "command failed"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func clipInformativeWords(text string, limit int) string {
	flat := strings.Join(strings.Fields(text), " ")
	if flat == "" || limit <= 0 {
		return ""
	}
	count := 0
	for _, location := range compactionWordPattern.FindAllStringIndex(flat, -1) {
		word := strings.ToLower(flat[location[0]:location[1]])
		if compactionStopWords[word] {
			continue
		}
		count++
		if count > limit {
			end := location[0]
			return strings.TrimSpace(flat[:end]) + "...(truncated)"
		}
	}
	return flat
}

func clipBytes(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	end := limit - 3
	for end > 0 && end < len(text) && text[end]&0xc0 == 0x80 {
		end--
	}
	return strings.TrimSpace(text[:end]) + "..."
}

var compactionStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "being": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "shall": true, "can": true, "need": true, "must": true,
	"to": true, "of": true, "in": true, "for": true, "on": true, "with": true, "at": true,
	"by": true, "from": true, "as": true, "into": true, "through": true, "during": true,
	"before": true, "after": true, "above": true, "below": true, "between": true, "under": true, "over": true,
	"and": true, "but": true, "or": true, "nor": true, "not": true, "so": true, "yet": true,
	"both": true, "either": true, "neither": true, "each": true, "every": true, "all": true,
	"any": true, "few": true, "more": true, "most": true, "other": true, "some": true, "such": true, "no": true,
	"that": true, "this": true, "these": true, "those": true, "it": true, "its": true,
	"i": true, "me": true, "my": true, "we": true, "our": true, "you": true, "your": true,
	"he": true, "him": true, "his": true, "she": true, "her": true, "they": true, "them": true, "their": true,
	"who": true, "which": true, "what": true, "if": true, "then": true, "than": true,
	"when": true, "where": true, "how": true, "just": true, "also": true,
}
