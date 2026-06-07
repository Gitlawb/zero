package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const defaultSystemPrompt = "You are Zero, a terminal coding agent. Help with the current workspace and use tools when needed."
const maxTurnsAnswer = "Agent reached maximum number of turns without a final answer."

// abortedToolResultNotice is the placeholder tool result recorded for a tool
// call that was advertised by the assistant turn but never executed because the
// repeated-failure guard halted the run first. It keeps every tool_use paired
// with a tool_result so the transcript stays valid for a strict provider replay.
const abortedToolResultNotice = "aborted: run halted by the repeated-failure guard"

// droppedToolCallNotice tells the model a tool call it attempted was malformed
// (missing a tool name) and dropped before execution, so it re-issues a valid
// call instead of assuming the call ran. It is surfaced both when a turn yields
// ONLY a dropped call and when a turn mixes valid calls with a dropped one.
const droppedToolCallNotice = "Your previous tool call was malformed (it was missing a tool name) and was not executed. " +
	"Re-issue the tool call with a valid tool name and JSON arguments, or reply with your final answer."

// confirmationPolicy is the de-branded safety policy appended to the system
// prompt so the model self-polices before risky actions. It mirrors the
// sandbox's enforced rules but applies model-side judgement first.
//
//go:embed confirmation_policy.md
var confirmationPolicy string

// buildSystemPrompt returns the base instruction with the confirmation policy
// appended. It is built once per run so every turn shares the same system turn.
func buildSystemPrompt() string {
	policy := strings.TrimSpace(confirmationPolicy)
	if policy == "" {
		return defaultSystemPrompt
	}
	return defaultSystemPrompt + "\n\n" + policy
}

func Run(ctx context.Context, prompt string, provider Provider, options Options) (Result, error) {
	if provider == nil {
		return Result{}, errors.New("agent provider is required")
	}

	maxTurns := options.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 12
	}

	registry := options.Registry
	if registry == nil {
		registry = tools.NewRegistry()
	}

	permissionMode := options.PermissionMode
	if permissionMode == "" {
		permissionMode = PermissionModeAuto
	}

	messages := zeroruntime.SeedMessages(buildSystemPrompt(), prompt)

	guards := newGuardState()
	compactor := newCompactionState(options)

	result := Result{Messages: copyMessages(messages)}
	for turn := 0; turn < maxTurns; turn++ {
		result.Turns = turn + 1

		// PROACTIVE compaction: if the history is approaching the model's
		// context window, summarize the oldest middle before building the
		// request. A no-op when ContextWindow == 0 (compaction disabled).
		messages = compactor.maybeCompact(ctx, provider, messages)

		request := zeroruntime.CompletionRequest{
			Messages: copyMessages(messages),
			Tools:    toolDefinitions(registry, permissionMode, options),
		}

		stream, err := provider.StreamCompletion(ctx, request)
		if err != nil {
			// REACTIVE compaction: a context-limit failure on the call itself
			// can be recovered by compacting once and retrying the same turn.
			if compacted, retried, retryErr := compactor.recover(ctx, provider, messages, err.Error()); retried {
				messages = compacted
				if retryErr != nil {
					result.Messages = copyMessages(messages)
					return result, retryErr
				}
				request = zeroruntime.CompletionRequest{
					Messages: copyMessages(messages),
					Tools:    toolDefinitions(registry, permissionMode, options),
				}
				stream, err = provider.StreamCompletion(ctx, request)
			}
			if err != nil {
				result.Messages = copyMessages(messages)
				return result, err
			}
		}

		collected := zeroruntime.CollectStreamWithOptions(ctx, stream, zeroruntime.CollectOptions{
			OnText:  options.OnText,
			OnUsage: options.OnUsage,
		})
		if collected.Error != "" {
			// REACTIVE compaction: the streamed error may also be a context
			// limit (some providers surface it mid-stream). Compact and retry
			// the same turn once before giving up.
			if compacted, retried, retryErr := compactor.recover(ctx, provider, messages, collected.Error); retried {
				messages = compacted
				if retryErr != nil {
					result.Messages = copyMessages(messages)
					return result, retryErr
				}
				retryRequest := zeroruntime.CompletionRequest{
					Messages: copyMessages(messages),
					Tools:    toolDefinitions(registry, permissionMode, options),
				}
				retryStream, retryStreamErr := provider.StreamCompletion(ctx, retryRequest)
				if retryStreamErr != nil {
					result.Messages = copyMessages(messages)
					return result, retryStreamErr
				}
				// Omit OnText on the reactive retry: when the original error
				// surfaced MID-stream, partial text was already forwarded to the
				// user. Re-streaming the retried response on top of it would
				// duplicate output. OnUsage IS kept so token telemetry/budgeting
				// still counts the successful retry. The retried text is captured
				// in collected.Text and becomes the turn's assistant message.
				collected = zeroruntime.CollectStreamWithOptions(ctx, retryStream, zeroruntime.CollectOptions{
					OnUsage: options.OnUsage,
				})
			}
		}
		if collected.Error != "" {
			result.Messages = copyMessages(messages)
			return result, errors.New(collected.Error)
		}
		if ctx.Err() != nil {
			result.Messages = copyMessages(messages)
			return result, ctx.Err()
		}

		messages = append(messages, zeroruntime.Message{
			Role:      zeroruntime.MessageRoleAssistant,
			Content:   collected.Text,
			ToolCalls: collected.ToolCalls,
		})

		if len(collected.ToolCalls) == 0 {
			// The model intended a tool call but it was malformed and dropped.
			// Tell it to retry rather than silently treating text as the answer.
			// This path is handled before the no-output guard so a dropped-call
			// turn is never counted as a runaway empty turn.
			if collected.DroppedToolCalls > 0 {
				messages = append(messages, zeroruntime.Message{
					Role:    zeroruntime.MessageRoleUser,
					Content: droppedToolCallNotice,
				})
				continue
			}
			// No-output guard: a turn with visible text is a real final answer.
			// A truly-empty turn (no text, no tool calls, no dropped calls) is
			// counted toward the runaway cap so we stop before burning maxTurns.
			if guards.observeTurn(collected) {
				result.FinalAnswer = noOutputStopAnswer(result.Turns)
				result.Messages = copyMessages(messages)
				return result, nil
			}
			if strings.TrimSpace(collected.Text) == "" {
				// Empty-but-under-cap turn: nudge the model to make progress
				// rather than treating the empty response as a final answer.
				messages = append(messages, zeroruntime.Message{
					Role: zeroruntime.MessageRoleUser,
					Content: "Your previous response had no visible output and no tool calls. " +
						"Continue the task by using a tool or reply with your final answer.",
				})
				continue
			}
			result.FinalAnswer = collected.Text
			result.Messages = copyMessages(messages)
			return result, nil
		}

		// A turn with tool calls is progress: update guard counters before
		// executing so the empty-turn counter resets and plan-tracking signals
		// stay current.
		guards.observeTurn(collected)

		failureHint := ""
		for index, call := range collected.ToolCalls {
			if options.OnToolCall != nil {
				options.OnToolCall(call)
			}
			toolResult, abortErr := executeToolCall(ctx, registry, call, permissionMode, options)
			if options.OnToolResult != nil {
				options.OnToolResult(toolResult)
			}
			messages = append(messages, zeroruntime.Message{
				Role:       zeroruntime.MessageRoleTool,
				Content:    toolResult.Output,
				ToolCallID: toolResult.ToolCallID,
			})

			// A tool may demand the run ABORT — a canceled/timed-out ask_user prompt
			// returns context.Canceled rather than fabricating a headless answer. Stop
			// promptly with that error (or run-context cancellation), keeping messages
			// valid by closing out the still-advertised calls first.
			if abortErr == nil && ctx.Err() != nil {
				abortErr = ctx.Err()
			}
			if abortErr != nil {
				messages = appendAbortedToolResults(messages, collected.ToolCalls[index+1:])
				result.Messages = copyMessages(messages)
				return result, abortErr
			}

			// Repeated-failure guard: if a tool keeps failing the same way, hint
			// once (with its schema) then halt — so no model loops on a bad call.
			// Only RETRIABLE failures (bad arguments / execution errors) drive it:
			// policy refusals (disabled tool, permission denial, sandbox violation)
			// aren't fixed by reformatting the call, so a "match this schema" hint
			// would misdirect the model toward JSON shape or blocked behavior.
			retriableFailure := isRetriableToolError(toolResult)
			outcome := guards.observeToolResult(call.Name, retriableFailure, toolResult.Output)
			if outcome.Stop {
				// The assistant message advertised EVERY collected tool call, but
				// the guard halts mid-turn so the calls after this one never run.
				// Append an aborted placeholder result for each remaining call so
				// every tool_use has a matching tool_result and the recorded
				// messages stay valid for a strict provider replay (Anthropic
				// rejects a tool_use with no answering tool_result).
				messages = appendAbortedToolResults(messages, collected.ToolCalls[index+1:])
				result.FinalAnswer = toolFailureStopAnswer(call.Name, outcome.Count)
				result.Messages = copyMessages(messages)
				return result, nil
			}
			if outcome.InjectHint && failureHint == "" {
				failureHint = toolFailureHint(call.Name, toolSchemaJSON(registry, call.Name), toolResult.Output)
			}
		}

		// A turn can mix valid tool calls with a dropped (nameless) one. The valid
		// calls executed above; surface the dropped call too so it is never
		// silently ignored just because the turn also did real work. This is
		// independent of (and additive to) the failure-hint / plan-reminder nudges.
		if collected.DroppedToolCalls > 0 {
			messages = append(messages, zeroruntime.Message{
				Role:    zeroruntime.MessageRoleUser,
				Content: droppedToolCallNotice,
			})
		}

		// A repeated-failure hint (schema + exact error) takes priority over the
		// planning reminders — fixing the failing call matters more than plan
		// hygiene. Both are light, one-shot, user-role nudges.
		if failureHint != "" {
			messages = append(messages, zeroruntime.Message{
				Role:    zeroruntime.MessageRoleUser,
				Content: failureHint,
			})
		} else if reminder := guards.planReminder(result.Turns); reminder != "" {
			messages = append(messages, zeroruntime.Message{
				Role:    zeroruntime.MessageRoleUser,
				Content: reminder,
			})
		}
	}

	result.FinalAnswer = maxTurnsAnswer
	result.Messages = copyMessages(messages)
	return result, nil
}

// toolSchemaJSON renders a tool's parameter schema as readable JSON for the
// repeated-failure corrective hint, so the model can see exactly what arguments
// the tool expects. Returns "{}" if the tool or schema is unavailable.
func toolSchemaJSON(registry *tools.Registry, name string) string {
	tool, ok := registry.Get(name)
	if !ok {
		return "{}"
	}
	data, err := json.MarshalIndent(schemaToRuntimeMap(tool.Parameters()), "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

// executeToolCall runs one tool call and returns its result. The second return
// is a non-nil ABORT error only when the call demands the whole run stop (a
// canceled/timed-out ask_user prompt) rather than continuing — every ordinary
// success or tool error returns a nil abort error.
func executeToolCall(ctx context.Context, registry *tools.Registry, call ToolCall, permissionMode PermissionMode, options Options) (ToolResult, error) {
	args := map[string]any{}
	if call.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Status:     tools.StatusError,
				Output:     "Error: Failed to parse arguments for " + call.Name + ": " + err.Error(),
			}, nil
		}
	}
	if !ToolAllowedByFilters(call.Name, options.EnabledTools, options.DisabledTools) {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusError,
			Output:     `Error: Tool "` + call.Name + `" is not enabled for this run.`,
		}, nil
	}

	// ask_user is intercepted in the loop (like permissions) so the question can
	// be routed to an interactive front-end instead of blocking inside the tool.
	// When no front-end is wired up it degrades to the tool's own graceful Run().
	if call.Name == "ask_user" {
		return executeAskUser(ctx, registry, call, args, permissionMode, options)
	}

	tool, toolFound := registry.Get(call.Name)
	permissionGranted := permissionMode == PermissionModeUnsafe
	if toolFound && tool.Safety().Permission == tools.PermissionAllow {
		permissionGranted = true
	}

	var preflightDecision *sandbox.Decision
	if toolFound && options.Sandbox != nil {
		decision := options.Sandbox.Evaluate(ctx, sandboxRequest(call.Name, tool, args, permissionGranted, permissionMode, options))
		preflightDecision = &decision
	}

	decisionReason := ""
	if toolFound && options.OnPermissionRequest != nil && shouldRequestPermission(tool, permissionGranted, preflightDecision) {
		requestEvent, ok := buildPermissionEvent(call, tool, args, permissionGranted, permissionMode, options, preflightDecision)
		if !ok {
			requestEvent = fallbackPermissionEvent(call, tool, args, permissionMode, options)
		}
		request := permissionRequestFromEvent(requestEvent, args)
		decision, err := requestPermission(ctx, request, options)
		if err != nil {
			decision = PermissionDecision{Action: PermissionDecisionDeny, Reason: err.Error()}
		}
		decision.Action = normalizePermissionDecisionAction(decision.Action)
		decisionReason = strings.TrimSpace(decision.Reason)
		switch decision.Action {
		case PermissionDecisionAllow:
			permissionGranted = true
		case PermissionDecisionAlwaysAllow:
			permissionGranted = true
			grant, err := persistPermissionGrant(call.Name, decisionReason, options)
			if err != nil {
				emitDeniedPermission(options, call, requestEvent, "failed to persist permission grant: "+err.Error())
				return deniedPermissionResult(call, "failed to persist permission grant: "+err.Error(), requestEvent), nil
			}
			requestEvent.GrantMatched = true
			requestEvent.Grant = &grant
		default:
			emitDeniedPermission(options, call, requestEvent, decisionReason)
			return deniedPermissionResult(call, decisionReason, requestEvent), nil
		}
	}

	result := registry.RunWithOptions(ctx, call.Name, args, tools.RunOptions{
		PermissionGranted: permissionGranted,
		PermissionMode:    string(permissionMode),
		Autonomy:          options.Autonomy,
		Sandbox:           options.Sandbox,
		ToolCallID:        call.ID,
		SessionID:         options.SessionID,
		Model:             options.Model,
		ReasoningEffort:   options.ReasoningEffort,
		Depth:             options.Depth,
		Cwd:               options.Cwd,
		// The sandbox decision (if any) is returned synchronously on the Result and
		// used here for permission event building.
	})
	sandboxDecision := result.SandboxDecision
	if toolFound && options.OnPermission != nil {
		if event, ok := buildPermissionEvent(call, tool, args, permissionGranted, permissionMode, options, sandboxDecision); ok {
			event.DecisionReason = decisionReason
			options.OnPermission(event)
		}
	}
	// Secret scrubbing happens at the registry boundary (the single point both
	// the agent loop and the MCP server pass through), so result.Output is
	// already redacted here and result.Redacted reflects whether it changed.
	return ToolResult{
		ToolCallID:   call.ID,
		Name:         call.Name,
		Status:       result.Status,
		Output:       result.Output,
		Meta:         result.Meta,
		Redacted:     result.Redacted,
		ChangedFiles: result.ChangedFiles,
		Display:      result.Display,
	}, nil
}

// isRetriableToolError reports whether a failed tool result is one the model can
// plausibly fix by changing its next call (argument or execution failure), as
// opposed to a policy refusal (disabled tool, permission denial, sandbox
// violation) that no reformatting will satisfy. Only retriable failures should
// drive the repeated-failure schema hint / stop.
func isRetriableToolError(result ToolResult) bool {
	if result.Status != tools.StatusError {
		return false
	}
	if result.Meta["permission_action"] == string(PermissionActionDeny) {
		return false
	}
	switch {
	case strings.Contains(result.Output, "is not enabled for this run"),
		strings.Contains(result.Output, "Permission denied for "),
		strings.Contains(result.Output, "Permission required for "),
		strings.Contains(result.Output, "Sandbox violation"),
		strings.Contains(result.Output, "Sandbox approval required for "):
		return false
	}
	return true
}

// scrubInterceptedOutput mirrors the registry's scrubResultSecrets boundary for
// the loop-intercepted paths (ask_user answers, task child final answers) that
// build a ToolResult.Output directly instead of going through
// registry.RunWithOptions. RedactString substitutes "[REDACTED]" inline; the
// returned bool reports whether anything was scrubbed so the caller can set
// ToolResult.Redacted, keeping these paths consistent with every other tool
// result the model and transcript see.
func scrubInterceptedOutput(output string) (string, bool) {
	scrubbed := redaction.RedactString(output, redaction.Options{})
	return scrubbed, scrubbed != output
}

// executeAskUser routes an ask_user call to the interactive front-end via
// options.OnAskUser, mirroring the async permission flow. If no handler is set,
// or the handler errors, it falls back to the tool's own graceful result so the
// loop never blocks forever waiting on a user who isn't there.
// executeAskUser returns (result, abortErr). abortErr is non-nil ONLY when the
// prompt was canceled/timed out and the run must stop with that error; every
// other path (success, bad args, headless degradation) returns a nil abortErr.
func executeAskUser(ctx context.Context, registry *tools.Registry, call ToolCall, args map[string]any, permissionMode PermissionMode, options Options) (ToolResult, error) {
	questions, err := tools.ParseAskUserQuestions(args)
	if err != nil {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusError,
			Output:     "Error: Invalid arguments for ask_user: " + err.Error(),
		}, nil
	}

	if options.OnAskUser == nil {
		return askUserFallbackResult(ctx, registry, call, args, permissionMode, options), nil
	}

	header, _ := args["header"].(string)
	request := AskUserRequest{
		ToolCallID: call.ID,
		Header:     strings.TrimSpace(header),
		Questions:  toAgentAskUserQuestions(questions),
	}
	response, err := options.OnAskUser(ctx, request)
	if err != nil {
		// A canceled / timed-out prompt must ABORT the run (return the error), not
		// fabricate a headless answer that keeps mutating the transcript after the UI
		// asked to stop. The error result carries the reason; the abort error stops
		// the loop even when the run context itself was not canceled.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Status:     tools.StatusError,
				Output:     "Error: ask_user canceled: " + err.Error(),
			}, err
		}
		// Genuine handler unavailability (no interactive surface / non-cancel error):
		// degrade to the same headless path as a missing handler.
		return askUserFallbackResult(ctx, registry, call, args, permissionMode, options), nil
	}

	// Scrub the formatted answers through the same redaction boundary the
	// registry applies to tool output, so a secret in a user's answer never lands
	// in the transcript unredacted.
	output, redacted := scrubInterceptedOutput(tools.FormatAskUserAnswers(questions, response.Answers))
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusOK,
		Output:     output,
		Redacted:   redacted,
	}, nil
}

// askUserFallbackResult runs the registered ask_user tool (or returns the shared
// graceful message) so the no-interactive-user path matches every OTHER tool: it
// goes through registry.RunWithOptions with the same run context (ToolCallID,
// session/model/depth/cwd, sandbox/permission) and copies the full result fields
// (Meta/ChangedFiles/Display), instead of the bare registry.Run that dropped them.
func askUserFallbackResult(ctx context.Context, registry *tools.Registry, call ToolCall, args map[string]any, permissionMode PermissionMode, options Options) ToolResult {
	if _, ok := registry.Get(call.Name); ok {
		result := registry.RunWithOptions(ctx, call.Name, args, tools.RunOptions{
			// ask_user is read-only (PermissionAllow); no prompt/grant is required.
			PermissionGranted: true,
			PermissionMode:    string(permissionMode),
			Autonomy:          options.Autonomy,
			Sandbox:           options.Sandbox,
			ToolCallID:        call.ID,
			SessionID:         options.SessionID,
			Model:             options.Model,
			ReasoningEffort:   options.ReasoningEffort,
			Depth:             options.Depth,
			Cwd:               options.Cwd,
		})
		return ToolResult{
			ToolCallID:   call.ID,
			Name:         call.Name,
			Status:       result.Status,
			Output:       result.Output,
			Meta:         result.Meta,
			Redacted:     result.Redacted,
			ChangedFiles: result.ChangedFiles,
			Display:      result.Display,
		}
	}
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusOK,
		Output:     tools.AskUserNonInteractiveMessage(),
	}
}

func toAgentAskUserQuestions(questions []tools.AskUserQuestion) []AskUserQuestion {
	converted := make([]AskUserQuestion, len(questions))
	for index, question := range questions {
		converted[index] = AskUserQuestion{
			Question:    question.Question,
			Options:     append([]string{}, question.Options...),
			MultiSelect: question.MultiSelect,
		}
	}
	return converted
}

func sandboxRequest(toolName string, tool tools.Tool, args map[string]any, permissionGranted bool, permissionMode PermissionMode, options Options) sandbox.Request {
	safety := tool.Safety()
	return sandbox.Request{
		WorkspaceRoot:     "",
		ToolName:          toolName,
		SideEffect:        sandbox.SideEffect(safety.SideEffect),
		Permission:        sandbox.Permission(safety.Permission),
		PermissionGranted: permissionGranted,
		PermissionMode:    sandbox.PermissionMode(permissionMode),
		Autonomy:          sandbox.Autonomy(options.Autonomy),
		Args:              args,
		Reason:            safety.Reason,
	}
}

func shouldRequestPermission(tool tools.Tool, permissionGranted bool, decision *sandbox.Decision) bool {
	if permissionGranted || tool.Safety().Permission != tools.PermissionPrompt {
		return false
	}
	if decision != nil {
		return decision.Action == sandbox.ActionPrompt
	}
	return true
}

func requestPermission(ctx context.Context, request PermissionRequest, options Options) (PermissionDecision, error) {
	if options.OnPermissionRequest == nil {
		return PermissionDecision{Action: PermissionDecisionDeny, Reason: request.Reason}, nil
	}
	return options.OnPermissionRequest(ctx, request)
}

func normalizePermissionDecisionAction(action PermissionDecisionAction) PermissionDecisionAction {
	switch action {
	case PermissionDecisionAllow, PermissionDecisionAlwaysAllow:
		return action
	default:
		return PermissionDecisionDeny
	}
}

func persistPermissionGrant(toolName string, reason string, options Options) (sandbox.Grant, error) {
	if options.Sandbox == nil {
		return sandbox.Grant{}, errors.New("sandbox engine is not configured")
	}
	maxAutonomy := sandbox.Autonomy(options.Autonomy)
	if maxAutonomy == "" {
		maxAutonomy = sandbox.AutonomyMedium
	}
	if normalized, err := sandbox.NormalizeAutonomy(maxAutonomy); err == nil {
		maxAutonomy = normalized
	}
	return options.Sandbox.Grant(sandbox.GrantInput{
		ToolName:    toolName,
		Decision:    sandbox.GrantAllow,
		MaxAutonomy: maxAutonomy,
		Reason:      reason,
	})
}

func emitDeniedPermission(options Options, call ToolCall, requestEvent PermissionEvent, reason string) {
	if options.OnPermission == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = requestEvent.Reason
	}
	event := requestEvent
	event.ToolCallID = call.ID
	event.ToolName = call.Name
	event.Action = PermissionActionDeny
	event.PermissionGranted = false
	event.DecisionReason = reason
	options.OnPermission(event)
}

func deniedPermissionResult(call ToolCall, reason string, requestEvent PermissionEvent) ToolResult {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = requestEvent.Reason
	}
	if reason == "" {
		reason = "tool requires approval before execution"
	}
	event := requestEvent
	event.Action = PermissionActionDeny
	event.PermissionGranted = false
	event.DecisionReason = reason
	if event.Risk.Level == "" {
		event.Risk = sandbox.Risk{Level: sandbox.RiskMedium, Reason: reason}
	}
	if requestEvent.ToolName == "" {
		event.ToolName = call.Name
	}
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusError,
		Output:     "Error: Permission denied for " + call.Name + ": " + reason,
		Meta: map[string]string{
			"permission_action": string(event.Action),
		},
	}
}

func buildPermissionEvent(call ToolCall, tool tools.Tool, args map[string]any, permissionGranted bool, permissionMode PermissionMode, options Options, decision *sandbox.Decision) (PermissionEvent, bool) {
	safety := tool.Safety()
	var action PermissionAction
	reason := safety.Reason
	risk := sandbox.Classify(sandbox.Request{
		WorkspaceRoot:     "",
		ToolName:          call.Name,
		SideEffect:        sandbox.SideEffect(safety.SideEffect),
		Permission:        sandbox.Permission(safety.Permission),
		PermissionGranted: permissionGranted,
		PermissionMode:    sandbox.PermissionMode(permissionMode),
		Autonomy:          sandbox.Autonomy(options.Autonomy),
		Args:              args,
		Reason:            safety.Reason,
	})
	var violation *sandbox.Violation
	grantMatched := false
	var grant *sandbox.Grant

	if decision != nil {
		action = permissionActionFromSandbox(decision.Action)
		if decision.Reason != "" {
			reason = decision.Reason
		}
		risk = decision.Risk
		violation = decision.Violation
		grantMatched = decision.GrantMatched
		grant = decision.Grant
	} else {
		switch safety.Permission {
		case tools.PermissionDeny:
			action = PermissionActionDeny
		case tools.PermissionPrompt:
			if permissionGranted {
				action = PermissionActionAllow
			} else {
				action = PermissionActionPrompt
			}
		default:
			return PermissionEvent{}, false
		}
	}

	if safety.Permission == tools.PermissionAllow && action == PermissionActionAllow && !grantMatched && violation == nil {
		return PermissionEvent{}, false
	}

	autonomy := options.Autonomy
	if normalized, err := sandbox.NormalizeAutonomy(sandbox.Autonomy(autonomy)); err == nil {
		autonomy = string(normalized)
	}

	return PermissionEvent{
		ToolCallID:        call.ID,
		ToolName:          call.Name,
		Action:            action,
		Permission:        string(safety.Permission),
		PermissionGranted: permissionGranted,
		PermissionMode:    permissionMode,
		Autonomy:          autonomy,
		SideEffect:        string(safety.SideEffect),
		Reason:            reason,
		Risk:              risk,
		Violation:         violation,
		GrantMatched:      grantMatched,
		Grant:             grant,
	}, true
}

func fallbackPermissionEvent(call ToolCall, tool tools.Tool, args map[string]any, permissionMode PermissionMode, options Options) PermissionEvent {
	event, _ := buildPermissionEvent(call, tool, args, false, permissionMode, options, nil)
	return event
}

func permissionRequestFromEvent(event PermissionEvent, args map[string]any) PermissionRequest {
	return PermissionRequest{
		ToolCallID:     event.ToolCallID,
		ToolName:       event.ToolName,
		Action:         event.Action,
		Permission:     event.Permission,
		PermissionMode: event.PermissionMode,
		Autonomy:       event.Autonomy,
		SideEffect:     event.SideEffect,
		Reason:         event.Reason,
		Risk:           event.Risk,
		Args:           cloneArgs(args),
		Violation:      event.Violation,
		GrantMatched:   event.GrantMatched,
		Grant:          event.Grant,
	}
}

func cloneArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	copied := make(map[string]any, len(args))
	for key, value := range args {
		copied[key] = value
	}
	return copied
}

func permissionActionFromSandbox(action sandbox.Action) PermissionAction {
	switch action {
	case sandbox.ActionAllow:
		return PermissionActionAllow
	case sandbox.ActionDeny:
		return PermissionActionDeny
	default:
		return PermissionActionPrompt
	}
}

func toolDefinitions(registry *tools.Registry, permissionMode PermissionMode, options Options) []zeroruntime.ToolDefinition {
	registeredTools := registry.All()
	definitions := make([]zeroruntime.ToolDefinition, 0, len(registeredTools))
	for _, tool := range registeredTools {
		if !ToolVisible(tool, permissionMode, options.EnabledTools, options.DisabledTools) {
			continue
		}
		definitions = append(definitions, zeroruntime.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  schemaToRuntimeMap(tool.Parameters()),
		})
	}

	sort.Slice(definitions, func(left int, right int) bool {
		return definitions[left].Name < definitions[right].Name
	})
	return definitions
}

func ToolVisible(tool tools.Tool, permissionMode PermissionMode, enabledTools []string, disabledTools []string) bool {
	return ToolAllowedByFilters(tool.Name(), enabledTools, disabledTools) && ToolAdvertised(tool, permissionMode)
}

func ToolAllowedByFilters(name string, enabledTools []string, disabledTools []string) bool {
	if len(enabledTools) > 0 {
		if !containsToolName(enabledTools, name) {
			return false
		}
	}
	if containsToolName(disabledTools, name) {
		return false
	}
	return true
}

func containsToolName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func schemaToRuntimeMap(schema tools.Schema) map[string]any {
	parameters := map[string]any{
		"type":                 schema.Type,
		"additionalProperties": schema.AdditionalProperties,
	}

	if len(schema.Required) > 0 {
		parameters["required"] = append([]string{}, schema.Required...)
	}

	if len(schema.Properties) > 0 {
		properties := make(map[string]any, len(schema.Properties))
		for name, property := range schema.Properties {
			properties[name] = propertyToRuntimeMap(property)
		}
		parameters["properties"] = properties
	}

	return parameters
}

func propertyToRuntimeMap(property tools.PropertySchema) map[string]any {
	schema := map[string]any{
		"type": property.Type,
	}
	if property.Description != "" {
		schema["description"] = property.Description
	}
	if len(property.Enum) > 0 {
		schema["enum"] = append([]string{}, property.Enum...)
	}
	if property.Default != nil {
		schema["default"] = property.Default
	}
	if property.Items != nil {
		schema["items"] = propertyToRuntimeMap(*property.Items)
	}
	if property.Minimum != nil {
		schema["minimum"] = *property.Minimum
	}
	if property.Maximum != nil {
		schema["maximum"] = *property.Maximum
	}
	if property.Items != nil {
		schema["items"] = propertyToRuntimeMap(*property.Items)
	}
	if len(property.Properties) > 0 {
		properties := make(map[string]any, len(property.Properties))
		for name, nested := range property.Properties {
			properties[name] = propertyToRuntimeMap(nested)
		}
		schema["properties"] = properties
	}
	if len(property.Required) > 0 {
		schema["required"] = append([]string{}, property.Required...)
	}
	return schema
}

func ToolAdvertised(tool tools.Tool, permissionMode PermissionMode) bool {
	if tool.Safety().Permission == tools.PermissionDeny {
		return false
	}
	if permissionMode == PermissionModeAuto {
		return tool.Safety().Permission == tools.PermissionAllow || tool.Safety().AdvertiseInAuto
	}
	return true
}

// appendAbortedToolResults adds a placeholder tool-result message for each of
// the given (unexecuted) tool calls, so every advertised tool_use keeps a
// matching tool_result when the loop halts a turn before all calls have run.
func appendAbortedToolResults(messages []Message, remaining []ToolCall) []Message {
	for _, call := range remaining {
		messages = append(messages, zeroruntime.Message{
			Role:       zeroruntime.MessageRoleTool,
			Content:    abortedToolResultNotice,
			ToolCallID: call.ID,
		})
	}
	return messages
}

func copyMessages(messages []Message) []Message {
	copied := make([]Message, len(messages))
	for index, message := range messages {
		copied[index] = message
		if message.ToolCalls != nil {
			copied[index].ToolCalls = append([]ToolCall{}, message.ToolCalls...)
		}
	}
	return copied
}
