package tools

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

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
