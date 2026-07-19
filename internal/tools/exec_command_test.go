package tools

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gitlawb/zero/internal/sandbox"
)

func TestIndependentExecCommandConstructorsShareDefaultManager(t *testing.T) {
	root := t.TempDir()
	execTool := NewScopedExecCommandTool(root, nil, nil)
	writeTool := NewWriteStdinTool(nil)

	start := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("sleep"),
		"yield_time_ms": 10,
	})
	if start.Status != StatusOK {
		t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
	}
	sessionID, err := strconv.Atoi(start.Meta["session_id"])
	if err != nil {
		t.Fatalf("session_id is not numeric: %v", err)
	}

	poll := writeTool.Run(context.Background(), map[string]any{
		"session_id":    sessionID,
		"yield_time_ms": 30000,
	})
	if poll.Status != StatusOK {
		t.Fatalf("write_stdin poll status = %s: %s", poll.Status, poll.Output)
	}
	if poll.Meta["exit_code"] != "0" {
		t.Fatalf("expected shared manager to find completed session, got meta=%#v output=%q", poll.Meta, poll.Output)
	}
}

func TestExecCommandToolDescribesHostStateEscalation(t *testing.T) {
	tool := NewScopedExecCommandTool(t.TempDir(), nil, nil)
	schema := tool.Parameters()
	descriptionParts := []string{tool.Description()}
	for _, property := range schema.Properties {
		descriptionParts = append(descriptionParts, property.Description)
	}
	description := strings.ToLower(strings.Join(descriptionParts, " "))
	for _, want := range []string{
		"sandbox_permissions",
		"require_escalated",
		"host/global process",
		"sandbox namespaces",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("expected exec_command escalation guidance %q, got %q", want, description)
		}
	}
}

func TestExecCommandToolDescribesHostShellSyntax(t *testing.T) {
	tool := NewScopedExecCommandTool(t.TempDir(), nil, nil)
	schema := tool.Parameters()
	descriptionParts := []string{tool.Description()}
	for _, property := range schema.Properties {
		descriptionParts = append(descriptionParts, property.Description)
	}
	description := strings.ToLower(strings.Join(descriptionParts, " "))

	if runtime.GOOS == "windows" {
		if !strings.Contains(description, "cmd.exe") || !strings.Contains(description, "cwd") {
			t.Fatalf("expected Windows cmd.exe and cwd guidance in exec_command description, got %q", description)
		}
		if !strings.Contains(description, "double quotes") || !strings.Contains(description, `--jq ".a | b"`) {
			t.Fatalf("expected the double-quote metacharacter rule in exec_command description, got %q", description)
		}
	}
}

func TestExecCommandReturnsSessionAndWriteStdinPollsCompletion(t *testing.T) {
	root := t.TempDir()
	manager := newExecSessionManager()
	execTool := NewScopedExecCommandTool(root, nil, manager)
	writeTool := NewWriteStdinTool(manager)

	start := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("sleep"),
		"yield_time_ms": 10,
	})
	if start.Status != StatusOK {
		t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
	}
	if start.Meta["session_id"] == "" {
		t.Fatalf("expected running session metadata, got %#v output=%q", start.Meta, start.Output)
	}
	if !strings.Contains(start.Output, `chars "\u0003"`) {
		t.Fatalf("running session output should explain Ctrl-C cleanup, got %q", start.Output)
	}
	sessionID, err := strconv.Atoi(start.Meta["session_id"])
	if err != nil {
		t.Fatalf("session_id is not numeric: %v", err)
	}

	poll := writeTool.Run(context.Background(), map[string]any{
		"session_id":    sessionID,
		"yield_time_ms": 30000,
	})
	if poll.Status != StatusOK {
		t.Fatalf("write_stdin poll status = %s: %s", poll.Status, poll.Output)
	}
	if !strings.Contains(poll.Output, "woke up") {
		t.Fatalf("expected final command output, got %q", poll.Output)
	}
	if poll.Meta["exit_code"] != "0" {
		t.Fatalf("expected exit_code 0, got %#v", poll.Meta)
	}
}

func TestExecCommandRequireEscalatedBypassesNativeSandboxAfterApproval(t *testing.T) {
	root := t.TempDir()
	manager := newExecSessionManager()
	registry := NewRegistry()
	registry.Register(NewScopedExecCommandTool(root, nil, manager))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
		Backend: sandbox.Backend{
			Name:            sandbox.BackendLinuxBwrap,
			Available:       true,
			Executable:      "/nonexistent/zero-linux-sandbox-stub",
			CommandWrapping: true,
			NativeIsolation: true,
		},
	})

	result := registry.RunWithOptions(context.Background(), ExecCommandToolName, map[string]any{
		"cmd":                 helperCommand("success"),
		"sandbox_permissions": string(SandboxPermissionsRequireEscalated),
	}, RunOptions{
		PermissionGranted: true,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
	})

	if result.Status != StatusOK || !strings.Contains(result.Output, "hello from bash") {
		t.Fatalf("expected approved require_escalated exec_command to run direct, got %s: %q", result.Status, result.Output)
	}
	if result.Meta["sandbox_wrapped"] == "true" {
		t.Fatalf("require_escalated exec_command must not be wrapped; meta=%#v", result.Meta)
	}
}

// TestExecCommandRequireEscalatedBypassesMsysGuardAfterApproval mirrors
// TestBashToolRequireEscalatedMsysGuard for exec_command: the MSYS sandbox
// guard exists only because MSYS/Cygwin coreutils fail under the
// write-restricted sandbox, so once require_escalated is actually approved
// (unsandboxed execution), the guard must not block the same command it was
// meant to let escalate past.
func TestExecCommandRequireEscalatedBypassesMsysGuardAfterApproval(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only MSYS sandbox guard")
	}
	root := t.TempDir()
	manager := newExecSessionManager()
	registry := NewRegistry()
	registry.Register(NewScopedExecCommandTool(root, nil, manager))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
		Backend:       sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "native sandbox unavailable"},
	})

	result := registry.RunWithOptions(context.Background(), ExecCommandToolName, map[string]any{
		"cmd":                 "cat somefile.txt",
		"sandbox_permissions": string(SandboxPermissionsRequireEscalated),
	}, RunOptions{
		PermissionGranted: true,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
	})

	// Assert on the preflight block sentinel (exit_code "-1", set only by
	// shellIssueBlockResult) rather than shell_issue: once the guard is
	// bypassed, "cat somefile.txt" actually runs, and its real,
	// PATH-dependent output could otherwise trip the unrelated
	// post-execution detectShellOutputIssue heuristic and make this
	// assertion flaky for reasons unrelated to the guard under test.
	if result.Meta["exit_code"] == "-1" {
		t.Fatalf("expected approved require_escalated to bypass the MSYS guard, got blocked: %#v", result)
	}
}

func TestExecCommandReturnsExitCodeWhenCommandCompletesDuringInitialYield(t *testing.T) {
	root := t.TempDir()
	manager := newExecSessionManager()
	execTool := NewScopedExecCommandTool(root, nil, manager)

	result := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("success"),
		"yield_time_ms": 30000,
	})
	if result.Status != StatusOK {
		t.Fatalf("exec_command status = %s: %s", result.Status, result.Output)
	}
	if result.Meta["session_id"] != "" {
		t.Fatalf("completed command must not return session_id, got %#v", result.Meta)
	}
	if result.Meta["exit_code"] != "0" {
		t.Fatalf("exit_code = %#v, want 0", result.Meta)
	}
	if manager.len() != 0 {
		t.Fatalf("completed command should be removed immediately, manager has %d sessions", manager.len())
	}
}

func TestExecCommandForegroundServerReturnsSessionAndServesHTTP(t *testing.T) {
	root := t.TempDir()
	manager := newExecSessionManager()
	execTool := NewScopedExecCommandTool(root, nil, manager)
	writeTool := NewWriteStdinTool(manager)

	start := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("http-server"),
		"yield_time_ms": 500,
	})
	if start.Status != StatusOK {
		t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
	}
	sessionID, err := strconv.Atoi(start.Meta["session_id"])
	if err != nil {
		t.Fatalf("foreground server should return session_id, meta=%#v output=%q", start.Meta, start.Output)
	}
	addr := parseListeningAddress(start.Output)
	if addr == "" {
		t.Fatalf("server output did not include listening address: %q", start.Output)
	}
	t.Cleanup(func() {
		writeTool.Run(context.Background(), map[string]any{
			"session_id": sessionID,
			"chars":      "\u0003",
		})
	})

	response, err := http.Get("http://" + addr)
	if err != nil {
		t.Fatalf("foreground exec server was not reachable at %s: %v; output=%q", addr, err, start.Output)
	}
	defer response.Body.Close()
	bytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "zero-server-ok" {
		t.Fatalf("server response = %q", string(bytes))
	}
}

func parseListeningAddress(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for index, field := range fields {
			if field == "listening" && index+1 < len(fields) {
				return strings.TrimSpace(fields[index+1])
			}
		}
	}
	return ""
}

func TestExecCommandReapsFinishedUnpolledSession(t *testing.T) {
	root := t.TempDir()
	manager := newExecSessionManager()
	manager.completedRetention = 10 * time.Millisecond
	execTool := NewScopedExecCommandTool(root, nil, manager)

	start := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("sleep"),
		"yield_time_ms": 10,
	})
	if start.Status != StatusOK {
		t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
	}
	sessionID, err := strconv.Atoi(start.Meta["session_id"])
	if err != nil {
		t.Fatalf("session_id is not numeric: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := manager.get(sessionID); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %d was not reaped; manager has %d sessions", sessionID, manager.len())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStopAllWaitsForSessionsToExit(t *testing.T) {
	manager := newExecSessionManager()
	release := make(chan struct{})
	returned := make(chan struct{})
	cancelled := make(chan struct{})
	session := &execSession{
		id:     1000,
		output: newExecOutputBuffer(),
		done:   make(chan struct{}),
	}
	session.cancel = func() {
		close(cancelled)
		go func() {
			<-release
			session.markDone(nil, -1)
		}()
	}
	manager.sessions[session.id] = session

	go func() {
		manager.stopAll()
		close(returned)
	}()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("stopAll did not terminate the session")
	}
	select {
	case <-returned:
		t.Fatal("stopAll returned before session.done closed")
	default:
	}
	close(release)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("stopAll did not return after session.done closed")
	}
}

func TestStoreEvictsLiveSessionWithExplanation(t *testing.T) {
	manager := newExecSessionManager()
	manager.maxSessions = 9
	evicted := &execSession{
		id:         1000,
		startedAt:  time.Unix(1000, 0),
		lastUsedAt: time.Unix(1000, 0),
		output:     newExecOutputBuffer(),
		done:       make(chan struct{}),
	}
	evictedDone := make(chan struct{})
	evicted.cancel = func() { close(evictedDone) }
	manager.sessions[evicted.id] = evicted
	for id := 1001; id <= 1008; id++ {
		manager.sessions[id] = &execSession{
			id:         id,
			startedAt:  time.Unix(int64(id), 0),
			lastUsedAt: time.Unix(int64(id), 0),
			output:     newExecOutputBuffer(),
			done:       make(chan struct{}),
		}
	}

	manager.store(&execSession{
		id:         1009,
		startedAt:  time.Unix(1009, 0),
		lastUsedAt: time.Unix(1009, 0),
		output:     newExecOutputBuffer(),
		done:       make(chan struct{}),
	})

	select {
	case <-evictedDone:
	case <-time.After(time.Second):
		t.Fatal("live pruned session was not terminated")
	}
	if got := evicted.output.recentString(); !strings.Contains(got, "session evicted") {
		t.Fatalf("evicted session output = %q, want explanation", got)
	}
	if _, ok := manager.get(evicted.id); !ok {
		t.Fatal("evicted live session should remain visible until its reaper removes it")
	}
}

func TestStartExecProcessFallsBackAfterPTYStartMutation(t *testing.T) {
	original := startPTYProcessFunc
	t.Cleanup(func() { startPTYProcessFunc = original })
	startPTYProcessFunc = func(command *exec.Cmd, _ *execOutputBuffer) (io.WriteCloser, func(), error) {
		command.SysProcAttr = &syscall.SysProcAttr{}
		command.Cancel = func() error { return nil }
		command.WaitDelay = time.Second
		return nil, nil, errors.New("pty start failed")
	}

	command := exec.CommandContext(context.Background(), os.Args[0], "--zero-bash-helper", "success")
	output := newExecOutputBuffer()
	stdin, tty, cleanup, err := startExecProcess(command, output, true)
	if err != nil {
		t.Fatalf("startExecProcess fallback failed: %v", err)
	}
	if tty {
		t.Fatal("fallback process must report tty=false")
	}
	_ = stdin.Close()
	if err := command.Wait(); err != nil {
		t.Fatalf("fallback command wait failed: %v", err)
	}
	cleanup()
	if got := output.drainString(); !strings.Contains(got, "hello from bash") {
		t.Fatalf("fallback output = %q", got)
	}
}

func TestStartExecProcessPTYFallbackPreservesSysProcAttr(t *testing.T) {
	original := startPTYProcessFunc
	t.Cleanup(func() { startPTYProcessFunc = original })
	startPTYProcessFunc = func(command *exec.Cmd, _ *execOutputBuffer) (io.WriteCloser, func(), error) {
		return nil, nil, errors.New("pty start failed")
	}

	command := exec.CommandContext(context.Background(), os.Args[0], "--zero-bash-helper", "success")
	customAttr := &syscall.SysProcAttr{}
	command.SysProcAttr = customAttr

	output := newExecOutputBuffer()
	stdin, tty, cleanup, err := startExecProcess(command, output, true)
	if err != nil {
		t.Fatalf("startExecProcess fallback failed: %v", err)
	}
	if tty {
		t.Fatal("fallback process must report tty=false")
	}
	_ = stdin.Close()
	_ = command.Wait()
	cleanup()

	if command.SysProcAttr != customAttr {
		t.Fatalf("SysProcAttr was not preserved: got %+v, want %+v", command.SysProcAttr, customAttr)
	}
}

// TestExecOutputBufferCapsUndrainedData: a session nobody polls must not grow
// its undrained buffer without bound — a long-lived background process that
// keeps writing while unpolled previously ran a session's memory into the
// tens of gigabytes and got the whole zero process OOM-killed by the OS.
func TestExecOutputBufferCapsUndrainedData(t *testing.T) {
	buffer := newExecOutputBuffer()

	chunk := strings.Repeat("x", 1024)
	writes := (maxExecOutputBufferBytes / len(chunk)) + 10 // comfortably over the cap
	for i := 0; i < writes; i++ {
		if _, err := buffer.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	buffer.mu.Lock()
	dataLen := len(buffer.data)
	buffer.mu.Unlock()
	if dataLen > maxExecOutputBufferBytes {
		t.Fatalf("undrained buffer grew to %d bytes, want <= %d", dataLen, maxExecOutputBufferBytes)
	}

	out := buffer.drainString()
	// drainString itself carries no truncation marker: a marker embedded in
	// the drained text would always sit ~maxExecOutputBufferBytes before the
	// end of the string, past any realistic head/tail truncation window a
	// caller applies afterward (see execToolResult), so it's reliably lost.
	// The signal lives out of band on consumeTruncated/peekTruncated instead.
	if !strings.HasSuffix(out, chunk) {
		t.Fatal("drained output should keep the most recent bytes, not the oldest")
	}
	if len(out) > maxExecOutputBufferBytes {
		t.Fatalf("drained output = %d bytes, want <= %d (no marker text mixed in)", len(out), maxExecOutputBufferBytes)
	}

	if !buffer.peekTruncated() {
		t.Fatal("peekTruncated should report the overflow without clearing it")
	}
	if !buffer.peekTruncated() {
		t.Fatal("a second peekTruncated call should still report it — peek must not consume")
	}
	if !buffer.consumeTruncated() {
		t.Fatal("consumeTruncated should report the overflow")
	}
	if buffer.consumeTruncated() {
		t.Fatal("consumeTruncated should reset after being read once")
	}
	if buffer.peekTruncated() {
		t.Fatal("peekTruncated should reflect the reset state after consumeTruncated")
	}

	// A second drain with no intervening writes must be empty.
	if got := buffer.drainString(); got != "" {
		t.Fatalf("drainString after a full drain = %q, want empty", got)
	}
}

// TestExecToolResultSurfacesBufferTruncationOutsideByteBudget: an earlier
// version embedded the truncation notice directly in the drained text, which
// a review found sits ~maxExecOutputBufferBytes before the end of the
// string — far past the head/tail window truncateExecOutput keeps even at
// the tool's smallest allowed max_output_tokens, so the notice was reliably
// swallowed or chopped. The notice must survive regardless of how small the
// byte budget is, and it must not count against that budget.
func TestExecToolResultSurfacesBufferTruncationOutsideByteBudget(t *testing.T) {
	hugeOutput := strings.Repeat("y", maxExecOutputBufferBytes+1024)

	result := execToolResult(execToolResultInput{
		commandText:           "cmd",
		output:                hugeOutput,
		outputBufferTruncated: true,
		sessionID:             1,
		exited:                false,
		maxOutputTokens:       1, // the schema's own declared minimum
	})

	if !strings.Contains(result.Output, execOutputBufferTruncatedMessage) {
		t.Fatalf("result output should contain the buffer-truncation notice even at the smallest max_output_tokens, got %q", result.Output[:min(len(result.Output), 200)])
	}
	if result.Meta["output_buffer_truncated"] != "true" {
		t.Fatalf("result meta should flag output_buffer_truncated, got %#v", result.Meta)
	}
	if !result.Truncated {
		t.Fatal("result should report Truncated when the buffer dropped output")
	}
}

// TestCollectRespectsDeadlineUnderContinuousOutput asserts collect() returns
// close to its requested deadline even while output keeps arriving. Before
// the corresponding fix, the deadline was only checked in the branch reached
// when drainString returns empty — a background process producing output
// fast/continuously enough to keep the buffer perpetually non-empty could
// theoretically starve that check indefinitely. This synthetic writer
// (8 goroutines, tight loop) did not reliably reproduce that starvation under
// Go's scheduler in practice — the reader still won the race often enough —
// so this test doesn't prove the old code could hang; it just pins down the
// intended behavior (bounded by wait) going forward.
func TestCollectRespectsDeadlineUnderContinuousOutput(t *testing.T) {
	session := &execSession{
		id:     1000,
		output: newExecOutputBuffer(),
		done:   make(chan struct{}),
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Several concurrent writers, each pushing a decent-sized chunk in a tight
	// loop: a single slow writer lets the reader win the race often enough
	// (the runtime schedules a gap between writes) to mask the bug. Enough
	// parallel writers keep the buffer non-empty essentially continuously,
	// closer to a real chatty process whose PTY reads land in bursts.
	const writers = 8
	chunk := []byte(strings.Repeat("x", 256))
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					session.output.Write(chunk)
				}
			}
		}()
	}
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})

	const wait = 200 * time.Millisecond
	start := time.Now()
	_, _ = session.collect(context.Background(), wait)
	elapsed := time.Since(start)

	// Generous slack over `wait` for scheduling jitter under a continuously
	// writing goroutine (worse on Windows CI under load). This must stay a
	// small multiple of wait, not "however long the writer keeps going"
	// (which is what the bug produced: this test would hang past the 30s
	// test timeout without the fix).
	if elapsed > 5*wait {
		t.Fatalf("collect took %v under continuous output, want close to the %v deadline", elapsed, wait)
	}
}

// resilientTempDir is like t.TempDir() but tolerates the Windows handle-release
// lag: a SIGKILL'd child process that had the dir as its cwd may not have
// released it the instant it is reaped, so the immediate RemoveAll t.TempDir()
// does on cleanup can fail with "being used by another process". Retry the
// removal briefly before giving up.
func resilientTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "zero-exec-interrupt-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			if time.Now().After(deadline) {
				// Best-effort: a leaked temp dir is not worth failing the test.
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	return dir
}

func TestWriteStdinInterruptTerminatesSession(t *testing.T) {
	root := resilientTempDir(t)
	manager := newExecSessionManager()
	execTool := NewScopedExecCommandTool(root, nil, manager)
	writeTool := NewWriteStdinTool(manager)

	start := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("long-sleep"),
		"yield_time_ms": 10,
	})
	if start.Status != StatusOK {
		t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
	}
	sessionID, err := strconv.Atoi(start.Meta["session_id"])
	if err != nil {
		t.Fatalf("session_id is not numeric: %v", err)
	}

	// Capture the live session BEFORE the interrupt so we can wait on its done
	// channel afterwards (write_stdin removes a finished session from the
	// manager, so manager.get would miss it post-interrupt).
	session, ok := manager.get(sessionID)
	if !ok {
		t.Fatalf("session %d not found after start", sessionID)
	}

	// The operation under test: write_stdin "\x03" must itself terminate the
	// session (exec_command.go's Ctrl-C branch). This is what the regression
	// guards — terminating the session here directly would let the test pass even
	// if that branch were deleted.
	interrupted := writeTool.Run(context.Background(), map[string]any{
		"session_id":    sessionID,
		"chars":         "\x03",
		"yield_time_ms": 1000,
	})

	// De-flake: wait deterministically for the process to be reaped rather than
	// relying on the 1000ms yield window being long enough for the async
	// SIGKILL + reap to land (which flaked on slow CI, notably Windows smoke). A
	// generous safety timeout fails loudly if the kill genuinely hangs; the
	// common case returns the instant the process exits.
	select {
	case <-session.done:
	case <-time.After(30 * time.Second):
		t.Fatalf("interrupted session %d was not reaped within 30s", sessionID)
	}

	if interrupted.Status != StatusOK {
		t.Fatalf("interrupted session status = %s: %s", interrupted.Status, interrupted.Output)
	}
	if interrupted.Meta["session_id"] != "" {
		t.Fatalf("interrupted session should not remain running, meta=%#v output=%q", interrupted.Meta, interrupted.Output)
	}
	if interrupted.Meta["exit_code"] == "" {
		t.Fatalf("interrupted session should report exit_code, meta=%#v output=%q", interrupted.Meta, interrupted.Output)
	}
	if interrupted.Meta["interrupted"] != "true" {
		t.Fatalf("interrupted session should report interrupted metadata, meta=%#v output=%q", interrupted.Meta, interrupted.Output)
	}
}

func TestWriteStdinRejectsInputForNonTTYSession(t *testing.T) {
	root := t.TempDir()
	manager := newExecSessionManager()
	execTool := NewScopedExecCommandTool(root, nil, manager)
	writeTool := NewWriteStdinTool(manager)

	start := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("long-sleep"),
		"yield_time_ms": 10,
	})
	if start.Status != StatusOK {
		t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
	}
	sessionID, err := strconv.Atoi(start.Meta["session_id"])
	if err != nil {
		t.Fatalf("session_id is not numeric: %v", err)
	}

	result := writeTool.Run(context.Background(), map[string]any{
		"session_id":    sessionID,
		"chars":         "hello\n",
		"yield_time_ms": 10,
	})
	if result.Status != StatusError {
		t.Fatalf("write_stdin status = %s, want error", result.Status)
	}
	if !strings.Contains(result.Output, "does not accept stdin") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	manager.stop(sessionID)
}

func TestWriteStdinStopIntentTerminatesNonTTYSession(t *testing.T) {
	for _, chars := range []string{`\u0003`, "exit\n"} {
		root := t.TempDir()
		manager := newExecSessionManager()
		execTool := NewScopedExecCommandTool(root, nil, manager)
		writeTool := NewWriteStdinTool(manager)

		start := execTool.Run(context.Background(), map[string]any{
			"cmd":           helperCommand("long-sleep"),
			"yield_time_ms": 10,
		})
		if start.Status != StatusOK {
			t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
		}
		sessionID, err := strconv.Atoi(start.Meta["session_id"])
		if err != nil {
			t.Fatalf("session_id is not numeric: %v", err)
		}

		result := writeTool.Run(context.Background(), map[string]any{
			"session_id":    sessionID,
			"chars":         chars,
			"yield_time_ms": 1000,
		})
		if result.Status != StatusOK {
			t.Fatalf("stop input %q status = %s: %s", chars, result.Status, result.Output)
		}
		if result.Meta["session_id"] != "" {
			t.Fatalf("stop input %q should not leave session running, meta=%#v output=%q", chars, result.Meta, result.Output)
		}
		if result.Meta["exit_code"] == "" {
			t.Fatalf("stop input %q should report exit_code, meta=%#v output=%q", chars, result.Meta, result.Output)
		}
		if result.Meta["interrupted"] != "true" {
			t.Fatalf("stop input %q should report interrupted metadata, meta=%#v output=%q", chars, result.Meta, result.Output)
		}
	}
}

func TestShouldInterruptExecSession(t *testing.T) {
	cases := []struct {
		chars string
		tty   bool
		want  bool
	}{
		{chars: "\x03", tty: false, want: true},
		{chars: `\u0003`, tty: false, want: true},
		{chars: `\\u0003`, tty: false, want: true},
		{chars: "^C", tty: false, want: true},
		{chars: "ctrl-c", tty: false, want: true},
		{chars: "control-c", tty: false, want: true},
		{chars: "sigint", tty: false, want: true},
		{chars: "interrupt", tty: false, want: true},
		{chars: "q", tty: false, want: true},
		{chars: "quit", tty: false, want: true},
		{chars: "exit\n", tty: false, want: true},
		{chars: "stop", tty: false, want: true},
		{chars: "kill", tty: false, want: true},
		{chars: "terminate", tty: false, want: true},
		{chars: "exit\n", tty: true, want: false},
		{chars: "quit", tty: true, want: false},
		{chars: "hello\n", tty: false, want: false},
		{chars: "hello\n", tty: true, want: false},
	}
	for _, tc := range cases {
		if got := shouldInterruptExecSession(tc.chars, tc.tty); got != tc.want {
			t.Fatalf("shouldInterruptExecSession(%q, tty=%v) = %v, want %v", tc.chars, tc.tty, got, tc.want)
		}
	}
}

func TestExecCommandTTYSessionAcceptsInputOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pty transport is currently implemented for linux")
	}
	root := t.TempDir()
	manager := newExecSessionManager()
	execTool := NewScopedExecCommandTool(root, nil, manager)
	writeTool := NewWriteStdinTool(manager)

	start := execTool.Run(context.Background(), map[string]any{
		"cmd":           "read line; echo got:$line",
		"tty":           true,
		"yield_time_ms": 10,
	})
	if start.Status != StatusOK {
		t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
	}
	if start.Meta["tty"] != "true" {
		t.Fatalf("expected tty metadata, got %#v output=%q", start.Meta, start.Output)
	}
	sessionID, err := strconv.Atoi(start.Meta["session_id"])
	if err != nil {
		t.Fatalf("session_id is not numeric: %v", err)
	}

	result := writeTool.Run(context.Background(), map[string]any{
		"session_id":    sessionID,
		"chars":         "hello\n",
		"yield_time_ms": 1000,
	})
	if result.Status != StatusOK {
		t.Fatalf("write_stdin status = %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "got:hello") {
		t.Fatalf("expected PTY input output, got %q", result.Output)
	}
	if result.Meta["exit_code"] != "0" {
		t.Fatalf("expected exited session, got meta=%#v output=%q", result.Meta, result.Output)
	}
}

func TestExecSessionSnapshotsAndStopAll(t *testing.T) {
	root := t.TempDir()
	manager := newExecSessionManager()
	execTool := NewScopedExecCommandTool(root, nil, manager).(execCommandTool)

	start := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("long-sleep"),
		"yield_time_ms": 10,
	})
	if start.Status != StatusOK {
		t.Fatalf("exec_command start status = %s: %s", start.Status, start.Output)
	}
	sessionID, err := strconv.Atoi(start.Meta["session_id"])
	if err != nil {
		t.Fatalf("session_id is not numeric: %v", err)
	}

	snapshots := execTool.ExecSessions()
	if len(snapshots) != 1 {
		t.Fatalf("expected one session snapshot, got %#v", snapshots)
	}
	if snapshots[0].ID != sessionID || snapshots[0].Command == "" || snapshots[0].Status != "running" {
		t.Fatalf("unexpected snapshot: %#v", snapshots[0])
	}

	stopped := execTool.StopAllExecSessions()
	if len(stopped) != 1 || stopped[0] != sessionID {
		t.Fatalf("StopAllExecSessions = %#v, want [%d]", stopped, sessionID)
	}
}

func TestWriteStdinPermissionForArgs(t *testing.T) {
	tool := NewWriteStdinTool(newExecSessionManager()).(writeStdinTool)
	for _, args := range []map[string]any{
		{"session_id": 1},
		{"session_id": 1, "chars": ""},
		{"session_id": 1, "chars": "\x03"},
	} {
		if got := tool.PermissionForArgs(args); got != PermissionAllow {
			t.Fatalf("PermissionForArgs(%#v) = %s, want allow", args, got)
		}
	}
	if got := tool.PermissionForArgs(map[string]any{"session_id": 1, "chars": "exit\n"}); got != PermissionPrompt {
		t.Fatalf("non-empty stdin PermissionForArgs = %s, want prompt", got)
	}
}

func TestRegistryHonorsWriteStdinArgumentPermission(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewWriteStdinTool(newExecSessionManager()))

	poll := registry.Run(context.Background(), WriteStdinToolName, map[string]any{"session_id": 9999})
	if poll.Status != StatusError || !strings.Contains(poll.Output, "still-running exec_command") {
		t.Fatalf("empty poll should reach tool without permission prompt, got status=%s output=%q", poll.Status, poll.Output)
	}

	send := registry.Run(context.Background(), WriteStdinToolName, map[string]any{
		"session_id": 9999,
		"chars":      "exit\n",
	})
	if send.Status != StatusError || !strings.Contains(send.Output, "Permission required for write_stdin") {
		t.Fatalf("non-empty stdin should require permission, got status=%s output=%q", send.Status, send.Output)
	}
}

func TestWriteStdinReportsUnknownSession(t *testing.T) {
	result := NewWriteStdinTool(newExecSessionManager()).Run(context.Background(), map[string]any{
		"session_id": 1234,
	})
	if result.Status != StatusError {
		t.Fatalf("status = %s, want error", result.Status)
	}
	// The message must lead with recovery guidance and still identify the id.
	if !strings.Contains(result.Output, "still-running exec_command") {
		t.Fatalf("unknown-session error must carry recovery guidance, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "1234") {
		t.Fatalf("unknown-session error must name the offending id, got: %q", result.Output)
	}
}

// The runtime rejects session_id < 1 (intArg min 1); the advertised schema must
// say the same so a model sees the constraint before it probes id 0.
func TestWriteStdinSchemaPinsSessionIDMinimum(t *testing.T) {
	schema := NewWriteStdinTool(nil).(writeStdinTool).parameters
	prop, ok := schema.Properties["session_id"]
	if !ok {
		t.Fatal("session_id property missing from write_stdin schema")
	}
	if prop.Minimum == nil || *prop.Minimum != 1 {
		t.Fatalf("session_id schema Minimum = %v, want 1 to match the runtime floor", prop.Minimum)
	}
}

func TestWriteStdinRequiresPositiveSessionID(t *testing.T) {
	tool := NewWriteStdinTool(newExecSessionManager())
	for _, args := range []map[string]any{
		{},
		{"session_id": 0},
	} {
		result := tool.Run(context.Background(), args)
		if result.Status != StatusError {
			t.Fatalf("Run(%#v) status = %s, want error", args, result.Status)
		}
		if !strings.Contains(result.Output, "Invalid arguments for write_stdin") {
			t.Fatalf("Run(%#v) output = %q, want invalid arguments", args, result.Output)
		}
	}
}

func TestTruncateExecOutputPreservesUTF8(t *testing.T) {
	output := strings.Repeat("界", 20)
	truncated, ok := truncateExecOutput(output, 2)
	if !ok {
		t.Fatal("expected output to truncate")
	}
	if !strings.Contains(truncated, "[zero] output truncated") {
		t.Fatalf("missing truncation marker: %q", truncated)
	}
	if !utf8.ValidString(truncated) {
		t.Fatalf("truncated output is not valid UTF-8: %q", truncated)
	}
}

func TestExecSessionPruneDoesNotRaceTouch(t *testing.T) {
	// AUDIT-L15: the prune comparator read execSession.lastUsedAt under manager.mu
	// while touch() writes it under session.mu — a data race on a time.Time. Drive
	// both concurrently under -race; with the snapshot-under-session.mu fix it is
	// clean, without it the race detector flags lastUsedAt.
	mgr := newExecSessionManager()
	for i := 0; i < 12; i++ {
		s := &execSession{id: 1000 + i, lastUsedAt: time.Now(), done: make(chan struct{})}
		mgr.sessions[s.id] = s
	}
	target := mgr.sessions[1000]

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for {
			select {
			case <-stop:
				return
			default:
				target.touch()
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		mgr.mu.Lock()
		_ = mgr.sessionToPruneLocked()
		mgr.mu.Unlock()
	}
	close(stop)
	writer.Wait()
}
