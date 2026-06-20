package tools

import (
	"context"
	"strconv"
	"strings"
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

func TestWriteStdinInterruptTerminatesSession(t *testing.T) {
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

	interrupted := writeTool.Run(context.Background(), map[string]any{
		"session_id":    sessionID,
		"chars":         "\x03",
		"yield_time_ms": 1000,
	})
	if interrupted.Meta["session_id"] != "" {
		t.Fatalf("interrupted session should not remain running, meta=%#v output=%q", interrupted.Meta, interrupted.Output)
	}
	if interrupted.Meta["exit_code"] == "" {
		t.Fatalf("interrupted session should report exit_code, meta=%#v output=%q", interrupted.Meta, interrupted.Output)
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
