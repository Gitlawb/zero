package hooks

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func beforeToolConfig(hooks ...Definition) Config {
	return Config{Enabled: true, Hooks: hooks}
}

func TestDispatchRunsMatchingHooksAndRecordsAudit(t *testing.T) {
	var calls []string
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		calls = append(calls, command)
		return commandResult{ExitCode: 0, Stdout: "ok"}
	}
	audit, err := NewAuditStore(AuditStoreOptions{AuditPath: filepath.Join(t.TempDir(), "audit.jsonl")})
	if err != nil {
		t.Fatalf("NewAuditStore: %v", err)
	}
	config := beforeToolConfig(
		Definition{ID: "h1", Event: EventBeforeTool, Matcher: "bash", Command: "guard", Enabled: true},
		Definition{ID: "h2", Event: EventBeforeTool, Command: "log", Enabled: true}, // no matcher = always
		Definition{ID: "h3", Event: EventBeforeTool, Matcher: "read_file", Command: "skip", Enabled: true},
	)
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, Audit: audit, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash", ToolCallID: "call_1"})
	if outcome.Blocked {
		t.Fatalf("unexpected block: %#v", outcome)
	}
	if outcome.Ran != 2 {
		t.Fatalf("Ran = %d, want 2 (h1 matcher + h2 unmatched), calls=%v", outcome.Ran, calls)
	}
	events, err := audit.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	started, completed := 0, 0
	for _, event := range events {
		switch event.Type {
		case "hook_execution_started":
			started++
		case "hook_execution_completed":
			completed++
			if event.Status != AuditCompleted {
				t.Fatalf("status = %q, want completed", event.Status)
			}
		}
	}
	if started != 2 || completed != 2 {
		t.Fatalf("audit events: started=%d completed=%d, want 2/2", started, completed)
	}
}

func TestDispatchBeforeToolBlocksOnNonZeroExitAndStops(t *testing.T) {
	ran := 0
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		ran++
		if command == "deny" {
			return commandResult{ExitCode: 2, Stderr: "policy violation"}
		}
		return commandResult{ExitCode: 0}
	}
	config := beforeToolConfig(
		Definition{ID: "deny", Event: EventBeforeTool, Command: "deny", Enabled: true},
		Definition{ID: "after-deny", Event: EventBeforeTool, Command: "second", Enabled: true},
	)
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"})
	if !outcome.Blocked || outcome.BlockedBy != "deny" {
		t.Fatalf("outcome = %#v, want blocked by deny", outcome)
	}
	if outcome.Reason != "policy violation" {
		t.Fatalf("reason = %q, want hook stderr", outcome.Reason)
	}
	if ran != 1 {
		t.Fatalf("ran %d hooks, want 1 (must stop after the first veto)", ran)
	}
}

func TestDispatchNonBlockingEventDoesNotVetoOnNonZero(t *testing.T) {
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		return commandResult{ExitCode: 1, Stderr: "noisy"}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "notify", Event: EventAfterTool, Command: "notify", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "bash"})
	if outcome.Blocked {
		t.Fatalf("afterTool must not block: %#v", outcome)
	}
	if outcome.Ran != 1 {
		t.Fatalf("Ran = %d, want 1", outcome.Ran)
	}
}

func TestDispatchSkipsWhenDisabledOrUnmatched(t *testing.T) {
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		t.Fatal("runner must not be called")
		return commandResult{}
	}
	disabled := Config{Enabled: false, Hooks: []Definition{{ID: "h", Event: EventBeforeTool, Command: "x", Enabled: true}}}
	if outcome := NewDispatcher(DispatcherOptions{Config: disabled, run: runner}).Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"}); outcome.Ran != 0 {
		t.Fatalf("disabled config ran hooks: %#v", outcome)
	}
	unmatched := beforeToolConfig(Definition{ID: "h", Event: EventBeforeTool, Matcher: "read_file", Command: "x", Enabled: true})
	if outcome := NewDispatcher(DispatcherOptions{Config: unmatched, run: runner}).Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"}); outcome.Ran != 0 {
		t.Fatalf("unmatched matcher ran hooks: %#v", outcome)
	}
}

func TestDispatchDeliversJSONPayloadOnStdin(t *testing.T) {
	var gotStdin string
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		gotStdin = string(stdin)
		return commandResult{ExitCode: 0}
	}
	config := beforeToolConfig(Definition{ID: "h", Event: EventBeforeTool, Command: "x", Enabled: true})
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	dispatcher.Dispatch(context.Background(), DispatchInput{
		Event:    EventBeforeTool,
		ToolName: "bash",
		Payload:  map[string]any{"tool": "bash", "args": map[string]any{"command": "ls"}},
	})
	if !strings.Contains(gotStdin, `"tool":"bash"`) || !strings.Contains(gotStdin, `"command":"ls"`) {
		t.Fatalf("stdin payload = %q, want serialized tool call", gotStdin)
	}
}

func TestExecCommandRunnerCapturesExitAndStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	// Echoes stdin to stderr and exits non-zero so we exercise both paths.
	result := execCommandRunner(context.Background(), "/bin/sh", []string{"-c", "cat 1>&2; exit 4"}, []byte("payload-123"), t.TempDir(), nil)
	if result.Err != nil {
		t.Fatalf("unexpected launch error: %v", result.Err)
	}
	if result.ExitCode != 4 {
		t.Fatalf("exit code = %d, want 4", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "payload-123") {
		t.Fatalf("stderr = %q, want stdin echoed", result.Stderr)
	}
}

func TestExecCommandRunnerReportsLaunchFailureWithoutBlocking(t *testing.T) {
	result := execCommandRunner(context.Background(), "definitely-not-a-real-binary-zzz", nil, nil, t.TempDir(), nil)
	if result.Err == nil {
		t.Fatal("expected launch error for a missing binary")
	}
	// A launch failure is an error, never a block, even for beforeTool.
	if status, blocked := classifyResult(EventBeforeTool, result); blocked || status != AuditError {
		t.Fatalf("classify = (%q, %v), want (error, false) for a launch failure", status, blocked)
	}
}
