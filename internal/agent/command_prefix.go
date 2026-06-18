package agent

import (
	"strings"

	"github.com/Gitlawb/zero/internal/sandbox"
	"mvdan.cc/sh/v3/syntax"
)

var bannedCommandPrefixSuggestions = [][]string{
	{"python3"},
	{"python3", "-"},
	{"python3", "-c"},
	{"python"},
	{"python", "-"},
	{"python", "-c"},
	{"py"},
	{"py", "-3"},
	{"pythonw"},
	{"pyw"},
	{"pypy"},
	{"pypy3"},
	{"git"},
	{"bash"},
	{"bash", "-lc"},
	{"sh"},
	{"sh", "-c"},
	{"sh", "-lc"},
	{"zsh"},
	{"zsh", "-lc"},
	{"/bin/zsh"},
	{"/bin/zsh", "-lc"},
	{"/bin/bash"},
	{"/bin/bash", "-lc"},
	{"pwsh"},
	{"pwsh", "-Command"},
	{"pwsh", "-c"},
	{"powershell"},
	{"powershell", "-Command"},
	{"powershell", "-c"},
	{"powershell.exe"},
	{"powershell.exe", "-Command"},
	{"powershell.exe", "-c"},
	{"env"},
	{"sudo"},
	{"node"},
	{"node", "-e"},
	{"perl"},
	{"perl", "-e"},
	{"ruby"},
	{"ruby", "-e"},
	{"php"},
	{"php", "-r"},
	{"lua"},
	{"lua", "-e"},
	{"osascript"},
}

func proposedCommandPrefix(toolName string, args map[string]any) []string {
	if toolName != "bash" {
		return nil
	}
	command, ok := firstStringArg(args, "command", "cmd", "script", "shell")
	if !ok {
		return nil
	}
	tokens, ok := safeShellCommandTokens(command)
	if !ok {
		return nil
	}
	if requested, ok := requestedPrefixRule(args); ok {
		if safeRequestedPrefix(requested, tokens) {
			return requested
		}
		return nil
	}
	if unsafeCommandPrefix(tokens) {
		return nil
	}
	return append([]string(nil), tokens...)
}

func matchSessionCommandPrefix(toolName string, args map[string]any, options Options) (sandbox.CommandPrefixGrant, bool) {
	if toolName != "bash" || options.Sandbox == nil {
		return sandbox.CommandPrefixGrant{}, false
	}
	command, ok := firstStringArg(args, "command", "cmd", "script", "shell")
	if !ok {
		return sandbox.CommandPrefixGrant{}, false
	}
	tokens, ok := safeShellCommandTokens(command)
	if !ok {
		return sandbox.CommandPrefixGrant{}, false
	}
	return options.Sandbox.LookupCommandPrefixForSession(toolName, tokens)
}

func firstStringArg(args map[string]any, names ...string) (string, bool) {
	for _, name := range names {
		if raw, ok := args[name].(string); ok {
			value := strings.TrimSpace(raw)
			if value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func requestedPrefixRule(args map[string]any) ([]string, bool) {
	raw, ok := args["prefix_rule"]
	if !ok {
		return nil, false
	}
	switch value := raw.(type) {
	case []string:
		return cleanPrefixRule(value), true
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			part, ok := item.(string)
			if !ok {
				return nil, true
			}
			out = append(out, part)
		}
		return cleanPrefixRule(out), true
	default:
		return nil, true
	}
}

func cleanPrefixRule(prefix []string) []string {
	cleaned := make([]string, 0, len(prefix))
	for _, part := range prefix {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil
		}
		cleaned = append(cleaned, part)
	}
	return cleaned
}

func safeRequestedPrefix(prefix []string, command []string) bool {
	if len(prefix) == 0 || unsafeCommandPrefix(prefix) || len(prefix) > len(command) {
		return false
	}
	for index := range prefix {
		if prefix[index] != command[index] {
			return false
		}
	}
	return true
}

func safeShellCommandTokens(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, false
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) != 1 {
		return nil, false
	}
	stmt := file.Stmts[0]
	if stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown || stmt.Semicolon.IsValid() || len(stmt.Redirs) > 0 {
		return nil, false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) == 0 {
		return nil, false
	}
	tokens := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		if len(word.Parts) != 1 {
			return nil, false
		}
		lit, ok := word.Parts[0].(*syntax.Lit)
		if !ok || unsafeCommandPrefixPart(lit.Value) {
			return nil, false
		}
		tokens = append(tokens, lit.Value)
	}
	if unsafeCommandPrefix(tokens) {
		return nil, false
	}
	return tokens, true
}

func unsafeCommandPrefix(prefix []string) bool {
	if len(prefix) == 0 {
		return true
	}
	for _, part := range prefix {
		if unsafeCommandPrefixPart(part) {
			return true
		}
	}
	for _, banned := range bannedCommandPrefixSuggestions {
		if equalStringSlices(prefix, banned) {
			return true
		}
	}
	if unsafeCommandPrefixLauncher(prefix[0]) {
		return true
	}
	return false
}

func unsafeCommandPrefixPart(part string) bool {
	part = strings.TrimSpace(part)
	return part == "" ||
		strings.ContainsAny(part, "\x00\r\n*?[]{}") ||
		strings.Contains(part, "$(") ||
		strings.Contains(part, "`")
}

func unsafeCommandPrefixLauncher(program string) bool {
	switch program {
	case "bash", "sh", "zsh", "/bin/bash", "/bin/zsh",
		"pwsh", "powershell", "powershell.exe",
		"env", "sudo", "osascript":
		return true
	default:
		return false
	}
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
