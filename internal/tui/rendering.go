package tui

import (
	"fmt"
	"strings"

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

func shellOnlyCommandText(name string) string {
	return fmt.Sprintf("%s is registered in the Go TUI shell but is not wired yet.", name)
}

func helpText() string {
	return "Commands:\n" + strings.Join(formatCommandHelpLines(), "\n") + "\nSubmit text to ask the assistant."
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
	default:
		return row.text
	}
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
