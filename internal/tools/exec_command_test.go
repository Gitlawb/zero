package tools

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
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
		"yield_time_ms": 1000,
	})
	if poll.Status != StatusOK {
		t.Fatalf("write_stdin poll status = %s: %s", poll.Status, poll.Output)
	}
	if poll.Meta["exit_code"] != "0" {
		t.Fatalf("expected shared manager to find completed session, got meta=%#v output=%q", poll.Meta, poll.Output)
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
		"yield_time_ms": 1000,
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

func TestExecCommandReturnsExitCodeWhenCommandCompletesDuringInitialYield(t *testing.T) {
	root := t.TempDir()
	manager := newExecSessionManager()
	execTool := NewScopedExecCommandTool(root, nil, manager)

	result := execTool.Run(context.Background(), map[string]any{
		"cmd":           helperCommand("success"),
		"yield_time_ms": 1000,
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

	// Send the interrupt, then wait deterministically for the process to be
	// reaped (session.done closes) before asserting. The previous version relied
	// on the write_stdin yield window (1000ms) being long enough for the async
	// SIGKILL + reap to land, which flaked on slow CI (notably Windows smoke).
	// Waiting on done removes the timing dependence: a generous safety timeout
	// fails loudly if the kill genuinely hangs, but the common case returns the
	// instant the process exits.
	session, ok := manager.get(sessionID)
	if !ok {
		t.Fatalf("session %d not found after start", sessionID)
	}
	session.terminate()
	select {
	case <-session.done:
	case <-time.After(30 * time.Second):
		t.Fatalf("interrupted session %d was not reaped within 30s", sessionID)
	}

	interrupted := writeTool.Run(context.Background(), map[string]any{
		"session_id":    sessionID,
		"chars":         "\x03",
		"yield_time_ms": 1000,
	})
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
	if poll.Status != StatusError || !strings.Contains(poll.Output, "Unknown exec session_id") {
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
	if !strings.Contains(result.Output, "Unknown exec session_id 1234") {
		t.Fatalf("unexpected output: %q", result.Output)
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
