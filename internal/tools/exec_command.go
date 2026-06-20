package tools

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

const (
	ExecCommandToolName       = "exec_command"
	WriteStdinToolName        = "write_stdin"
	defaultExecYieldTimeMS    = 10000
	defaultPollYieldTimeMS    = 5000
	maxExecYieldTimeMS        = 30000
	maxPollYieldTimeMS        = 300000
	defaultMaxOutputTokens    = 10000
	maxExecOutputTokenRequest = 200000
	completedSessionRetention = 30 * time.Second
)

type execSessionManager struct {
	mu                 sync.Mutex
	nextID             int
	sessions           map[int]*execSession
	completedRetention time.Duration
}

func newExecSessionManager() *execSessionManager {
	return &execSessionManager{
		nextID:             1000,
		sessions:           make(map[int]*execSession),
		completedRetention: completedSessionRetention,
	}
}

var defaultExecSessionManager = newExecSessionManager()

func (manager *execSessionManager) allocateID() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	id := manager.nextID
	manager.nextID++
	return id
}

func (manager *execSessionManager) store(session *execSession) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.sessions[session.id] = session
}

func (manager *execSessionManager) get(id int) (*execSession, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	session, ok := manager.sessions[id]
	return session, ok
}

func (manager *execSessionManager) remove(id int) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	delete(manager.sessions, id)
}

func (manager *execSessionManager) removeCompletedLater(session *execSession) {
	retention := manager.completedRetention
	go func() {
		<-session.done
		if retention > 0 {
			timer := time.NewTimer(retention)
			<-timer.C
		}
		manager.remove(session.id)
	}()
}

func (manager *execSessionManager) len() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return len(manager.sessions)
}

type execSession struct {
	id          int
	commandText string
	cwd         string
	relativeCwd string
	startedAt   time.Time
	command     *exec.Cmd
	plan        zeroSandbox.CommandPlan
	cancel      context.CancelFunc
	stdin       io.WriteCloser
	output      *execOutputBuffer

	doneOnce sync.Once
	done     chan struct{}
	mu       sync.Mutex
	exitCode *int
	waitErr  error
}

func (session *execSession) markDone(err error, exitCode int) {
	session.mu.Lock()
	session.waitErr = err
	session.exitCode = &exitCode
	session.mu.Unlock()
	session.doneOnce.Do(func() { close(session.done) })
}

func (session *execSession) doneClosed() bool {
	select {
	case <-session.done:
		return true
	default:
		return false
	}
}

func (session *execSession) exitStatus() (int, bool) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.exitCode == nil {
		return 0, false
	}
	return *session.exitCode, true
}

func (session *execSession) terminate() {
	if session.cancel != nil {
		session.cancel()
	}
}

type execOutputBuffer struct {
	mu     sync.Mutex
	data   []byte
	notify chan struct{}
}

func newExecOutputBuffer() *execOutputBuffer {
	return &execOutputBuffer{notify: make(chan struct{}, 1)}
}

func (buffer *execOutputBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	buffer.data = append(buffer.data, p...)
	buffer.mu.Unlock()
	select {
	case buffer.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (buffer *execOutputBuffer) drainString() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if len(buffer.data) == 0 {
		return ""
	}
	out := string(buffer.data)
	buffer.data = nil
	return out
}

type execCommandTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
	manager       *execSessionManager
}

func NewExecCommandTool(workspaceRoot string, manager *execSessionManager) Tool {
	return NewScopedExecCommandTool(workspaceRoot, nil, manager)
}

func NewScopedExecCommandTool(workspaceRoot string, scope PathScope, manager *execSessionManager) Tool {
	if manager == nil {
		manager = defaultExecSessionManager
	}
	shellGuidance := shellGuidanceForGOOS(runtimeGOOS())
	return execCommandTool{
		baseTool: baseTool{
			name:        ExecCommandToolName,
			description: "Runs a command and returns output or a session_id for ongoing interaction. Use this for long-running commands such as dev servers. " + shellGuidance,
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"cmd":               {Type: "string", Description: "Shell command to execute using the host shell. " + shellGuidance},
					"workdir":           {Type: "string", Description: "Working directory for the command. Defaults to the workspace root.", Default: "."},
					"cwd":               {Type: "string", Description: "Alias for workdir. Prefer workdir.", Default: "."},
					"yield_time_ms":     {Type: "integer", Description: "Wait before yielding output. If the command is still running after this, the result includes session_id.", Default: defaultExecYieldTimeMS, Minimum: intPtr(1), Maximum: intPtr(maxExecYieldTimeMS)},
					"max_output_tokens": {Type: "integer", Description: "Output token budget. Defaults to 10000; larger requests may be capped.", Default: defaultMaxOutputTokens, Minimum: intPtr(1), Maximum: intPtr(maxExecOutputTokenRequest)},
					"prefix_rule":       {Type: "array", Items: &PropertySchema{Type: "string"}, Description: "Optional reusable approval prefix for this command, for example [\"git\", \"status\"]. Only simple command prefixes are accepted."},
				},
				Required:             []string{"cmd"},
				AdditionalProperties: false,
			},
			safety: promptSafety(SideEffectShell, "Shell commands can read, write, or execute programs."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
		manager:       manager,
	}
}

func (tool execCommandTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.run(ctx, args, nil)
}

func (tool execCommandTool) RunWithSandbox(ctx context.Context, args map[string]any, engine *zeroSandbox.Engine) Result {
	return tool.run(ctx, args, engine)
}

func (tool execCommandTool) run(ctx context.Context, args map[string]any, engine *zeroSandbox.Engine) Result {
	commandText, err := aliasedStringArg(args, []string{"cmd", "command", "script", "shell"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for exec_command: " + err.Error())
	}
	workdir, err := aliasedStringArg(args, []string{"workdir", "cwd", "dir", "directory"}, ".", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for exec_command: " + err.Error())
	}
	yieldTimeMS, err := intArg(args, "yield_time_ms", defaultExecYieldTimeMS, 1, maxExecYieldTimeMS)
	if err != nil {
		return errorResult("Error: Invalid arguments for exec_command: " + err.Error())
	}
	maxOutputTokens, err := intArg(args, "max_output_tokens", defaultMaxOutputTokens, 1, maxExecOutputTokenRequest)
	if err != nil {
		return errorResult("Error: Invalid arguments for exec_command: " + err.Error())
	}
	if issue := detectShellCommandIssue(commandText, runtimeGOOS()); issue != nil {
		return shellIssueBlockResult(*issue)
	}
	if interactive := zeroSandbox.DetectInteractiveCommand(commandText, runtimeGOOS()); interactive.Interactive {
		return interactiveBlockResult(interactive)
	}
	absoluteCwd, relativeCwd, err := resolveScopedPath(tool.workspaceRoot, tool.scope, workdir)
	if err != nil {
		return errorResult("Error running exec_command: " + err.Error())
	}

	session, err := tool.startSession(commandText, absoluteCwd, relativeCwd, engine)
	if err != nil {
		return errorResult("Error starting exec_command: " + err.Error())
	}
	output := session.collect(ctx, time.Duration(yieldTimeMS)*time.Millisecond)
	if ctx != nil && ctx.Err() != nil && !session.doneClosed() {
		session.terminate()
		output += session.collect(context.Background(), time.Second)
	}
	exitCode, exited := session.exitStatus()
	if exited {
		tool.manager.remove(session.id)
	}
	return execToolResult(execToolResultInput{
		commandText:     commandText,
		output:          output,
		sessionID:       session.id,
		exitCode:        exitCode,
		exited:          exited,
		relativeCwd:     relativeCwd,
		plan:            session.plan,
		maxOutputTokens: maxOutputTokens,
	})
}

func (tool execCommandTool) startSession(commandText string, absoluteCwd string, relativeCwd string, engine *zeroSandbox.Engine) (*execSession, error) {
	id := tool.manager.allocateID()
	commandCtx, cancel := context.WithCancel(context.Background())
	command, plan, err := buildBashCommand(commandCtx, commandText, absoluteCwd, engine)
	if err != nil {
		cancel()
		return nil, err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		plan.Cleanup()
		cancel()
		return nil, err
	}
	output := newExecOutputBuffer()
	command.Stdout = output
	command.Stderr = output
	hardenProcessLifetime(command)
	monitor := zeroSandbox.StartDenialMonitor(context.Background(), plan.MonitorTag)
	if err := command.Start(); err != nil {
		_ = monitor.Stop()
		plan.Cleanup()
		cancel()
		return nil, err
	}
	session := &execSession{
		id:          id,
		commandText: commandText,
		cwd:         absoluteCwd,
		relativeCwd: relativeCwd,
		startedAt:   time.Now(),
		command:     command,
		plan:        plan,
		cancel:      cancel,
		stdin:       stdin,
		output:      output,
		done:        make(chan struct{}),
	}
	tool.manager.store(session)
	tool.manager.removeCompletedLater(session)
	go func() {
		err := command.Wait()
		if blocks := monitor.Stop(); len(blocks) > 0 {
			output.Write([]byte(appendSandboxBlocks("", blocks)))
		}
		plan.Cleanup()
		cancel()
		session.markDone(err, commandExitCode(err))
	}()
	return session, nil
}

func (session *execSession) collect(ctx context.Context, wait time.Duration) string {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(wait)
	var builder strings.Builder
	for {
		if chunk := session.output.drainString(); chunk != "" {
			builder.WriteString(chunk)
			continue
		}
		if session.doneClosed() {
			if chunk := session.output.drainString(); chunk != "" {
				builder.WriteString(chunk)
			}
			return builder.String()
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return builder.String()
		}
		timer := time.NewTimer(remaining)
		select {
		case <-session.output.notify:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-session.done:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return builder.String()
		case <-timer.C:
			return builder.String()
		}
	}
}

type writeStdinTool struct {
	baseTool
	manager *execSessionManager
}

func NewWriteStdinTool(manager *execSessionManager) Tool {
	if manager == nil {
		manager = defaultExecSessionManager
	}
	return writeStdinTool{
		baseTool: baseTool{
			name:        WriteStdinToolName,
			description: "Writes characters to an existing exec_command session and returns recent output. Empty polls and Ctrl-C interrupts are allowed; other stdin bytes may require approval.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"session_id":        {Type: "integer", Description: "Identifier returned by exec_command while the process is still running."},
					"chars":             {Type: "string", Description: "Bytes to write to stdin. Defaults to empty, which polls without writing. Use \\u0003 to interrupt the session.", Default: ""},
					"yield_time_ms":     {Type: "integer", Description: "Wait before yielding output. Empty polls default to 5000ms and can wait up to 300000ms.", Default: defaultPollYieldTimeMS, Minimum: intPtr(1), Maximum: intPtr(maxPollYieldTimeMS)},
					"max_output_tokens": {Type: "integer", Description: "Output token budget. Defaults to 10000; larger requests may be capped.", Default: defaultMaxOutputTokens, Minimum: intPtr(1), Maximum: intPtr(maxExecOutputTokenRequest)},
				},
				Required:             []string{"session_id"},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect:      SideEffectShell,
				Permission:      PermissionPrompt,
				Reason:          "Sending stdin can drive an existing shell process beyond the original command; empty polling and Ctrl-C interrupts are allowed automatically.",
				AdvertiseInAuto: true,
			},
		},
		manager: manager,
	}
}

func (tool writeStdinTool) PermissionForArgs(args map[string]any) Permission {
	raw, ok := args["chars"]
	if !ok || raw == nil {
		return PermissionAllow
	}
	chars, ok := raw.(string)
	if !ok {
		return PermissionPrompt
	}
	if chars == "" || chars == "\x03" {
		return PermissionAllow
	}
	return PermissionPrompt
}

func (tool writeStdinTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool writeStdinTool) RunWithOptions(ctx context.Context, args map[string]any, _ RunOptions) Result {
	sessionID, err := intArg(args, "session_id", 0, 1, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_stdin: " + err.Error())
	}
	chars, err := stringArgWithEmpty(args, "chars", "", false, true)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_stdin: " + err.Error())
	}
	yieldTimeMS, err := intArg(args, "yield_time_ms", defaultPollYieldTimeMS, 1, maxPollYieldTimeMS)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_stdin: " + err.Error())
	}
	maxOutputTokens, err := intArg(args, "max_output_tokens", defaultMaxOutputTokens, 1, maxExecOutputTokenRequest)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_stdin: " + err.Error())
	}
	session, ok := tool.manager.get(sessionID)
	if !ok {
		return errorResult(fmt.Sprintf("Error: Unknown exec session_id %d.", sessionID))
	}
	if chars != "" {
		if chars == "\x03" {
			session.terminate()
		} else if session.stdin != nil {
			if _, err := io.WriteString(session.stdin, chars); err != nil && !session.doneClosed() {
				return errorResult("Error writing to exec session: " + err.Error())
			}
		}
	}
	output := session.collect(ctx, time.Duration(yieldTimeMS)*time.Millisecond)
	exitCode, exited := session.exitStatus()
	if exited {
		tool.manager.remove(session.id)
	}
	return execToolResult(execToolResultInput{
		commandText:     session.commandText,
		output:          output,
		sessionID:       session.id,
		exitCode:        exitCode,
		exited:          exited,
		relativeCwd:     session.relativeCwd,
		plan:            session.plan,
		maxOutputTokens: maxOutputTokens,
	})
}

type execToolResultInput struct {
	commandText     string
	output          string
	sessionID       int
	exitCode        int
	exited          bool
	relativeCwd     string
	plan            zeroSandbox.CommandPlan
	maxOutputTokens int
}

func execToolResult(input execToolResultInput) Result {
	output, truncated := truncateExecOutput(input.output, input.maxOutputTokens)
	meta := map[string]string{
		"cwd": input.relativeCwd,
	}
	addSandboxMeta(meta, input.plan)
	if input.exited {
		meta["exit_code"] = strconv.Itoa(input.exitCode)
	} else {
		meta["session_id"] = strconv.Itoa(input.sessionID)
	}

	status := StatusOK
	if input.exited && input.exitCode != 0 {
		status = StatusError
	}
	body := formatExecCommandOutput(output, input.sessionID, input.exited, input.exitCode)
	return Result{
		Status:    status,
		Output:    body,
		Truncated: truncated,
		Meta:      meta,
		Display: Display{
			Summary: execDisplaySummary(input.commandText, input.sessionID, input.exited, input.exitCode),
			Kind:    "shell",
		},
	}
}

func formatExecCommandOutput(output string, sessionID int, exited bool, exitCode int) string {
	output = strings.TrimRight(output, "\r\n")
	parts := []string{}
	if output != "" {
		parts = append(parts, "output:\n"+output)
	}
	if exited {
		if output == "" {
			parts = append(parts, "Command completed with no output.")
		}
		parts = append(parts, fmt.Sprintf("exit_code: %d", exitCode))
	} else {
		if output == "" {
			parts = append(parts, "Command is still running.")
		}
		parts = append(parts, fmt.Sprintf("session_id: %d", sessionID))
		parts = append(parts, fmt.Sprintf("Use write_stdin with session_id %d to poll, send input, or interrupt it.", sessionID))
	}
	return strings.Join(parts, "\n")
}

func truncateExecOutput(output string, maxOutputTokens int) (string, bool) {
	if maxOutputTokens <= 0 {
		maxOutputTokens = defaultMaxOutputTokens
	}
	maxBytes := maxOutputTokens * 4
	if len(output) <= maxBytes {
		return output, false
	}
	head := maxBytes / 2
	tail := maxBytes - head
	return utf8Prefix(output, head) + "\n[zero] output truncated\n" + utf8Suffix(output, tail), true
}

func utf8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.RuneStart(value[maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}

func utf8Suffix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func execDisplaySummary(commandText string, sessionID int, exited bool, exitCode int) string {
	commandText = strings.TrimSpace(commandText)
	if commandText == "" {
		commandText = "command"
	}
	if exited {
		return fmt.Sprintf("%s exited with code %d", commandText, exitCode)
	}
	return fmt.Sprintf("%s still running as session %d", commandText, sessionID)
}

func runtimeGOOS() string {
	return runtime.GOOS
}
