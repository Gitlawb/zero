package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providers"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/streamjson"
	"github.com/Gitlawb/zero/internal/worktrees"
)

const (
	exitSuccess  = 0
	exitCrash    = 1
	exitUsage    = 2
	exitProvider = 3
)

type execOutputFormat string
type execInputFormat string

const (
	execOutputText       execOutputFormat = "text"
	execOutputJSON       execOutputFormat = "json"
	execOutputStreamJSON execOutputFormat = "stream-json"
	execInputText        execInputFormat  = "text"
	execInputStreamJSON  execInputFormat  = "stream-json"
)

type execOptions struct {
	promptParts           []string
	file                  string
	model                 string
	maxTurns              int
	cwd                   string
	inputFormat           execInputFormat
	outputFormat          execOutputFormat
	autonomy              string
	enabledTools          []string
	disabledTools         []string
	listTools             bool
	resume                string
	resumeLatest          bool
	fork                  string
	worktree              bool
	worktreeName          string
	worktreeDir           string
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
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	if help {
		if err := writeExecHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	workspaceRoot, err := resolveWorkspaceRoot(options.cwd, deps)
	if err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	if options.worktree {
		preparedWorktree, err := deps.prepareWorktree(context.Background(), worktrees.Options{
			Cwd:     workspaceRoot,
			Name:    options.worktreeName,
			BaseDir: options.worktreeDir,
			Now:     deps.now,
		})
		if err != nil {
			return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
		}
		workspaceRoot = preparedWorktree.Path
	}

	registry := newCoreRegistry(workspaceRoot)
	permissionMode, err := resolveExecPermissionMode(options)
	if err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	mcpRuntime, err := registerMCPToolsForWorkspace(context.Background(), workspaceRoot, registry, deps, execMCPAutonomy(options))
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "mcp_error", err.Error())
	}
	defer closeMCPRuntime(stderr, mcpRuntime)
	if err := validateExecToolFilters(options, registry); err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	if options.listTools {
		if options.outputFormat == execOutputStreamJSON {
			return writeExecStreamJSONFinal(stdout, workspaceRoot, execRunMetadata{}, permissionMode, formatExecToolList(registry, options, permissionMode), exitSuccess)
		}
		if err := writeExecToolList(stdout, registry, options, permissionMode); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if err := preflightExecSession(options); err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}

	prompt, err := resolveExecPrompt(options, workspaceRoot, deps.stdin)
	if err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
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
	runMetadata, err := resolveExecRunMetadata(resolved.Provider)
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}

	preparedSession := sessions.PreparedExec{}
	agentPrompt := prompt
	if shouldUseExecSession(options) {
		preparedSession, err = sessions.PrepareExec(sessions.PrepareExecOptions{
			Title:        createSessionTitle(prompt),
			Cwd:          workspaceRoot,
			ModelID:      resolved.Provider.Model,
			Provider:     runMetadata.Provider,
			Resume:       options.resume,
			ResumeLatest: options.resumeLatest,
			Fork:         options.fork,
		})
		if err != nil {
			return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
		}
		agentPrompt = sessions.FormatExecPrompt(prompt, preparedSession)
	}
	runID, err := streamjson.CreateRunID(time.Now())
	if err != nil {
		return writeAppError(stderr, "failed to create run id: "+err.Error(), exitCrash)
	}
	writer := execEventWriter{
		stdout:       stdout,
		stderr:       stderr,
		format:       options.outputFormat,
		runID:        runID,
		sessionID:    preparedSession.Session.SessionID,
		streamedText: &strings.Builder{},
	}
	writer.runStart(workspaceRoot, runMetadata, permissionMode)
	if writer.err != nil {
		return exitCrash
	}
	if options.skipPermissionsUnsafe {
		writer.warning("Unsafe permissions are active for this run because --skip-permissions-unsafe was passed.")
		if writer.err != nil {
			return exitCrash
		}
	}

	sessionRecorder := execSessionRecorder{prepared: preparedSession}
	sessionRecorder.append(sessions.EventMessage, map[string]any{
		"role":    "user",
		"content": prompt,
	})

	result, err := agent.Run(context.Background(), agentPrompt, provider, agent.Options{
		MaxTurns:       resolved.MaxTurns,
		Registry:       registry,
		PermissionMode: permissionMode,
		EnabledTools:   options.enabledTools,
		DisabledTools:  options.disabledTools,
		OnText:         writer.text,
		OnToolCall: func(call agent.ToolCall) {
			writer.toolCall(call, registry)
			sessionRecorder.append(sessions.EventToolCall, map[string]any{
				"id":        call.ID,
				"name":      call.Name,
				"arguments": call.Arguments,
			})
		},
		OnToolResult: func(result agent.ToolResult) {
			writer.toolResult(result)
			sessionRecorder.append(sessions.EventToolResult, map[string]any{
				"toolCallId": result.ToolCallID,
				"name":       result.Name,
				"status":     string(result.Status),
				"output":     result.Output,
			})
		},
		OnUsage: func(usage agent.Usage) {
			writer.usage(usage)
			sessionRecorder.append(sessions.EventUsage, map[string]any{
				"promptTokens":     usage.EffectiveInputTokens(),
				"completionTokens": usage.EffectiveOutputTokens(),
				"totalTokens":      usage.TotalTokens(),
			})
		},
	})
	if writer.err != nil {
		return exitCrash
	}
	if err != nil {
		sessionRecorder.append(sessions.EventError, map[string]any{"message": err.Error()})
		if options.outputFormat == execOutputStreamJSON {
			writer.errorEvent("provider_error", err.Error(), false)
			writer.runEnd("error", exitProvider)
			if writer.err != nil {
				return exitCrash
			}
			return exitProvider
		}
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}
	sessionRecorder.append(sessions.EventMessage, map[string]any{
		"role":    "assistant",
		"content": result.FinalAnswer,
	})

	writer.final(result.FinalAnswer)
	writer.runEnd("success", exitSuccess)
	if writer.err != nil {
		return exitCrash
	}
	return exitSuccess
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

func resolveExecPrompt(options execOptions, workspaceRoot string, stdin io.Reader) (string, error) {
	if options.inputFormat == execInputStreamJSON {
		input := ""
		if options.file != "" {
			promptPath := options.file
			if !filepath.IsAbs(promptPath) {
				promptPath = filepath.Join(workspaceRoot, promptPath)
			}
			data, err := os.ReadFile(promptPath)
			if err != nil {
				return "", execUsageError{fmt.Sprintf("prompt file not found: %s", promptPath)}
			}
			input = string(data)
		} else {
			data, err := io.ReadAll(stdin)
			if err != nil {
				return "", execUsageError{fmt.Sprintf("failed to read stream-json input: %v", err)}
			}
			input = string(data)
		}
		prompt, err := streamjson.ParsePrompt(input)
		if err != nil {
			return "", execUsageError{err.Error()}
		}
		return prompt, nil
	}

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

func writeExecFormatUsageError(stdout io.Writer, stderr io.Writer, format execOutputFormat, message string) int {
	if format == execOutputStreamJSON {
		return writeStreamJSONError(stdout, "usage_error", message, false, exitUsage)
	}
	return writeExecUsageError(stderr, message)
}

func writeExecProviderError(stdout io.Writer, stderr io.Writer, format execOutputFormat, code string, message string) int {
	if format == execOutputStreamJSON {
		return writeStreamJSONError(stdout, code, message, false, exitProvider)
	}
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

func resolveExecRunMetadata(profile config.ProviderProfile) (execRunMetadata, error) {
	metadata, err := providers.ResolveRuntimeMetadata(profile, providers.Options{})
	if err != nil {
		return execRunMetadata{}, err
	}
	provider := strings.TrimSpace(string(metadata.ProviderKind))
	if provider == "" {
		provider = strings.TrimSpace(profile.Name)
	}
	apiModel := strings.TrimSpace(metadata.APIModel)
	if apiModel == "" {
		apiModel = strings.TrimSpace(profile.Model)
	}
	return execRunMetadata{
		Provider: provider,
		Model:    strings.TrimSpace(profile.Model),
		APIModel: apiModel,
	}, nil
}

func writeExecStreamJSONFinal(stdout io.Writer, cwd string, metadata execRunMetadata, permissionMode agent.PermissionMode, text string, exitCode int) int {
	runID, err := streamjson.CreateRunID(time.Now())
	if err != nil {
		return exitCrash
	}
	writer := execEventWriter{
		stdout:       stdout,
		format:       execOutputStreamJSON,
		runID:        runID,
		streamedText: &strings.Builder{},
	}
	writer.runStart(cwd, metadata, permissionMode)
	writer.final(text)
	writer.runEnd("success", exitCode)
	if writer.err != nil {
		return exitCrash
	}
	return exitCode
}
