package agenteval

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

type AgentRunInput struct {
	TaskID        string
	Prompt        string
	WorkspacePath string
}

type AgentRunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Error    string
}

type AgentRunner interface {
	Run(context.Context, AgentRunInput) AgentRunResult
}

type AgentRunnerFunc func(context.Context, AgentRunInput) AgentRunResult

func (fn AgentRunnerFunc) Run(ctx context.Context, input AgentRunInput) AgentRunResult {
	return fn(ctx, input)
}

type CommandAgentRunner struct {
	Command []string
}

func (runner CommandAgentRunner) Run(ctx context.Context, input AgentRunInput) AgentRunResult {
	result := AgentRunResult{ExitCode: -1}
	if len(runner.Command) == 0 || strings.TrimSpace(runner.Command[0]) == "" {
		result.Error = "agent command is required"
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	command := expandAgentCommand(runner.Command, input)
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = input.WorkspacePath
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	if err == nil {
		result.ExitCode = 0
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Error = ctxErr.Error()
		return result
	}
	result.Error = err.Error()
	return result
}

func expandAgentCommand(command []string, input AgentRunInput) []string {
	expanded := make([]string, len(command))
	for i, arg := range command {
		arg = strings.ReplaceAll(arg, "{prompt}", input.Prompt)
		arg = strings.ReplaceAll(arg, "{workspace}", input.WorkspacePath)
		arg = strings.ReplaceAll(arg, "{task_id}", input.TaskID)
		expanded[i] = arg
	}
	return expanded
}
