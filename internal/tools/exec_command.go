package tools

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sort"
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
	maxExecSessions           = 64
	recentExecOutputBytes     = 4096
	execSessionStopTimeout    = 3 * time.Second
	execSessionEvictedMessage = "[zero] session evicted: too many background terminals\n"
)

type execSessionManager struct {
	mu                 sync.Mutex
	nextID             int
	sessions           map[int]*execSession
	completedRetention time.Duration
	maxSessions        int
}

func newExecSessionManager() *execSessionManager {
	return &execSessionManager{
		nextID:             1000,
		sessions:           make(map[int]*execSession),
		completedRetention: completedSessionRetention,
		maxSessions:        maxExecSessions,
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
	var pruned *execSession
	removePruned := false
	if manager.maxSessions > 0 && len(manager.sessions) >= manager.maxSessions {
		pruned = manager.sessionToPruneLocked()
		if pruned != nil {
			removePruned = pruned.doneClosed()
			if removePruned {
				delete(manager.sessions, pruned.id)
			}
		}
	}
	manager.sessions[session.id] = session
	manager.mu.Unlock()
	if pruned != nil && !removePruned {
		pruned.output.Write([]byte(execSessionEvictedMessage))
		pruned.terminate()
	}
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

func (manager *execSessionManager) sessionToPruneLocked() *execSession {
	if len(manager.sessions) == 0 {
		return nil
	}
	// Snapshot each session's lastUsedAt UNDER session.mu before sorting: touch()
	// writes lastUsedAt under session.mu, so reading it here (under manager.mu only)
	// was a data race on a multi-word time.Time. Lock order stays manager.mu →
	// session.mu (no path takes them the other way). (AUDIT-L15)
	type sessionAge struct {
		session *execSession
		last    time.Time
	}
	ages := make([]sessionAge, 0, len(manager.sessions))
	for _, session := range manager.sessions {
		ages = append(ages, sessionAge{session: session, last: session.lastUsed()})
	}
	sort.Slice(ages, func(i, j int) bool {
		return ages[i].last.Before(ages[j].last)
	})
	for _, a := range ages {
		if a.session.doneClosed() {
			return a.session
		}
	}
	if len(ages) <= 8 {
		return nil
	}
	return ages[0].session
}

// lastUsed returns the session's last-used time under its own lock, so the prune
// comparator never races touch()'s write to lastUsedAt.
func (session *execSession) lastUsed() time.Time {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.lastUsedAt
}

func (manager *execSessionManager) list() []ExecSessionSnapshot {
	manager.mu.Lock()
	sessions := make([]*execSession, 0, len(manager.sessions))
	for _, session := range manager.sessions {
		if !session.doneClosed() {
			sessions = append(sessions, session)
		}
	}
	manager.mu.Unlock()

	snapshots := make([]ExecSessionSnapshot, 0, len(sessions))
	for _, session := range sessions {
		snapshots = append(snapshots, session.snapshot())
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].ID < snapshots[j].ID
	})
	return snapshots
}

func (manager *execSessionManager) stop(id int) bool {
	session, ok := manager.get(id)
	if !ok {
		return false
	}
	session.terminate()
	return true
}

func (manager *execSessionManager) stopAll() []int {
	manager.mu.Lock()
	sessions := make([]*execSession, 0, len(manager.sessions))
	for _, session := range manager.sessions {
		if !session.doneClosed() {
			sessions = append(sessions, session)
		}
	}
	manager.mu.Unlock()
	ids := make([]int, 0, len(sessions))
	for _, session := range sessions {
		session.terminate()
		ids = append(ids, session.id)
	}
	waitForExecSessions(sessions, execSessionStopTimeout)
	sort.Ints(ids)
	return ids
}

func waitForExecSessions(sessions []*execSession, timeout time.Duration) {
	if timeout <= 0 || len(sessions) == 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	for _, session := range sessions {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		timer := time.NewTimer(remaining)
		select {
		case <-session.done:
		case <-timer.C:
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
}

type ExecSessionSnapshot struct {
	ID           int
	Command      string
	Cwd          string
	RelativeCwd  string
	StartedAt    time.Time
	LastUsedAt   time.Time
	TTY          bool
	Status       string
	ExitCode     *int
	RecentOutput string
}

type ExecSessionController interface {
	ExecSessions() []ExecSessionSnapshot
	StopExecSession(id int) bool
	StopAllExecSessions() []int
}

type execSession struct {
	id          int
	commandText string
	cwd         string
	relativeCwd string
	startedAt   time.Time
	lastUsedAt  time.Time
	tty         bool
	command     *exec.Cmd
	plan        zeroSandbox.CommandPlan
	cancel      context.CancelFunc
	stdin       io.WriteCloser
	cleanup     func()
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

func (session *execSession) touch() {
	session.mu.Lock()
	session.lastUsedAt = time.Now()
	session.mu.Unlock()
}

func (session *execSession) terminate() {
	if session.cancel != nil {
		session.cancel()
	}
}

func (session *execSession) snapshot() ExecSessionSnapshot {
	session.mu.Lock()
	startedAt := session.startedAt
	lastUsedAt := session.lastUsedAt
	exitCode := session.exitCode
	var copiedExit *int
	if exitCode != nil {
		value := *exitCode
		copiedExit = &value
	}
	session.mu.Unlock()
	status := "running"
	if copiedExit != nil {
		status = "exited"
	}
	return ExecSessionSnapshot{
		ID:           session.id,
		Command:      session.commandText,
		Cwd:          session.cwd,
		RelativeCwd:  session.relativeCwd,
		StartedAt:    startedAt,
		LastUsedAt:   lastUsedAt,
		TTY:          session.tty,
		Status:       status,
		ExitCode:     copiedExit,
		RecentOutput: session.output.recentString(),
	}
}

type execOutputBuffer struct {
	mu     sync.Mutex
	data   []byte
	recent []byte
	notify chan struct{}
}

func newExecOutputBuffer() *execOutputBuffer {
	return &execOutputBuffer{notify: make(chan struct{}, 1)}
}

func (buffer *execOutputBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	buffer.data = append(buffer.data, p...)
	buffer.recent = append(buffer.recent, p...)
	if len(buffer.recent) > recentExecOutputBytes {
		buffer.recent = buffer.recent[len(buffer.recent)-recentExecOutputBytes:]
	}
	buffer.mu.Unlock()
	select {
	case buffer.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (buffer *execOutputBuffer) recentString() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.recent)
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
	description := "Runs a command in a PTY, returning output or a session ID for ongoing interaction."
	if runtimeGOOS() == "windows" {
		description += "\n\n" + shellGuidanceForGOOS(runtimeGOOS())
	}
	return execCommandTool{
		baseTool: baseTool{
			name:        ExecCommandToolName,
			description: description,
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"cmd":                 {Type: "string", Description: "Shell command to execute."},
					"workdir":             {Type: "string", Description: "Working directory for the command. Defaults to the turn cwd.", Default: "."},
					"cwd":                 {Type: "string", Description: "Alias for workdir. Prefer workdir.", Default: "."},
					"yield_time_ms":       {Type: "integer", Description: "Wait before yielding output. Defaults to 10000 ms; effective range is 250-30000 ms.", Default: defaultExecYieldTimeMS, Minimum: intPtr(1), Maximum: intPtr(maxExecYieldTimeMS)},
					"max_output_tokens":   {Type: "integer", Description: "Output token budget. Defaults to 10000 tokens; larger requests may be capped by policy.", Default: defaultMaxOutputTokens, Minimum: intPtr(1), Maximum: intPtr(maxExecOutputTokenRequest)},
					"sandbox_permissions": {Type: "string", Enum: []string{string(SandboxPermissionsUseDefault), string(SandboxPermissionsWithAdditionalPermissions), string(SandboxPermissionsRequireEscalated)}, Description: "Per-command sandbox override. Defaults to `use_default`; use `with_additional_permissions` with `additional_permissions`, or `require_escalated` for unsandboxed execution.", Default: string(SandboxPermissionsUseDefault)},
					"additional_permissions": {
						Type:        "object",
						Description: "Sandboxed filesystem or network access for this command; only with `sandbox_permissions: \"with_additional_permissions\"`.",
						Properties:  additionalPermissionsProperties(),
					},
					"justification": {Type: "string", Description: "User-facing approval question for `require_escalated`; omit otherwise."},
					"prefix_rule":   {Type: "array", Items: &PropertySchema{Type: "string"}, Description: "Reusable approval prefix for `cmd`, only with `sandbox_permissions: \"require_escalated\"`; for example [\"git\", \"pull\"]."},
					"tty":           {Type: "boolean", Description: "True allocates a PTY for the command; false or omitted uses plain pipes.", Default: false},
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

func (tool execCommandTool) ExecSessions() []ExecSessionSnapshot {
	return tool.manager.list()
}

func (tool execCommandTool) StopExecSession(id int) bool {
	return tool.manager.stop(id)
}

func (tool execCommandTool) StopAllExecSessions() []int {
	return tool.manager.stopAll()
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
	ttyRequested, err := boolArg(args, "tty", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for exec_command: " + err.Error())
	}
	sandboxPermissions, err := sandboxPermissionsArg(args)
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

	session, err := tool.startSession(commandText, absoluteCwd, relativeCwd, ttyRequested, engine, sandboxPermissions)
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
		tty:             session.tty,
		plan:            session.plan,
		maxOutputTokens: maxOutputTokens,
	})
}

func (tool execCommandTool) startSession(commandText string, absoluteCwd string, relativeCwd string, ttyRequested bool, engine *zeroSandbox.Engine, sandboxPermissions SandboxPermissionOverride) (*execSession, error) {
	id := tool.manager.allocateID()
	commandCtx, cancel := context.WithCancel(context.Background())
	commandEngine := commandEngineForSandboxPermissions(engine, sandboxPermissions)
	command, plan, err := buildBashCommand(commandCtx, commandText, absoluteCwd, commandEngine)
	if err != nil {
		cancel()
		return nil, err
	}
	output := newExecOutputBuffer()
	monitor := zeroSandbox.StartDenialMonitor(context.Background(), plan.MonitorTag)
	stdin, tty, cleanup, err := startExecProcess(command, output, ttyRequested)
	if err != nil {
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
		lastUsedAt:  time.Now(),
		tty:         tty,
		command:     command,
		plan:        plan,
		cancel:      cancel,
		stdin:       stdin,
		cleanup:     cleanup,
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
		if session.cleanup != nil {
			session.cleanup()
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
			description: "Writes characters to an existing unified exec session and returns recent output.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"session_id":        {Type: "integer", Description: "Identifier of the running unified exec session."},
					"chars":             {Type: "string", Description: "Bytes to write to stdin. Defaults to empty, which polls without writing.", Default: ""},
					"yield_time_ms":     {Type: "integer", Description: "Wait before yielding output. Non-empty writes default to 250 ms and cap at 30000 ms; empty polls wait 5000-300000 ms by default.", Default: defaultPollYieldTimeMS, Minimum: intPtr(1), Maximum: intPtr(maxPollYieldTimeMS)},
					"max_output_tokens": {Type: "integer", Description: "Output token budget. Defaults to 10000 tokens; larger requests may be capped by policy.", Default: defaultMaxOutputTokens, Minimum: intPtr(1), Maximum: intPtr(maxExecOutputTokenRequest)},
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
	if value, ok := args["session_id"]; !ok || value == nil {
		return errorResult("Error: Invalid arguments for write_stdin: session_id is required")
	}
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
	session.touch()
	interrupted := false
	if chars != "" {
		if shouldInterruptExecSession(chars, session.tty) {
			interrupted = true
			session.terminate()
		} else if !session.tty {
			return errorResult(fmt.Sprintf("Error: exec session_id %d does not accept stdin. Use empty chars to poll, or send chars \"\\u0003\" to interrupt/stop it.", sessionID))
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
		tty:             session.tty,
		interrupted:     interrupted,
		plan:            session.plan,
		maxOutputTokens: maxOutputTokens,
	})
}

func shouldInterruptExecSession(chars string, tty bool) bool {
	if strings.Contains(chars, "\x03") {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(chars))
	normalizedNoSpace := strings.ReplaceAll(normalized, " ", "")
	switch normalizedNoSpace {
	case `\u0003`, `\\u0003`, `\x03`, `\\x03`, "^c", "ctrl-c", "control-c", "sigint", "interrupt":
		return true
	}
	if tty {
		return false
	}
	switch normalized {
	case "q", "quit", "exit", "stop", "kill", "terminate":
		return true
	}
	return false
}

type execToolResultInput struct {
	commandText     string
	output          string
	sessionID       int
	exitCode        int
	exited          bool
	relativeCwd     string
	tty             bool
	interrupted     bool
	plan            zeroSandbox.CommandPlan
	maxOutputTokens int
}

func execToolResult(input execToolResultInput) Result {
	output, truncated := truncateExecOutput(input.output, input.maxOutputTokens)
	meta := map[string]string{
		"cwd": input.relativeCwd,
		"tty": strconv.FormatBool(input.tty),
	}
	addSandboxMeta(meta, input.plan)
	if input.exited {
		meta["exit_code"] = strconv.Itoa(input.exitCode)
		if input.interrupted {
			meta["interrupted"] = "true"
		}
		if !input.interrupted {
			markLikelySandboxDenial(meta, input.plan, input.exitCode, output)
		}
	} else {
		meta["session_id"] = strconv.Itoa(input.sessionID)
	}

	status := StatusOK
	if input.exited && ((input.exitCode != 0 && !input.interrupted) || meta[SandboxLikelyDeniedMeta] == "true") {
		status = StatusError
	}
	body := formatExecCommandOutput(output, input.sessionID, input.exited, input.exitCode, input.interrupted)
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

func formatExecCommandOutput(output string, sessionID int, exited bool, exitCode int, interrupted bool) string {
	output = strings.TrimRight(output, "\r\n")
	parts := []string{}
	if output != "" {
		parts = append(parts, "output:\n"+output)
	}
	if exited {
		if output == "" {
			if interrupted {
				parts = append(parts, "Command interrupted.")
			} else {
				parts = append(parts, "Command completed with no output.")
			}
		}
		if interrupted {
			parts = append(parts, "interrupted: true")
		}
		parts = append(parts, fmt.Sprintf("exit_code: %d", exitCode))
	} else {
		if output == "" {
			parts = append(parts, "Command is still running.")
		}
		parts = append(parts, fmt.Sprintf("session_id: %d", sessionID))
		parts = append(parts, fmt.Sprintf("Use write_stdin with session_id %d and empty chars to poll; send chars \"\\u0003\" to interrupt/stop it.", sessionID))
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
