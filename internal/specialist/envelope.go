package specialist

import (
	"fmt"
	"strings"
)

func WrapSystemPrompt(name string, systemPrompt string, prompt string, description string) string {
	var builder strings.Builder
	builder.WriteString("# Specialist Invocation\n\n")
	fmt.Fprintf(&builder, "Specialist: %s\n", strings.TrimSpace(name))
	if trimmed := strings.TrimSpace(description); trimmed != "" {
		fmt.Fprintf(&builder, "Task description: %s\n", trimmed)
	}
	builder.WriteString("\n")
	builder.WriteString("You are a specialized sub-agent invoked by another Zero agent.\n")
	builder.WriteString("Stay strictly within the assigned task. Do not broaden scope or start unrelated work.\n")
	builder.WriteString("Complete the task, report concrete outcomes, and stop when done.\n\n")
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		builder.WriteString("## Specialist Instructions\n\n")
		builder.WriteString(trimmed)
		builder.WriteString("\n\n")
	}
	builder.WriteString("## Assigned Task\n\n")
	builder.WriteString(strings.TrimSpace(prompt))
	builder.WriteString("\n\n")
	builder.WriteString("## Reporting\n\n")
	builder.WriteString("- Summarize actions taken and outcomes.\n")
	builder.WriteString("- Mention blockers, uncertainty, or follow-up only when relevant.\n")
	builder.WriteString("- Return the requested result without extra commentary.\n")
	return strings.TrimSpace(builder.String())
}

func WrapResumePrompt(prompt string) string {
	return strings.TrimSpace(strings.Join([]string{
		"# Follow-up Instructions",
		"",
		strings.TrimSpace(prompt),
		"",
		"## Reporting",
		"",
		"- Continue the existing specialist session.",
		"- Summarize new actions and outcomes.",
		"- Stop when the follow-up is complete.",
	}, "\n"))
}
