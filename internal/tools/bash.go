package tools

import (
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
					"command":             {Type: "string", Description: "Shell command to execute using the host shell. " + shellGuidance},
					"cwd":                 {Type: "string", Description: "Directory to run the command in. Relative paths stay in the workspace; use an absolute path to run in a granted extra directory. Defaults to workspace root. Prefer cwd over cd when changing directories.", Default: "."},
					"timeout_ms":          {Type: "integer", Description: "Command timeout in milliseconds.", Default: defaultBashTimeoutMS, Minimum: intPtr(1), Maximum: intPtr(maxBashTimeoutMS)},
					"sandbox_permissions": {Type: "string", Enum: []string{string(SandboxPermissionsUseDefault), string(SandboxPermissionsWithAdditionalPermissions), string(SandboxPermissionsRequireEscalated)}, Description: "Per-command sandbox override. Defaults to `use_default`; use `with_additional_permissions` with `additional_permissions` for sandboxed file/network access, or `require_escalated` only when the command must run outside the sandbox, such as host/global process, socket, service, or desktop state hidden by sandbox namespaces.", Default: string(SandboxPermissionsUseDefault)},
					"additional_permissions": {
						Type:        "object",
						Description: "Sandboxed filesystem or network access for this command; only with `sandbox_permissions: \"with_additional_permissions\"`.",
						Properties:  additionalPermissionsProperties(),
					},
					"justification": {Type: "string", Description: "User-facing approval question for `require_escalated`; omit otherwise."},
					"prefix_rule":   {Type: "array", Items: &PropertySchema{Type: "string"}, Description: "Reusable approval prefix for this command, only with `sandbox_permissions: \"require_escalated\"`; keep it narrow, for example [\"git\", \"pull\"]."},
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
	sandboxPermissions, err := sandboxPermissionsArg(args)
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
	commandEngine := commandEngineForSandboxPermissions(engine, sandboxPermissions)
	if commandEngine == nil && sandboxPermissions == SandboxPermissionsRequireEscalated {
		meta["sandbox_permissions"] = string(SandboxPermissionsRequireEscalated)
	}
	command, plan, err := buildBashCommand(commandCtx, commandText, absoluteCwd, commandEngine)
	if err != nil {
		meta["exit_code"] = "-1"
		return Result{
			Status: StatusError,
			Output: "Error preparing sandboxed bash command: " + err.Error(),
			Meta:   meta,
		}
	}
	defer plan.Cleanup()
	addSandboxMeta(meta, plan)

	// Bound the capture so a command with runaway output (`cat huge.log`, `yes`)
	// can't grow Zero's memory before truncation: only the head+tail each stream
	// will ever surface to the model are retained, the middle is discarded as it
	// streams, and the true size is counted for the truncation marker.
	stdout := newBoundedBuffer(bashOutputBudgetBytes, bashOutputBudgetBytes)
	stderr := newBoundedBuffer(bashOutputBudgetBytes, bashOutputBudgetBytes)
	command.Stdout = stdout
	command.Stderr = stderr

	// Kill the shell as a process group on timeout and bound the post-kill I/O
	// wait, so a backgrounded child cannot outlive the command or hang Run().
	hardenProcessLifetime(command)

	// Capture sandbox denials when the plan opted in (macOS + Policy.MonitorDenials).
	// A no-op when MonitorTag is empty, so the default path is unchanged.
	monitor := zeroSandbox.StartDenialMonitor(context.Background(), plan.MonitorTag)
	err = command.Run()
	exitCode := commandExitCode(err)
	meta["exit_code"] = strconv.Itoa(exitCode)
	stdoutText := stdout.retained()
	stderrRetained := stderr.retained()
	stderrText := appendSandboxBlocks(stderrRetained, monitor.Stop())
	// Sandbox blocks are extra model-visible stderr bytes appended after capture;
	// count them toward the true total so the budget/marker stay accurate.
	stderrTotal := stderr.total + (len(stderrText) - len(stderrRetained))

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
		markLikelySandboxDenial(meta, plan, exitCode, stdoutText, stderrText)
		outText, errText, truncated := budgetBashCapture(stdoutText, stdout.total, stderrText, stderrTotal, meta)
		return Result{
			Status:    StatusError,
			Output:    formatBashOutputWithShellHint(commandText, outText, errText, exitCode, meta),
			Truncated: truncated,
			Meta:      meta,
		}
	}

	markLikelySandboxDenial(meta, plan, exitCode, stdoutText, stderrText)
	outText, errText, truncated := budgetBashCapture(stdoutText, stdout.total, stderrText, stderrTotal, meta)
	if meta[SandboxLikelyDeniedMeta] == "true" {
		return Result{
			Status:    StatusError,
			Output:    formatBashOutputWithShellHint(commandText, outText, errText, exitCode, meta),
			Truncated: truncated,
			Meta:      meta,
		}
	}
	return Result{
		Status:    StatusOK,
		Output:    formatBashOutput(outText, errText, exitCode),
		Truncated: truncated,
		Meta:      meta,
	}
}

func commandEngineForSandboxPermissions(engine *zeroSandbox.Engine, sandboxPermissions SandboxPermissionOverride) *zeroSandbox.Engine {
	if sandboxPermissions == SandboxPermissionsRequireEscalated && (engine == nil || engine.UnsandboxedExecutionAllowed()) {
		return nil
	}
	return engine
}

// appendSandboxBlocks appends a <sandbox_blocks> block listing the denials
// the sandbox log monitor captured, so the model can see what was blocked. With no
// blocks the stderr is returned unchanged.
func appendSandboxBlocks(stderr string, blocks []string) string {
	if len(blocks) == 0 {
		return stderr
	}
	var builder strings.Builder
	builder.WriteString("<sandbox_blocks>\n")
	for _, block := range blocks {
		builder.WriteString(block)
		builder.WriteString("\n")
	}
	builder.WriteString("</sandbox_blocks>")
	if strings.TrimSpace(stderr) == "" {
		return builder.String()
	}
	return stderr + "\n" + builder.String()
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

// buildBashCommand returns the exec.Cmd and the sandbox plan for running
// commandText. On Windows, when the command is not wrapped by the sandbox
// engine (plan.Wrapped == false), it also overrides the child's raw command
// line so commandText reaches cmd.exe unescaped; see
// zeroSandbox.WindowsShellCommandLine for why that matters. The wrapped case
// gets the same treatment inside the sandboxed runner process itself
// (internal/sandbox/windows_process_windows.go), since that command line is
// built there, not here.
func buildBashCommand(ctx context.Context, commandText string, absoluteCwd string, engine *zeroSandbox.Engine) (*exec.Cmd, zeroSandbox.CommandPlan, error) {
	spec := zeroSandbox.CommandSpec{
		Name: shellExecutable(),
		Args: shellArguments(commandText),
		Dir:  absoluteCwd,
	}
	if engine != nil {
		command, plan, err := engine.CommandContext(ctx, spec)
		if err == nil {
			applyWindowsShellCommandLine(command, commandText, plan.Wrapped)
		}
		return command, plan, err
	}
	plan := zeroSandbox.CommandPlan{
		Backend: zeroSandbox.Backend{
			Name:    zeroSandbox.BackendUnavailable,
			Message: "sandbox engine not provided",
		},
		Wrapped: false,
		Name:    spec.Name,
		Args:    spec.Args,
		Dir:     spec.Dir,
	}
	command := exec.CommandContext(ctx, spec.Name, spec.Args...)
	command.Dir = spec.Dir
	applyWindowsShellCommandLine(command, commandText, plan.Wrapped)
	return command, plan, nil
}

func addSandboxMeta(meta map[string]string, plan zeroSandbox.CommandPlan) {
	if plan.Backend.Name == "" {
		return
	}
	meta["sandbox_backend"] = string(plan.Backend.Name)
	if plan.TargetBackend != "" {
		meta["sandbox_target_backend"] = string(plan.TargetBackend)
	}
	meta["sandbox_wrapped"] = strconv.FormatBool(plan.Wrapped)
	if plan.EnforcementLevel != "" {
		meta["sandbox_enforcement_level"] = string(plan.EnforcementLevel)
	}
	if plan.DowngradeReason != "" {
		meta["sandbox_downgrade_reason"] = plan.DowngradeReason
	}
	meta["sandbox_requires_platform"] = strconv.FormatBool(plan.RequiresPlatformSandbox)
	if plan.Backend.Message != "" {
		meta["sandbox_message"] = plan.Backend.Message
	}
	if plan.SandboxDir != "" {
		meta["sandbox_cwd"] = plan.SandboxDir
	}
}

// interactiveBlockResult builds the structured tool Result returned when a
// command is refused before execution because it would hang the agent. The
// block is surfaced both in Output (clearly delimited) and in Meta/Display
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
		return zeroSandbox.WindowsShellArgs(command)
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

// bashOutputBudgetBytes caps each of stdout/stderr shown to the model. bash is the
// one tool that can emit unbounded output (`cat large.log`, `find /`, verbose test
// runs); every other read/search tool already budgets its output. Head+tail
// truncation keeps both the start and the end of an oversized stream, since
// build/test failures usually surface at the tail.
const bashOutputBudgetBytes = 96 * 1024

// budgetBashOutput truncates stdout and stderr to bashOutputBudgetBytes each,
// keeping the head and tail of anything larger, and records raw/emitted byte
// counts plus a truncated flag in meta (mirroring outputBudgetMeta's shape for
// the read/search tools). Detection that needs the full output (sandbox-denial
// scanning) must run on the raw strings before this is applied.
func budgetBashOutput(stdout string, stderr string, meta map[string]string) (string, string, bool) {
	return budgetBashCapture(stdout, len(stdout), stderr, len(stderr), meta)
}

// budgetBashCapture is budgetBashOutput for the streaming-capture path: outTotal
// and errTotal are the true byte counts (from boundedBuffer.total), which may
// exceed the retained strings when the middle was dropped during capture. Meta's
// raw_bytes therefore reflects everything the command produced, not just what was
// kept in memory.
func budgetBashCapture(out string, outTotal int, errStr string, errTotal int, meta map[string]string) (string, string, bool) {
	outText, outRaw, outTrunc := truncateHeadTailWithTotal(out, outTotal, bashOutputBudgetBytes)
	errText, errRaw, errTrunc := truncateHeadTailWithTotal(errStr, errTotal, bashOutputBudgetBytes)
	truncated := outTrunc || errTrunc
	if meta != nil {
		emitted := len(outText) + len(errText)
		meta["raw_bytes"] = strconv.Itoa(outRaw + errRaw)
		meta["emitted_bytes"] = strconv.Itoa(emitted)
		meta["estimated_tokens"] = strconv.Itoa(estimatedTokensFromBytes(emitted))
		if truncated {
			meta["truncated"] = "true"
		}
	}
	return outText, errText, truncated
}

// boundedBuffer is an io.Writer that retains at most headCap bytes from the start
// and tailCap bytes from the end of a stream while counting the total written, so
// a command emitting unbounded output (`cat huge.log`, `yes`) cannot grow Zero's
// memory: the middle is discarded as it arrives instead of buffered whole and then
// truncated. total records the full size for the truncation marker even though the
// middle is never held. Not safe for concurrent writes; exec drives Stdout and
// Stderr from separate goroutines, so each stream gets its own buffer.
type boundedBuffer struct {
	head    []byte
	headCap int
	tail    []byte
	tailCap int
	total   int
}

func newBoundedBuffer(headCap, tailCap int) *boundedBuffer {
	return &boundedBuffer{headCap: headCap, tailCap: tailCap}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.total += n
	// Fill the head until it reaches headCap; the head is written once and frozen.
	if len(b.head) < b.headCap {
		take := min(b.headCap-len(b.head), len(p))
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	// Anything past the head feeds a rolling tail that keeps only the last tailCap
	// bytes. Compact once it grows past 2×tailCap so the append stays amortized O(1)
	// while memory never exceeds ~2×tailCap.
	if len(p) > 0 && b.tailCap > 0 {
		b.tail = append(b.tail, p...)
		if len(b.tail) > 2*b.tailCap {
			b.tail = append(b.tail[:0], b.tail[len(b.tail)-b.tailCap:]...)
		}
	}
	return n, nil
}

// retained returns the kept head+tail bytes (marker-less) as a string. The junction
// between head and tail lands in the middle, which the display budget trims away;
// callers that need the true size read total separately.
func (b *boundedBuffer) retained() string {
	if len(b.tail) > b.tailCap {
		// Not yet compacted since the last overflow; expose only the last tailCap.
		return string(b.head) + string(b.tail[len(b.tail)-b.tailCap:])
	}
	return string(b.head) + string(b.tail)
}

// truncateHeadTail keeps the first and last halves of value when it exceeds
// maxBytes, dropping the middle behind a marker. Returns the possibly-truncated
// text, the raw byte length, and whether truncation happened.
func truncateHeadTail(value string, maxBytes int) (string, int, bool) {
	// value holds the whole stream, so its length is the true raw size.
	return truncateHeadTailWithTotal(value, len(value), maxBytes)
}

// truncateHeadTailWithTotal head+tail-truncates value to maxBytes, using total —
// the full original byte count — for the "N bytes omitted" marker and the raw
// count. total may exceed len(value) when the middle was already discarded during
// bounded capture (boundedBuffer): value then holds only the retained head+tail,
// and this trims it to the display budget while still reporting the true total.
func truncateHeadTailWithTotal(value string, total, maxBytes int) (string, int, bool) {
	if maxBytes <= 0 || total <= maxBytes {
		return value, total, false
	}
	marker := fmt.Sprintf("\n[zero] output truncated: %d bytes omitted from the middle — redirect to a file and read_file a range for the full text\n", total-maxBytes)
	budget := max(maxBytes-len(marker), 0)
	head := budget / 2
	tail := budget - head
	return utf8Prefix(value, head) + marker + utf8Suffix(value, tail), total, true
}

func formatBashOutputWithShellHint(command string, stdout string, stderr string, exitCode int, meta map[string]string) string {
	output := formatBashOutput(stdout, stderr, exitCode)
	if issue := detectShellOutputIssue(command, stdout+"\n"+stderr, runtime.GOOS); issue != nil {
		meta["shell_issue"] = issue.Kind
		output = appendShellIssueHint(output, *issue)
	}
	return output
}
