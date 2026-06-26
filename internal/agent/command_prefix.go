package agent

import (
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
	"mvdan.cc/sh/v3/syntax"
)

func proposedCommandPrefix(toolName string, args map[string]any) []string {
	if !isShellCommandTool(toolName) {
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
	if !sandbox.ValidCommandPrefix(tokens) {
		return nil
	}
	return append([]string(nil), tokens...)
}

func matchCommandPrefix(toolName string, args map[string]any, options Options) (sandbox.CommandPrefixGrant, bool, bool) {
	if !isShellCommandTool(toolName) || options.Sandbox == nil {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	if shellCommandAdditionalPermissionsRequested(args) {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	command, ok := firstStringArg(args, "command", "cmd", "script", "shell")
	if !ok {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	tokens, ok := safeShellCommandTokens(command)
	if !ok {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	if grant, ok := options.Sandbox.LookupCommandPrefix(toolName, tokens); ok {
		return grant, true, false
	}
	if grant, ok := options.Sandbox.LookupCommandPrefixForSession(toolName, tokens); ok {
		return grant, true, true
	}
	return sandbox.CommandPrefixGrant{}, false, false
}

func shellExecutionArgsForApproval(toolName string, args map[string]any, action PermissionDecisionAction, options Options) map[string]any {
	if !isShellCommandTool(toolName) || !shellPrefixApprovalBypassesSandbox(action) {
		return args
	}
	if options.Sandbox == nil || !options.Sandbox.UnsandboxedExecutionAllowed() {
		return args
	}
	if shellCommandAdditionalPermissionsRequested(args) || shellCommandRequiresEscalated(args) {
		return args
	}
	planned := cloneArgs(args)
	if planned == nil {
		planned = map[string]any{}
	}
	planned["sandbox_permissions"] = string(tools.SandboxPermissionsRequireEscalated)
	return planned
}

func shellPrefixApprovalBypassesSandbox(action PermissionDecisionAction) bool {
	return action == PermissionDecisionAllowPrefix || action == PermissionDecisionAlwaysAllowPrefix
}

func shellCommandRequiresEscalated(args map[string]any) bool {
	raw, ok := args["sandbox_permissions"]
	if !ok || raw == nil {
		return false
	}
	value, ok := raw.(string)
	if !ok {
		value = fmt.Sprint(raw)
	}
	return strings.TrimSpace(value) == string(tools.SandboxPermissionsRequireEscalated)
}

func isShellCommandTool(toolName string) bool {
	return toolName == "bash" || toolName == "exec_command"
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
	if len(prefix) == 0 || !sandbox.ValidCommandPrefix(prefix) || len(prefix) > len(command) {
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
		if !ok {
			return nil, false
		}
		tokens = append(tokens, lit.Value)
	}
	if !sandbox.ValidCommandPrefix(tokens) {
		return nil, false
	}
	return tokens, true
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
