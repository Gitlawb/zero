package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// defaultHookTimeout bounds a single hook command so a hung or slow hook cannot
// stall the agent indefinitely.
const defaultHookTimeout = 30 * time.Second

// blockingExitCode is the conventional "blocking error" exit code. A beforeTool
// hook already vetoes on any non-zero exit; afterTool hooks use exactly this code
// to signal a block decision (continue-on-block or end-turn), and an asyncRewake
// hook uses it to wake the model.
const blockingExitCode = 2

// RewakeSignal is emitted when a background (asyncRewake) hook exits with the
// blocking code. The loop selects on Dispatcher.Rewakes() and, on receipt, wakes
// the model with a system-reminder built from Message and shows Summary to the user.
type RewakeSignal struct {
	HookID  string
	Summary string
	Message string
}

// DispatchInput describes one lifecycle point at which hooks may run.
type DispatchInput struct {
	Event      Event
	ToolName   string // for beforeTool/afterTool matching
	ToolCallID string
	// Payload is serialized to JSON and written to each hook command's stdin so a
	// hook can inspect the tool call, its result, or session context.
	Payload any
}

// DispatchOutcome reports what happened for one Dispatch call.
type DispatchOutcome struct {
	Ran       int    // hooks that executed
	Blocked   bool   // a blocking-event hook exited non-zero, vetoing the action
	BlockedBy string // ID of the hook that blocked (empty unless Blocked)
	Reason    string // the blocking hook's stderr/stdout, for surfacing to the model
	// Messages collects the output (stdout, else stderr) of each hook that
	// produced any, in run order. afterTool validators use this to feed results
	// (e.g. a formatter diff or vet warning) back to the model on the tool result.
	Messages []string
	// AsyncLaunched counts hooks started in the background (not awaited).
	AsyncLaunched int
	// ContinueReason is set when an afterTool hook signalled a block with
	// ContinueOnBlock=true: its reason is fed back to the model and the turn continues.
	ContinueReason string
	// EndTurn is set when an afterTool hook signalled a block without
	// ContinueOnBlock: the turn should end (Reason carries why).
	EndTurn bool
}

type commandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error // set when the command could not be executed (not a non-zero exit)
	TimedOut bool  // the hook started but its deadline/cancellation fired before it returned
}

// commandRunner executes one hook command. It is injectable so the dispatch
// logic can be tested without spawning processes.
type commandRunner func(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult

// DispatcherOptions configures a Dispatcher.
type DispatcherOptions struct {
	Config  Config
	Audit   *AuditStore   // optional; when set, every run is recorded
	Cwd     string        // working directory for hook commands
	Env     []string      // extra environment entries appended to the process env
	Timeout time.Duration // per-command timeout (defaults to defaultHookTimeout)
	// PromptRunner evaluates "prompt" action hooks; nil makes them a non-blocking
	// error. AllowedHTTPURLs is the exact-match allowlist for "http" action hooks;
	// a URL not on it is rejected before any request is made. HTTPClient is
	// injectable for tests (defaults to http.DefaultClient).
	PromptRunner    PromptRunner
	AllowedHTTPURLs []string
	HTTPClient      *http.Client
	now             func() time.Time
	run             commandRunner
}

// Dispatcher selects and runs the hooks configured for a lifecycle event,
// recording each run to the audit store. A beforeTool hook that exits non-zero
// blocks the tool; hooks for other events are advisory (failures are recorded
// but do not interrupt the run).
type Dispatcher struct {
	config          Config
	audit           *AuditStore
	cwd             string
	env             []string
	timeout         time.Duration
	promptRunner    PromptRunner
	allowedHTTPURLs map[string]bool
	httpClient      *http.Client
	now             func() time.Time
	run             commandRunner
	rewakes         chan RewakeSignal
	wg              sync.WaitGroup
}

// Rewakes returns the channel on which background asyncRewake hooks deliver
// wake-the-model signals. The loop should select on it between turns.
func (dispatcher *Dispatcher) Rewakes() <-chan RewakeSignal {
	if dispatcher == nil {
		return nil
	}
	return dispatcher.rewakes
}

// WaitAsync blocks until every background hook launched so far has finished. The
// loop drains async hooks at session end; tests use it to observe their results.
// It must be called after the final Dispatch, not concurrently with one: like any
// sync.WaitGroup, a Dispatch that adds from a zero counter must not race a Wait.
func (dispatcher *Dispatcher) WaitAsync() {
	if dispatcher == nil {
		return
	}
	dispatcher.wg.Wait()
}

// NewDispatcher builds a Dispatcher. A zero/empty config yields a dispatcher that
// runs nothing, so callers can always construct one unconditionally.
func NewDispatcher(options DispatcherOptions) *Dispatcher {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	now := options.now
	if now == nil {
		now = time.Now
	}
	run := options.run
	if run == nil {
		run = execCommandRunner
	}
	allowed := map[string]bool{}
	for _, raw := range options.AllowedHTTPURLs {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	return &Dispatcher{
		config:          options.Config,
		audit:           options.Audit,
		cwd:             options.Cwd,
		env:             options.Env,
		timeout:         timeout,
		promptRunner:    options.PromptRunner,
		allowedHTTPURLs: allowed,
		httpClient:      options.HTTPClient,
		now:             now,
		run:             run,
		rewakes:         make(chan RewakeSignal, 16),
	}
}

// blocksOn reports whether a non-zero exit for this event should veto the action.
// Only beforeTool gates the tool; other events are observational.
func blocksOn(event Event) bool {
	return event == EventBeforeTool
}

// Dispatch runs every enabled hook configured for the input event (and matcher,
// for tool events). It returns once all hooks have run, or early if a blocking
// hook vetoes the action.
func (dispatcher *Dispatcher) Dispatch(ctx context.Context, input DispatchInput) DispatchOutcome {
	outcome := DispatchOutcome{}
	if dispatcher == nil {
		return outcome
	}
	selected := Select(dispatcher.config, SelectInput{Event: input.Event, ToolName: input.ToolName})
	if len(selected) == 0 {
		return outcome
	}

	payload, err := json.Marshal(input.Payload)
	if err != nil {
		payload = nil
	}

	for _, hook := range selected {
		// Async hooks run in the background and never block the turn; their result
		// is collected out-of-band (audit + asyncRewake channel). AsyncRewake
		// implies async even if config normalization did not set it.
		if hook.Async || hook.AsyncRewake {
			dispatcher.runAsync(ctx, hook, input, payload)
			outcome.AsyncLaunched++
			continue
		}

		command := Command{Command: hook.Command, Args: hook.Args}
		dispatcher.recordStarted(hook, input, command)

		started := dispatcher.now()
		result := dispatcher.executeAction(ctx, hook, payload)
		durationMs := int(dispatcher.now().Sub(started) / time.Millisecond)
		outcome.Ran++

		status, blocked := classifyResult(input.Event, result)
		dispatcher.recordCompleted(hook, input, status, result, durationMs)

		if message := hookMessage(result); message != "" {
			outcome.Messages = append(outcome.Messages, message)
		}

		if blocked {
			outcome.Blocked = true
			outcome.BlockedBy = hook.ID
			outcome.Reason = blockReason(result)
			// Stop on the first veto: the action is already denied.
			return outcome
		}

		// afterTool (and other non-veto events) signal a block decision with the
		// conventional blocking exit code. ContinueOnBlock feeds the reason back and
		// keeps going; otherwise the turn ends.
		if !blocksOn(input.Event) && result.Err == nil && !result.TimedOut && result.ExitCode == blockingExitCode {
			reason := blockReason(result)
			if hook.ContinueOnBlock {
				outcome.ContinueReason = appendReason(outcome.ContinueReason, reason)
				continue
			}
			outcome.EndTurn = true
			outcome.Reason = reason
			return outcome
		}
	}
	return outcome
}

// runAsync launches a background hook. It records the run to the audit store and,
// for asyncRewake hooks that exit with the blocking code, emits a RewakeSignal.
func (dispatcher *Dispatcher) runAsync(ctx context.Context, hook Definition, input DispatchInput, payload []byte) {
	dispatcher.recordStarted(hook, input, Command{Command: hook.Command, Args: hook.Args})
	// Detach from the request context so a background hook outlives the turn that
	// launched it (the whole point of async); it remains bounded by the per-hook
	// timeout applied inside executeAction. WithoutCancel preserves ctx values.
	asyncCtx := context.WithoutCancel(ctx)
	dispatcher.wg.Add(1)
	go func() {
		defer dispatcher.wg.Done()
		started := dispatcher.now()
		result := dispatcher.executeAction(asyncCtx, hook, payload)
		durationMs := int(dispatcher.now().Sub(started) / time.Millisecond)
		status, _ := classifyResult(input.Event, result)
		dispatcher.recordCompleted(hook, input, status, result, durationMs)

		// A timed-out/killed hook never produced a real verdict, so it must not wake
		// the model — matching the synchronous afterTool block decision's fail-closed
		// TimedOut check.
		if hook.AsyncRewake && result.Err == nil && !result.TimedOut && result.ExitCode == blockingExitCode {
			signal := RewakeSignal{
				HookID:  hook.ID,
				Summary: firstNonEmpty(hook.RewakeSummary, "a background hook requested attention"),
				Message: buildRewakeMessage(hook.RewakeMessage, result),
			}
			// Never block the goroutine if nobody is draining the channel.
			select {
			case dispatcher.rewakes <- signal:
			default:
			}
		}
	}()
}

// buildRewakeMessage joins the configured prefix with the hook's output into the
// system-reminder body fed to the model.
func buildRewakeMessage(prefix string, result commandResult) string {
	parts := make([]string, 0, 2)
	if trimmed := strings.TrimSpace(prefix); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if message := hookMessage(result); message != "" {
		parts = append(parts, message)
	}
	if len(parts) == 0 {
		return "a background hook exited with the blocking code"
	}
	return strings.Join(parts, "\n")
}

func appendReason(existing string, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return existing
	}
	if existing == "" {
		return reason
	}
	return existing + "\n" + reason
}

// classifyResult maps a command result to an audit status and whether it vetoes
// the action. A command that ran and exited non-zero blocks a beforeTool hook; a
// command that could not be executed at all is an error but never blocks (a
// missing hook binary must not wedge every tool call).
func classifyResult(event Event, result commandResult) (AuditStatus, bool) {
	// A hook that started but was killed by its deadline (or a cancellation) never
	// returned a verdict. For a blocking event we must fail CLOSED and veto: a
	// hung beforeTool policy hook cannot be treated as approval. (A launch failure
	// below still fails OPEN, so a misconfigured hook doesn't wedge every tool.)
	if result.TimedOut {
		if blocksOn(event) {
			return AuditBlocked, true
		}
		return AuditError, false
	}
	if result.Err != nil {
		return AuditError, false
	}
	if result.ExitCode != 0 {
		if blocksOn(event) {
			return AuditBlocked, true
		}
		return AuditError, false
	}
	return AuditCompleted, false
}

// hookMessage returns the output worth surfacing from a hook run: stdout when
// present, else stderr. Empty when the hook produced no output.
func hookMessage(result commandResult) string {
	if trimmed := strings.TrimSpace(result.Stdout); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(result.Stderr)
}

func blockReason(result commandResult) string {
	if result.TimedOut {
		if trimmed := strings.TrimSpace(result.Stderr); trimmed != "" {
			return "hook timed out: " + trimmed
		}
		return "hook timed out before returning a verdict"
	}
	for _, candidate := range []string{result.Stderr, result.Stdout} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return "hook exited non-zero"
}

func (dispatcher *Dispatcher) recordStarted(hook Definition, input DispatchInput, command Command) {
	if dispatcher.audit == nil {
		return
	}
	_, _ = dispatcher.audit.AppendStarted(AppendStartedInput{
		HookID:     hook.ID,
		Event:      input.Event,
		Matcher:    hook.Matcher,
		ToolCallID: input.ToolCallID,
		Commands:   []AuditCommand{command},
	})
}

func (dispatcher *Dispatcher) recordCompleted(hook Definition, input DispatchInput, status AuditStatus, result commandResult, durationMs int) {
	if dispatcher.audit == nil {
		return
	}
	_, _ = dispatcher.audit.AppendCompleted(AppendCompletedInput{
		HookID:     hook.ID,
		Event:      input.Event,
		Matcher:    hook.Matcher,
		ToolCallID: input.ToolCallID,
		Status:     status,
		Results:    []AuditResult{{ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr}},
		DurationMs: durationMs,
	})
}

// execCommandRunner runs a hook command directly (no shell), feeding the JSON
// payload on stdin and capturing stdout/stderr. A non-zero exit is reported via
// ExitCode (not Err); Err is reserved for commands that could not be launched.
func execCommandRunner(ctx context.Context, command string, args []string, stdin []byte, cwd string, env []string) commandResult {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = cwd
	cmd.Env = env
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := commandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result
	}
	// Could not launch (binary missing, timeout, etc.).
	result.ExitCode = -1
	result.Err = err
	return result
}
