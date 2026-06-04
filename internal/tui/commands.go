package tui

import "strings"

type commandKind int

const (
	commandEmpty commandKind = iota
	commandPrompt
	commandHelp
	commandClear
	commandExit
	commandTools
	commandPermissions
	commandProvider
	commandModel
	commandContext
	commandConfig
	commandDebug
	commandDoctor
	commandPlan
	commandSearch
	commandTheme
	commandInputStyle
	commandUnknown
)

type commandGroup string

const (
	commandGroupSession commandGroup = "session"
	commandGroupModel   commandGroup = "model"
	commandGroupRuntime commandGroup = "runtime"
	commandGroupTools   commandGroup = "tools"
	commandGroupMeta    commandGroup = "meta"
)

type commandDefinition struct {
	name        string
	aliases     []string
	usage       string
	group       commandGroup
	description string
	kind        commandKind
}

type parsedCommand struct {
	kind commandKind
	text string
	name string
}

var commandDefinitions = []commandDefinition{
	{
		name:        "/provider",
		usage:       "/provider",
		group:       commandGroupModel,
		description: "Show the active provider.",
		kind:        commandProvider,
	},
	{
		name:        "/model",
		usage:       "/model [list|id]",
		group:       commandGroupModel,
		description: "Show the active model and model-shell status.",
		kind:        commandModel,
	},
	{
		name:        "/plan",
		usage:       "/plan",
		group:       commandGroupSession,
		description: "Show planning mode status.",
		kind:        commandPlan,
	},
	{
		name:        "/permissions",
		usage:       "/permissions",
		group:       commandGroupRuntime,
		description: "Show the active permission mode.",
		kind:        commandPermissions,
	},
	{
		name:        "/tools",
		usage:       "/tools",
		group:       commandGroupTools,
		description: "List registered tools.",
		kind:        commandTools,
	},
	{
		name:        "/context",
		usage:       "/context",
		group:       commandGroupSession,
		description: "Show current workspace and runtime context.",
		kind:        commandContext,
	},
	{
		name:        "/clear",
		usage:       "/clear",
		group:       commandGroupMeta,
		description: "Clear the visible transcript.",
		kind:        commandClear,
	},
	{
		name:        "/search",
		usage:       "/search",
		group:       commandGroupSession,
		description: "Show code search shell status.",
		kind:        commandSearch,
	},
	{
		name:        "/doctor",
		usage:       "/doctor",
		group:       commandGroupRuntime,
		description: "Show local diagnostics shell status.",
		kind:        commandDoctor,
	},
	{
		name:        "/config",
		usage:       "/config",
		group:       commandGroupRuntime,
		description: "Show active configuration summary.",
		kind:        commandConfig,
	},
	{
		name:        "/debug",
		aliases:     []string{"/debug-mode"},
		usage:       "/debug",
		group:       commandGroupRuntime,
		description: "Show debug mode status.",
		kind:        commandDebug,
	},
	{
		name:        "/theme",
		usage:       "/theme",
		group:       commandGroupSession,
		description: "Show theme shell status.",
		kind:        commandTheme,
	},
	{
		name:        "/input-style",
		usage:       "/input-style",
		group:       commandGroupSession,
		description: "Show input style shell status.",
		kind:        commandInputStyle,
	},
	{
		name:        "/help",
		usage:       "/help",
		group:       commandGroupMeta,
		description: "Show available commands.",
		kind:        commandHelp,
	},
	{
		name:        "/exit",
		aliases:     []string{"/quit"},
		usage:       "/exit",
		group:       commandGroupMeta,
		description: "Exit Zero.",
		kind:        commandExit,
	},
}

func parseCommand(input string) parsedCommand {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return parsedCommand{kind: commandEmpty}
	}

	if strings.HasPrefix(trimmed, "/") {
		name, args := splitCommand(trimmed)
		command, ok := resolveCommand(name)
		if ok {
			return parsedCommand{kind: command.kind, name: command.name, text: args}
		}
		return parsedCommand{kind: commandUnknown, text: trimmed}
	}

	return parsedCommand{kind: commandPrompt, text: trimmed}
}

func splitCommand(input string) (string, string) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return "", ""
	}

	name := parts[0]
	args := strings.TrimSpace(input[len(name):])
	return strings.ToLower(name), args
}

func resolveCommand(name string) (commandDefinition, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, command := range commandDefinitions {
		if normalized == command.name {
			return command, true
		}
		for _, alias := range command.aliases {
			if normalized == alias {
				return command, true
			}
		}
	}
	return commandDefinition{}, false
}

func listCommandNames() []string {
	names := make([]string, 0, len(commandDefinitions))
	for _, command := range commandDefinitions {
		names = append(names, command.name)
		names = append(names, command.aliases...)
	}
	return names
}

func formatCommandHelpLines() []string {
	lines := make([]string, 0, len(commandDefinitions))
	for _, command := range commandDefinitions {
		label := command.usage
		if len(command.aliases) > 0 {
			label += " (" + strings.Join(command.aliases, ", ") + ")"
		}
		lines = append(lines, label+" - "+command.description)
	}
	return lines
}
