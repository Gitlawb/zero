package hooks

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatchAsyncHookDoesNotBlockAndIsCollected(t *testing.T) {
	release := make(chan struct{})
	var ran int32
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		<-release
		atomic.AddInt32(&ran, 1)
		return commandResult{ExitCode: 0, Stdout: "done"}
	}
	audit, err := NewAuditStore(AuditStoreOptions{AuditPath: filepath.Join(t.TempDir(), "audit.jsonl")})
	if err != nil {
		t.Fatalf("NewAuditStore: %v", err)
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "bg", Event: EventAfterTool, Command: "bg", Async: true, Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, Audit: audit, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "edit_file"})

	if atomic.LoadInt32(&ran) != 0 {
		t.Fatal("async hook ran before Dispatch returned; it must not block the turn")
	}
	if outcome.Ran != 0 || outcome.AsyncLaunched != 1 {
		t.Fatalf("outcome = %#v; want Ran=0, AsyncLaunched=1", outcome)
	}

	close(release)
	dispatcher.WaitAsync()

	if atomic.LoadInt32(&ran) != 1 {
		t.Fatal("async hook did not run after release")
	}
	events, err := audit.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	completed := 0
	for _, event := range events {
		if event.Type == "hook_execution_completed" && event.HookID == "bg" {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("async result not collected to audit: %#v", events)
	}
}

func TestDispatchAsyncRewakeSignalsOnBlockingExit(t *testing.T) {
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		return commandResult{ExitCode: 2, Stderr: "tests failing"}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "verifier", Event: EventAfterTool, Command: "verify", AsyncRewake: true, RewakeMessage: "You broke it:", RewakeSummary: "build broken", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "edit_file"})

	select {
	case sig := <-dispatcher.Rewakes():
		if sig.HookID != "verifier" {
			t.Fatalf("HookID = %q", sig.HookID)
		}
		if sig.Summary != "build broken" {
			t.Fatalf("Summary = %q", sig.Summary)
		}
		if !strings.Contains(sig.Message, "You broke it:") || !strings.Contains(sig.Message, "tests failing") {
			t.Fatalf("Message = %q; want prefix + hook output", sig.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rewake signal delivered for asyncRewake exit code 2")
	}
	dispatcher.WaitAsync()
}

func TestDispatchAsyncRewakeNoSignalOnSuccess(t *testing.T) {
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		return commandResult{ExitCode: 0, Stdout: "all good"}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "verifier", Event: EventAfterTool, Command: "verify", AsyncRewake: true, RewakeMessage: "broke:", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "edit_file"})
	dispatcher.WaitAsync()

	select {
	case sig := <-dispatcher.Rewakes():
		t.Fatalf("unexpected rewake on success: %#v", sig)
	default:
	}
}

func TestDispatchAsyncRewakeNoSignalWhenTimedOut(t *testing.T) {
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		// Wait for the per-hook deadline to fire, then return the blocking code:
		// the result is both ExitCode 2 and TimedOut, with no real verdict. Waiting
		// on ctx.Done() makes TimedOut deterministic on every platform (no reliance
		// on a sub-tick timer firing within a synchronous window).
		<-ctx.Done()
		return commandResult{ExitCode: 2, Stderr: "exited 2 while being killed"}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "verifier", Event: EventAfterTool, Command: "verify", AsyncRewake: true, RewakeMessage: "broke:", Enabled: true},
	}}
	// A short timeout the runner waits for, forcing executeAction to mark TimedOut.
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner, Timeout: 10 * time.Millisecond})

	dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "edit_file"})
	dispatcher.WaitAsync()

	select {
	case sig := <-dispatcher.Rewakes():
		t.Fatalf("a timed-out hook produced no verdict and must not wake the model: %#v", sig)
	default:
	}
}

func TestRunAsyncDetachesFromRequestContext(t *testing.T) {
	release := make(chan struct{})
	sawCancelled := make(chan bool, 1)
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		<-release
		sawCancelled <- (ctx.Err() != nil)
		return commandResult{ExitCode: 0}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "bg", Event: EventAfterTool, Command: "bg", Async: true, Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	ctx, cancel := context.WithCancel(context.Background())
	dispatcher.Dispatch(ctx, DispatchInput{Event: EventAfterTool, ToolName: "edit_file"})
	cancel() // cancel the request context while the background hook is mid-run
	close(release)
	dispatcher.WaitAsync()

	if <-sawCancelled {
		t.Fatal("background async hook saw a cancelled context; it must be detached from the request context to outlive the turn")
	}
}

func TestDispatchAfterToolContinueOnBlockFeedsReasonAndContinues(t *testing.T) {
	var ran []string
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		ran = append(ran, command)
		if command == "gate" {
			return commandResult{ExitCode: 2, Stderr: "convention violated"}
		}
		return commandResult{ExitCode: 0}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "gate", Event: EventAfterTool, Command: "gate", ContinueOnBlock: true, Enabled: true},
		{ID: "after", Event: EventAfterTool, Command: "after", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "edit_file"})

	if outcome.EndTurn {
		t.Fatalf("continueOnBlock=true must not end the turn: %#v", outcome)
	}
	if !strings.Contains(outcome.ContinueReason, "convention violated") {
		t.Fatalf("ContinueReason = %q; want the gate's reason fed back", outcome.ContinueReason)
	}
	if strings.Join(ran, ",") != "gate,after" {
		t.Fatalf("ran = %v; want both hooks (turn continues past the block)", ran)
	}
}

func TestDispatchAfterToolBlockWithoutContinueEndsTurn(t *testing.T) {
	var ran []string
	runner := func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
		ran = append(ran, command)
		if command == "gate" {
			return commandResult{ExitCode: 2, Stderr: "stop now"}
		}
		return commandResult{ExitCode: 0}
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "gate", Event: EventAfterTool, Command: "gate", Enabled: true},
		{ID: "after", Event: EventAfterTool, Command: "after", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, run: runner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "edit_file"})

	if !outcome.EndTurn {
		t.Fatalf("a block without continueOnBlock must end the turn: %#v", outcome)
	}
	if !strings.Contains(outcome.Reason, "stop now") {
		t.Fatalf("Reason = %q", outcome.Reason)
	}
	if strings.Join(ran, ",") != "gate" {
		t.Fatalf("ran = %v; want only gate (stop after end-turn block)", ran)
	}
}
