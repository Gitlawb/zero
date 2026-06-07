package tui

import (
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

func displayValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (m model) runState() string {
	if m.pending {
		return "running"
	}
	return "ready"
}

// pickerBusyText explains that a settings picker (/model, /mode, /effort, /theme)
// can't be opened while a run is in flight. Opening it then would silently refuse
// the selection once the run lands, so the no-arg command no-ops into this notice.
func pickerBusyText(name string) string {
	label := strings.TrimPrefix(name, "/")
	return renderCommandOutput(commandOutput{
		Title:  label,
		Status: commandStatusWarning,
		Sections: []commandSection{{
			Title: "Busy",
			Lines: []string{"Can't change " + label + " while a run is in progress."},
		}},
		Hints: []string{"press Esc to cancel the run, then try again"},
	})
}

func shellOnlyCommandText(name string) string {
	return renderCommandOutput(commandOutput{
		Title:  strings.TrimPrefix(name, "/"),
		Status: commandStatusWarning,
		Sections: []commandSection{{
			Title: "State",
			Lines: []string{"This control is available in the TUI but does not have a backend setting yet."},
		}},
		Hints: []string{"use /help to inspect active commands"},
	})
}

func helpText() string {
	return formatGroupedCommandHelp()
}

const defaultCommandFooterText = "/help  /model  /provider  /context  /compact  /effort  /style  /tools  /permissions  /clear  /exit  Esc clear  Ctrl+C quit"

func commandFooterText() string {
	return formatCommandFooterText(commandDefinitions, false)
}

func (m model) footerText() string {
	return strings.Join([]string{
		m.runState(),
		displayValue(m.modelName, "model:none"),
		m.usageSummaryText(),
		formatCommandFooterText(commandDefinitions, m.pending),
	}, "  ")
}

func formatCommandFooterText(commands []commandDefinition, pending bool) string {
	if len(commands) == 0 {
		return defaultCommandFooterText
	}

	namesByKind := make(map[commandKind]string, len(commands))
	for _, command := range commands {
		namesByKind[command.kind] = command.name
	}

	featured := []commandKind{
		commandHelp,
		commandModel,
		commandProvider,
		commandContext,
		commandCompact,
		commandEffort,
		commandStyle,
		commandTools,
		commandPermissions,
		commandClear,
		commandExit,
	}
	parts := make([]string, 0, len(featured)+2)
	for _, kind := range featured {
		name := namesByKind[kind]
		if name != "" {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return defaultCommandFooterText
	}

	if pending {
		parts = append(parts, "Esc cancel")
	} else {
		parts = append(parts, "Esc clear")
	}
	parts = append(parts, "Ctrl+C quit")
	return strings.Join(parts, "  ")
}

func renderRow(row transcriptRow, width int) string {
	switch row.kind {
	case rowWelcome:
		return zeroTheme.muted.Render(row.text)
	case rowUser:
		return zeroTheme.you.Render("▍ you") + "\n" + indentText(zeroTheme.text.Render(row.text), 2)
	case rowAssistant:
		return zeroTheme.zero.Render("◇ zero") + "\n" + indentText(zeroTheme.text.Render(row.text), 2)
	case rowSystem:
		return indentText(zeroTheme.text.Render(row.text), 2)
	case rowError:
		return zeroTheme.red.Render("✗ ") + zeroTheme.text.Render(row.text)
	case rowToolCall:
		return renderToolCallRow(row)
	case rowToolResult:
		return renderToolResultRow(row, width)
	case rowPermission:
		return renderPermissionRow(row)
	case rowAskUser:
		return renderAskUserRow(row)
	default:
		return row.text
	}
}

func renderAskUserRow(row transcriptRow) string {
	line := zeroTheme.zero.Render("ask zero") + "  " + zeroTheme.text.Render(strings.TrimPrefix(row.text, "ask_user: "))
	if detail := strings.TrimSpace(row.detail); detail != "" {
		line += "\n" + indentText(zeroTheme.muted.Render(detail), 2)
	}
	return line
}

func renderToolCallRow(row transcriptRow) string {
	name := row.tool
	if name == "" {
		name = strings.TrimPrefix(row.text, "tool call: ")
	}
	line := zeroTheme.tool.Render("▸ ") + zeroTheme.text.Render(name)
	if hint := strings.TrimSpace(row.detail); hint != "" {
		line += "  " + zeroTheme.muted.Render(hint)
	}
	return line
}

func renderPermissionRow(row transcriptRow) string {
	event := row.permission
	if event == nil {
		return zeroTheme.amber.Render("permission") + "  " + zeroTheme.text.Render(row.text)
	}

	name := event.ToolName
	if name == "" {
		name = row.tool
	}
	action := strings.TrimSpace(string(event.Action))
	if action == "" {
		action = "prompt"
	}

	actionStyle := zeroTheme.amber
	actionLabel := action
	switch event.Action {
	case "allow":
		actionStyle = zeroTheme.green
	case "deny":
		actionStyle = zeroTheme.red
		actionLabel = "denied"
	case "prompt":
		actionStyle = zeroTheme.amber
	}

	line := zeroTheme.amber.Render("permission") + "  " + zeroTheme.text.Render(name) + "  " + actionStyle.Render(actionLabel)
	if event.Risk.Level != "" {
		line += "  " + zeroTheme.muted.Render("risk:"+string(event.Risk.Level))
	}
	if event.GrantMatched {
		line += "  " + zeroTheme.green.Render("grant")
	}
	if detail := strings.TrimSpace(row.detail); detail != "" {
		line += "\n" + indentText(zeroTheme.muted.Render(detail), 2)
	}
	return line
}

func renderFocusedPermissionPrompt(request agent.PermissionRequest, width int) string {
	name := strings.TrimSpace(request.ToolName)
	if name == "" {
		name = "tool"
	}

	header := zeroTheme.amber.Render("permission required") + "  " + zeroTheme.text.Render(name)
	choices := zeroTheme.text.Render("[a] allow") + "  " +
		zeroTheme.text.Render("[d] deny") + "  " +
		zeroTheme.text.Render("[y] always")

	details := []string{}
	if request.Risk.Level != "" {
		details = append(details, "risk:"+string(request.Risk.Level))
	}
	if request.Reason != "" {
		details = append(details, request.Reason)
	}
	if request.SideEffect != "" {
		details = append(details, "side_effect:"+request.SideEffect)
	}
	if len(details) > 0 {
		choices += "\n" + zeroTheme.muted.Render(strings.Join(details, "  "))
	}

	return borderedBlock(width, []string{header, choices})
}

func renderFocusedAskUserPrompt(prompt pendingAskUserPrompt, input string, width int) string {
	questions := prompt.request.Questions
	total := len(questions)
	index := prompt.index
	if index >= total {
		index = total - 1
	}
	if index < 0 {
		index = 0
	}

	lines := []string{}
	heading := zeroTheme.zero.Render("ask zero")
	if header := strings.TrimSpace(prompt.request.Header); header != "" {
		heading += "  " + zeroTheme.text.Render(header)
	}
	lines = append(lines, heading)

	if total > 0 {
		question := questions[index]
		lines = append(lines, zeroTheme.muted.Render(fmt.Sprintf("question %d of %d", index+1, total)))
		lines = append(lines, zeroTheme.text.Render(question.Question))
		if len(question.Options) > 0 {
			lines = append(lines, zeroTheme.muted.Render("options: "+strings.Join(question.Options, ", ")))
		}
	}
	lines = append(lines, zeroTheme.muted.Render("type an answer, Enter to submit · Esc to skip"))

	return borderedBlock(width, lines)
}

func renderToolResultRow(row transcriptRow, width int) string {
	name := row.tool
	if name == "" {
		name = strings.TrimPrefix(row.text, "tool result: ")
	}

	icon := zeroTheme.green.Render("✓")
	if row.status == tools.StatusError {
		icon = zeroTheme.red.Render("✗")
	}

	line := zeroTheme.tool.Render("▸ ") + zeroTheme.text.Render(name) + "  " + icon

	// A diff card already shows the change in full, so skip the flattened
	// one-line summary in that case to avoid duplicating the content.
	if looksLikeDiff(row.detail) {
		return line + "\n" + indentText(diffCard(name, row.detail, width-2), 2)
	}
	if summary := truncateTUIOutput(row.detail, 100); summary != "" {
		line += "  " + zeroTheme.muted.Render(summary)
	}
	return line
}
