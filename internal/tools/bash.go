package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultBashTimeoutMS = 120000
const maxBashTimeoutMS = 600000

type bashTool struct {
	baseTool
	workspaceRoot string
}

func NewBashTool(workspaceRoot string) Tool {
	return bashTool{
		baseTool: baseTool{
			name:        "bash",
			description: "Execute a shell command inside the workspace after permission is granted.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"command":    {Type: "string", Description: "Shell command to execute."},
					"cwd":        {Type: "string", Description: "Workspace directory to run the command in. Defaults to workspace root.", Default: "."},
					"timeout_ms": {Type: "integer", Description: "Command timeout in milliseconds.", Default: defaultBashTimeoutMS, Minimum: intPtr(1), Maximum: intPtr(maxBashTimeoutMS)},
				},
				Required:             []string{"command"},
				AdditionalProperties: false,
			},
			safety: promptSafety(SideEffectShell, "Shell commands can read, write, or execute programs."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
	}
}

func (tool bashTool) Run(ctx context.Context, args map[string]any) Result {
	commandText, err := stringArg(args, "command", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for bash: " + err.Error())
	}
	cwd, err := stringArg(args, "cwd", ".", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for bash: " + err.Error())
	}
	timeoutMS, err := intArg(args, "timeout_ms", defaultBashTimeoutMS, 1, maxBashTimeoutMS)
	if err != nil {
		return errorResult("Error: Invalid arguments for bash: " + err.Error())
	}

	absoluteCwd, relativeCwd, err := resolveWorkspacePath(tool.workspaceRoot, cwd)
	if err != nil {
		return errorResult("Error running bash: " + err.Error())
	}

	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	command := exec.CommandContext(commandCtx, shellExecutable(), shellArguments(commandText)...)
	command.Dir = absoluteCwd

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err = command.Run()
	exitCode := commandExitCode(err)
	meta := map[string]string{
		"cwd":        relativeCwd,
		"exit_code":  strconv.Itoa(exitCode),
		"timeout_ms": strconv.Itoa(timeoutMS),
	}

	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return Result{
			Status: StatusError,
			Output: fmt.Sprintf("Error: Command timed out after %dms.", timeoutMS),
			Meta:   meta,
		}
	}
	if err != nil {
		if exitCode < 0 {
			return Result{
				Status: StatusError,
				Output: "Error executing command: " + err.Error(),
				Meta:   meta,
			}
		}
		return Result{
			Status: StatusError,
			Output: formatBashOutput(stdout.String(), stderr.String(), exitCode),
			Meta:   meta,
		}
	}

	return Result{
		Status: StatusOK,
		Output: formatBashOutput(stdout.String(), stderr.String(), exitCode),
		Meta:   meta,
	}
}

func shellExecutable() string {
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "/bin/sh"
}

func shellArguments(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"/d", "/s", "/c", command}
	}
	return []string{"-c", command}
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

func formatBashOutput(stdout string, stderr string, exitCode int) string {
	parts := []string{}
	stdout = strings.TrimRight(stdout, "\r\n")
	stderr = strings.TrimRight(stderr, "\r\n")
	if stdout != "" {
		parts = append(parts, "stdout:\n"+stdout)
	}
	if stderr != "" {
		parts = append(parts, "stderr:\n"+stderr)
	}
	if exitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit_code: %d", exitCode))
	}
	if len(parts) == 0 {
		return "Command completed with no output."
	}
	return strings.Join(parts, "\n")
}
