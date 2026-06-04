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
	commandUnknown
)

type parsedCommand struct {
	kind commandKind
	text string
}

func parseCommand(input string) parsedCommand {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return parsedCommand{kind: commandEmpty}
	}

	switch trimmed {
	case "/help":
		return parsedCommand{kind: commandHelp}
	case "/clear":
		return parsedCommand{kind: commandClear}
	case "/exit":
		return parsedCommand{kind: commandExit}
	case "/tools":
		return parsedCommand{kind: commandTools}
	case "/permissions":
		return parsedCommand{kind: commandPermissions}
	}

	if strings.HasPrefix(trimmed, "/") {
		return parsedCommand{kind: commandUnknown, text: trimmed}
	}

	return parsedCommand{kind: commandPrompt, text: trimmed}
}
