package tui

import (
	"fmt"
	"strings"
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

const defaultCommandFooterText = "/help  /model  /provider  /context  /tools  /permissions  /clear  /exit  Esc clear  Ctrl+C quit"

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

func renderRow(row transcriptRow) string {
	switch row.kind {
	case rowWelcome:
		return row.text
	case rowUser:
		return "user: " + row.text
	case rowAssistant:
		return "assistant: " + row.text
	case rowToolCall:
		return row.text
	case rowToolResult:
		return row.text
	case rowError:
		return "error: " + row.text
	default:
		return row.text
	}
}
