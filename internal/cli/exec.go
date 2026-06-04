package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
)

const (
	exitSuccess  = 0
	exitCrash    = 1
	exitUsage    = 2
	exitProvider = 3
)

type execOutputFormat string

const (
	execOutputText execOutputFormat = "text"
	execOutputJSON execOutputFormat = "json"
)

type execOptions struct {
	promptParts           []string
	file                  string
	model                 string
	maxTurns              int
	cwd                   string
	outputFormat          execOutputFormat
	skipPermissionsUnsafe bool
}

type execUsageError struct {
	message string
}

func (err execUsageError) Error() string {
	return err.message
}

func runExec(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseExecArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeExecHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	workspaceRoot, err := resolveWorkspaceRoot(options.cwd, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	prompt, err := resolveExecPrompt(options, workspaceRoot)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	overrides := config.Overrides{}
	if options.model != "" {
		overrides.Provider.Model = options.model
	}
	if options.maxTurns > 0 {
		overrides.MaxTurns = options.maxTurns
	}
	resolved, err := deps.resolveConfig(workspaceRoot, overrides)
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}
	if resolved.Provider == (config.ProviderProfile{}) {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", "No provider configured. Set OPENAI_MODEL/OPENAI_API_KEY or add .zero/config.json.")
	}

	provider, err := buildProvider(resolved, deps)
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}

	permissionMode := agent.PermissionModeAuto
	if options.skipPermissionsUnsafe {
		permissionMode = agent.PermissionModeUnsafe
	}

	registry := newCoreRegistry(workspaceRoot)
	writer := execEventWriter{
		stdout:       stdout,
		stderr:       stderr,
		format:       options.outputFormat,
		streamedText: &strings.Builder{},
	}
	writer.runStart(workspaceRoot, resolved.Provider, permissionMode)
	if writer.err != nil {
		return exitCrash
	}
	if options.skipPermissionsUnsafe {
		writer.warning("Unsafe permissions are active for this run because --skip-permissions-unsafe was passed.")
		if writer.err != nil {
			return exitCrash
		}
	}

	result, err := agent.Run(context.Background(), prompt, provider, agent.Options{
		MaxTurns:       resolved.MaxTurns,
		Registry:       registry,
		PermissionMode: permissionMode,
		OnText:         writer.text,
		OnToolCall:     writer.toolCall,
		OnToolResult:   writer.toolResult,
		OnUsage:        writer.usage,
	})
	if writer.err != nil {
		return exitCrash
	}
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}

	writer.final(result.FinalAnswer)
	if writer.err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parseExecArgs(args []string) (execOptions, bool, error) {
	options := execOptions{outputFormat: execOutputText}
	if len(args) == 0 {
		return options, false, execUsageError{"Prompt required. Use `zero exec \"prompt\"` or `zero exec --file prompt.txt`."}
	}

	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--skip-permissions-unsafe":
			options.skipPermissionsUnsafe = true
		case arg == "-f" || arg == "--file":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.file = value
			index = next
		case strings.HasPrefix(arg, "--file="):
			options.file = strings.TrimSpace(strings.TrimPrefix(arg, "--file="))
		case arg == "-m" || arg == "--model":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.model = value
			index = next
		case strings.HasPrefix(arg, "--model="):
			options.model = strings.TrimSpace(strings.TrimPrefix(arg, "--model="))
		case arg == "--max-turns":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			maxTurns, err := parseExecMaxTurns(value)
			if err != nil {
				return options, false, err
			}
			options.maxTurns = maxTurns
			index = next
		case strings.HasPrefix(arg, "--max-turns="):
			maxTurns, err := parseExecMaxTurns(strings.TrimSpace(strings.TrimPrefix(arg, "--max-turns=")))
			if err != nil {
				return options, false, err
			}
			options.maxTurns = maxTurns
		case arg == "-C" || arg == "--cwd":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.cwd = value
			index = next
		case strings.HasPrefix(arg, "--cwd="):
			options.cwd = strings.TrimSpace(strings.TrimPrefix(arg, "--cwd="))
		case arg == "-o" || arg == "--output-format":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			format, err := parseExecOutputFormat(value)
			if err != nil {
				return options, false, err
			}
			options.outputFormat = format
			index = next
		case strings.HasPrefix(arg, "--output-format="):
			format, err := parseExecOutputFormat(strings.TrimSpace(strings.TrimPrefix(arg, "--output-format=")))
			if err != nil {
				return options, false, err
			}
			options.outputFormat = format
		case arg == "--prompt":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.promptParts = append(options.promptParts, value)
			index = next
		case strings.HasPrefix(arg, "--prompt="):
			options.promptParts = append(options.promptParts, strings.TrimSpace(strings.TrimPrefix(arg, "--prompt=")))
		case arg == "--":
			options.promptParts = append(options.promptParts, args[index+1:]...)
			index = len(args)
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown exec flag %q", arg)}
		default:
			options.promptParts = append(options.promptParts, arg)
		}
	}

	if options.file == "" && strings.TrimSpace(strings.Join(options.promptParts, " ")) == "" {
		return options, false, execUsageError{"Prompt required. Use `zero exec \"prompt\"` or `zero exec --file prompt.txt`."}
	}
	return options, false, nil
}

func parseExecMaxTurns(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, execUsageError{"--max-turns requires a value"}
	}
	maxTurns, err := strconv.Atoi(trimmed)
	if err != nil || maxTurns <= 0 {
		return 0, execUsageError{fmt.Sprintf("invalid --max-turns %q. Expected a positive integer.", value)}
	}
	return maxTurns, nil
}

func nextFlagValue(args []string, index int, flag string) (string, int, error) {
	if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
		return "", index, execUsageError{fmt.Sprintf("%s requires a value", flag)}
	}
	return strings.TrimSpace(args[index+1]), index + 1, nil
}

func parseExecOutputFormat(value string) (execOutputFormat, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(execOutputText):
		return execOutputText, nil
	case string(execOutputJSON):
		return execOutputJSON, nil
	default:
		return "", execUsageError{fmt.Sprintf("invalid output format %q. Expected text or json.", value)}
	}
}

func resolveWorkspaceRoot(cwd string, deps appDeps) (string, error) {
	current, err := deps.getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace: %w", err)
	}

	workspaceRoot := strings.TrimSpace(cwd)
	if workspaceRoot == "" {
		workspaceRoot = current
	} else if !filepath.IsAbs(workspaceRoot) {
		workspaceRoot = filepath.Join(current, workspaceRoot)
	}
	workspaceRoot = filepath.Clean(workspaceRoot)

	info, err := os.Stat(workspaceRoot)
	if err != nil || !info.IsDir() {
		return "", execUsageError{fmt.Sprintf("cwd must be an existing directory: %s", workspaceRoot)}
	}
	return workspaceRoot, nil
}

func resolveExecPrompt(options execOptions, workspaceRoot string) (string, error) {
	parts := []string{}
	inlinePrompt := strings.TrimSpace(strings.Join(options.promptParts, " "))
	if inlinePrompt != "" {
		parts = append(parts, inlinePrompt)
	}

	if options.file != "" {
		promptPath := options.file
		if !filepath.IsAbs(promptPath) {
			promptPath = filepath.Join(workspaceRoot, promptPath)
		}
		data, err := os.ReadFile(promptPath)
		if err != nil {
			return "", execUsageError{fmt.Sprintf("prompt file not found: %s", promptPath)}
		}
		filePrompt := strings.TrimSpace(string(data))
		if filePrompt == "" {
			return "", execUsageError{fmt.Sprintf("prompt file is empty: %s", promptPath)}
		}
		parts = append(parts, filePrompt)
	}

	prompt := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if prompt == "" {
		return "", execUsageError{"Prompt required. Use `zero exec \"prompt\"` or `zero exec --file prompt.txt`."}
	}
	return prompt, nil
}

func writeExecUsageError(stderr io.Writer, message string) int {
	if _, err := fmt.Fprintf(stderr, "[zero] %s\n", message); err != nil {
		return exitCrash
	}
	return exitUsage
}

func writeExecProviderError(stdout io.Writer, stderr io.Writer, format execOutputFormat, code string, message string) int {
	if format == execOutputJSON {
		if err := writeJSONLine(stdout, map[string]any{
			"type":    "error",
			"code":    code,
			"message": message,
		}); err != nil {
			return exitCrash
		}
		if err := writeJSONLine(stdout, map[string]any{
			"type":      "done",
			"exit_code": exitProvider,
		}); err != nil {
			return exitCrash
		}
		return exitProvider
	}
	if _, err := fmt.Fprintf(stderr, "[zero] %s\n", message); err != nil {
		return exitCrash
	}
	return exitProvider
}

type execEventWriter struct {
	stdout       io.Writer
	stderr       io.Writer
	format       execOutputFormat
	streamedText *strings.Builder
	err          error
}

func (writer *execEventWriter) runStart(cwd string, provider config.ProviderProfile, permissionMode agent.PermissionMode) {
	if writer.format != execOutputJSON {
		return
	}
	writer.writeJSON(map[string]any{
		"type":            "run_start",
		"cwd":             cwd,
		"provider":        provider.Name,
		"model":           provider.Model,
		"permission_mode": string(permissionMode),
	})
}

func (writer *execEventWriter) warning(message string) {
	if writer.format == execOutputJSON {
		writer.writeJSON(map[string]any{"type": "warning", "message": message})
		return
	}
	writer.writeStderr("[zero] WARNING: " + message + "\n")
}

func (writer *execEventWriter) text(delta string) {
	writer.streamedText.WriteString(delta)
	if writer.format == execOutputJSON {
		writer.writeJSON(map[string]any{"type": "text", "delta": delta})
		return
	}
	writer.writeStdout(delta)
}

func (writer *execEventWriter) toolCall(call agent.ToolCall) {
	if writer.format == execOutputJSON {
		writer.writeJSON(map[string]any{
			"type":      "tool_call",
			"id":        call.ID,
			"name":      call.Name,
			"arguments": call.Arguments,
		})
		return
	}
	writer.writeStderr("[tool] " + call.Name + "\n")
}

func (writer *execEventWriter) toolResult(result agent.ToolResult) {
	if writer.format == execOutputJSON {
		writer.writeJSON(map[string]any{
			"type":         "tool_result",
			"tool_call_id": result.ToolCallID,
			"name":         result.Name,
			"status":       string(result.Status),
			"output":       result.Output,
		})
		return
	}
	writer.writeStderr("[result] " + truncateForStatus(result.Output) + "\n")
}

func (writer *execEventWriter) usage(usage agent.Usage) {
	if writer.format != execOutputJSON {
		return
	}
	writer.writeJSON(map[string]any{
		"type":              "usage",
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens(),
	})
}

func (writer *execEventWriter) final(answer string) {
	if writer.format == execOutputJSON {
		writer.writeJSON(map[string]any{"type": "final", "text": answer})
		writer.writeJSON(map[string]any{"type": "done", "exit_code": exitSuccess})
		return
	}

	if writer.streamedText.Len() == 0 && answer != "" {
		writer.writeStdout(answer)
		writer.streamedText.WriteString(answer)
	}
	if writer.streamedText.Len() > 0 && !strings.HasSuffix(writer.streamedText.String(), "\n") {
		writer.writeStdout("\n")
	}
}

func (writer *execEventWriter) writeJSON(payload map[string]any) {
	if writer.err != nil {
		return
	}
	writer.err = writeJSONLine(writer.stdout, payload)
}

func (writer *execEventWriter) writeStdout(value string) {
	if writer.err != nil {
		return
	}
	_, writer.err = io.WriteString(writer.stdout, value)
}

func (writer *execEventWriter) writeStderr(value string) {
	if writer.err != nil {
		return
	}
	_, writer.err = io.WriteString(writer.stderr, value)
}

func writeJSONLine(w io.Writer, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func truncateForStatus(value string) string {
	compact := strings.Join(strings.Fields(value), " ")
	if len(compact) > 200 {
		return compact[:200] + "..."
	}
	return compact
}
