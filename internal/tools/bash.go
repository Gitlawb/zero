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

	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/secrets"
)

const defaultBashTimeoutMS = 120000
const maxBashTimeoutMS = 600000

type bashTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewBashTool(workspaceRoot string) Tool {
	return NewScopedBashTool(workspaceRoot, nil)
}

func NewScopedBashTool(workspaceRoot string, scope PathScope) Tool {
	shellGuidance := shellGuidanceForGOOS(runtime.GOOS)
	return bashTool{
		baseTool: baseTool{
			name:        "bash",
			description: "Execute a shell command inside the workspace (or an explicitly granted extra directory) after permission is granted. " + shellGuidance,
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"command":    {Type: "string", Description: "Shell command to execute using the host shell. " + shellGuidance},
					"cwd":        {Type: "string", Description: "Directory to run the command in. Relative paths stay in the workspace; use an absolute path to run in a granted extra directory. Defaults to workspace root. Prefer cwd over cd when changing directories.", Default: "."},
					"timeout_ms": {Type: "integer", Description: "Command timeout in milliseconds.", Default: defaultBashTimeoutMS, Minimum: intPtr(1), Maximum: intPtr(maxBashTimeoutMS)},
				},
				Required:             []string{"command"},
				AdditionalProperties: false,
			},
			safety: promptSafety(SideEffectShell, "Shell commands can read, write, or execute programs."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool bashTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.run(ctx, args, nil)
}

func (tool bashTool) RunWithSandbox(ctx context.Context, args map[string]any, engine *zeroSandbox.Engine) Result {
	return tool.run(ctx, args, engine)
}

func (tool bashTool) run(ctx context.Context, args map[string]any, engine *zeroSandbox.Engine) Result {
	commandText, err := aliasedStringArg(args, []string{"command", "cmd", "script", "shell"}, "", true, false)
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
	if issue := detectShellCommandIssue(commandText, runtime.GOOS); issue != nil {
		return shellIssueBlockResult(*issue)
	}

	// Pre-execution safety: refuse interactive commands (editors, pagers, REPLs,
	// remote shells, etc.) that would hang the non-interactive agent until the
	// timeout fires. This runs before the command is built or launched.
	if interactive := zeroSandbox.DetectInteractiveCommand(commandText, runtime.GOOS); interactive.Interactive {
		return interactiveBlockResult(interactive)
	}

	absoluteCwd, relativeCwd, err := resolveScopedPath(tool.workspaceRoot, tool.scope, cwd)
	if err != nil {
		return errorResult("Error running bash: " + err.Error())
	}

	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	meta := map[string]string{
		"cwd":        relativeCwd,
		"timeout_ms": strconv.Itoa(timeoutMS),
	}
	command, plan, err := buildBashCommand(commandCtx, commandText, absoluteCwd, engine)
	if err != nil {
		meta["exit_code"] = "-1"
		return Result{
			Status: StatusError,
			Output: "Error preparing sandboxed bash command: " + err.Error(),
			Meta:   meta,
		}
	}
	addSandboxMeta(meta, plan)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	// Kill the shell as a process group on timeout and bound the post-kill I/O
	// wait, so a backgrounded child cannot outlive the command or hang Run().
	hardenProcessLifetime(command)

	err = command.Run()
	exitCode := commandExitCode(err)
	meta["exit_code"] = strconv.Itoa(exitCode)

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
			Output: formatBashOutputWithShellHint(commandText, stdout.String(), stderr.String(), exitCode, meta),
			Meta:   meta,
		}
	}

	return Result{
		Status: StatusOK,
		Output: formatBashOutput(stdout.String(), stderr.String(), exitCode),
		Meta:   meta,
	}
}

func shellIssueBlockResult(issue shellIssue) Result {
	return Result{
		Status: StatusError,
		Output: appendShellIssueHint("", issue),
		Meta: map[string]string{
			"exit_code":   "-1",
			"shell_issue": issue.Kind,
		},
		Display: Display{
			Summary: issue.Message,
			Kind:    "shell",
		},
	}
}

func buildBashCommand(ctx context.Context, commandText string, absoluteCwd string, engine *zeroSandbox.Engine) (*exec.Cmd, zeroSandbox.CommandPlan, error) {
	spec := zeroSandbox.CommandSpec{
		Name: shellExecutable(),
		Args: shellArguments(commandText),
		Dir:  absoluteCwd,
	}
	if engine != nil {
		return engine.CommandContext(ctx, spec)
	}
	plan := zeroSandbox.CommandPlan{
		Backend: zeroSandbox.Backend{
			Name:    zeroSandbox.BackendPolicyOnly,
			Message: "sandbox engine not provided",
		},
		Wrapped: false,
		Name:    spec.Name,
		Args:    spec.Args,
		Dir:     spec.Dir,
	}
	command := exec.CommandContext(ctx, spec.Name, spec.Args...)
	command.Dir = spec.Dir
	return command, plan, nil
}

func addSandboxMeta(meta map[string]string, plan zeroSandbox.CommandPlan) {
	if plan.Backend.Name == "" {
		return
	}
	meta["sandbox_backend"] = string(plan.Backend.Name)
	meta["sandbox_wrapped"] = strconv.FormatBool(plan.Wrapped)
	if plan.Backend.Message != "" {
		meta["sandbox_message"] = plan.Backend.Message
	}
	if plan.SandboxDir != "" {
		meta["sandbox_cwd"] = plan.SandboxDir
	}
}

// interactiveBlockResult builds the structured tool Result returned when a
// command is refused before execution because it would hang the agent. The
// violation is surfaced both in Output (clearly delimited) and in Meta/Display
// so downstream consumers and the TUI can render it consistently.
func interactiveBlockResult(detection zeroSandbox.InteractiveCommandResult) Result {
	message := fmt.Sprintf(
		"Error: Blocked interactive command %q before execution: %s. This would hang the non-interactive agent.\nSuggestion: %s",
		detection.Command, detection.Reason, detection.Suggestion,
	)
	return Result{
		Status: StatusError,
		Output: message,
		Meta: map[string]string{
			"exit_code":    "-1",
			"safety_block": "interactive_command",
			"safety_cmd":   detection.Command,
		},
		Display: Display{
			Summary: "Blocked interactive command: " + detection.Command,
			Kind:    "shell",
		},
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
	// Redact high-confidence secrets a command may have printed, so they are not
	// echoed back into the model context (additive to the configured-key scrub
	// done at the registry boundary).
	stdout, outFindings := secrets.Redact(stdout)
	stderr, errFindings := secrets.Redact(stderr)
	if stdout != "" {
		parts = append(parts, "stdout:\n"+stdout)
	}
	if stderr != "" {
		parts = append(parts, "stderr:\n"+stderr)
	}
	if exitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit_code: %d", exitCode))
	}
	if n := len(outFindings) + len(errFindings); n > 0 {
		parts = append(parts, fmt.Sprintf("[zero] redacted %d likely secret(s) from this output before showing it.", n))
	}
	if len(parts) == 0 {
		return "Command completed with no output."
	}
	return strings.Join(parts, "\n")
}

func formatBashOutputWithShellHint(command string, stdout string, stderr string, exitCode int, meta map[string]string) string {
	output := formatBashOutput(stdout, stderr, exitCode)
	if issue := detectShellOutputIssue(command, stdout+"\n"+stderr, runtime.GOOS); issue != nil {
		meta["shell_issue"] = issue.Kind
		output = appendShellIssueHint(output, *issue)
	}
	return output
}
