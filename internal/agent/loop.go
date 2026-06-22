package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/streamjson"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const maxTurnsAnswer = "Agent reached maximum number of turns without a final answer."
const maxTurnsFinalAnswerPrompt = "You have reached the tool-turn limit. Do not call tools. Give a concise final answer now: summarize what you completed, what you found, and any remaining blockers."

const (
	toolResultMetaControl       = "control"
	toolResultControlSpecReview = "spec_review_required"
)

var errPermissionApprovalCanceled = errors.New("permission approval cancelled")

// isImageRejectionError reports whether err is a provider 400 that rejects
// image/multimodal content. This is checked BEFORE the compaction-retry path
// so an image-rejection 400 doesn't loop endlessly (the user hit this with
// minimax-m3: text-only model, image attached, 400 loop until the process
// was killed).
func isImageRejectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "400") {
		return false
	}
	for _, keyword := range []string{"image", "vision", "multimodal", "unsupported content type", "does not support"} {
		if strings.Contains(msg, keyword) {
			return true
		}
	}
	return false
}

type permissionRunState struct {
	mu       sync.Mutex
	cleanups []func()
}

func (state *permissionRunState) add(cleanup func()) {
	if state == nil || cleanup == nil {
		return
	}
	state.mu.Lock()
	state.cleanups = append(state.cleanups, cleanup)
	state.mu.Unlock()
}

func (state *permissionRunState) cleanup() {
	if state == nil {
		return
	}
	state.mu.Lock()
	cleanups := state.cleanups
	state.cleanups = nil
	state.mu.Unlock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
}

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

// escalationFailedNoticePrefix introduces the brief, user-role note recorded
// when a requested mid-run model switch could not be performed (the
// ModelSwitcher returned an error). The run continues on the current model;
// escalation is best-effort, never fatal.
const escalationFailedNoticePrefix = "Note: could not switch to the requested model"

// The system prompt (core coding-craft instructions + workspace context + safety
// confirmation policy) is assembled in system_prompt.go via buildSystemPrompt.

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
	runPermissions := &permissionRunState{}
	options.runPermissions = runPermissions
	defer runPermissions.cleanup()

	messages := zeroruntime.SeedMessagesWithImages(buildSystemPrompt(options), prompt, options.Images)

	guards := newGuardState()
	compactor := newCompactionState(options)

	// loaded tracks deferred-eligible tools the model has pulled via tool_search
	// during THIS run. It is consulted by partitionTools each turn to expose a
	// loaded tool's full schema; it lives only for the run (v1 within-run scope).
	loaded := map[string]bool{}

	result := Result{Messages: copyMessages(messages)}
	for turn := 0; turn < maxTurns; turn++ {
		result.Turns = turn + 1

		// Build the per-turn tool list first so proactive compaction can include
		// the tool-definition tokens (they ride on every request) in its estimate.
		// partitionTools depends only on registry/permissions/options/loaded, not on
		// the messages, so computing it before compaction is safe.
		exposed, reminder := partitionTools(registry, permissionMode, options, loaded)

		// PROACTIVE compaction: if the history is approaching the model's
		// context window, summarize the oldest middle before building the
		// request. A no-op when ContextWindow == 0 (compaction disabled).
		messages = compactor.maybeCompact(ctx, provider, messages, exposed)
		request := zeroruntime.CompletionRequest{
			Messages:        copyMessages(messages),
			Tools:           exposed,
			ReasoningEffort: options.ReasoningEffort,
		}
		if reminder != "" {
			// Append to the per-turn request copy only — NEVER to persistent
			// messages — so the reminder refreshes each turn and never accumulates
			// in the saved transcript.
			request.Messages = append(request.Messages, zeroruntime.Message{
				Role:    zeroruntime.MessageRoleUser,
				Content: reminder,
			})
		}

		// Report the per-category context budget for this turn so a surface can
		// show utilization. Opt-in: a no-op when OnContext is unset.
		if options.OnContext != nil {
			options.OnContext(MeasureContext(messages, request.Tools, options.ContextWindow))
		}

		// A transient upstream disconnect on the initial connect is retried with
		// backoff (before any content is forwarded, so no OnText is duplicated);
		// a context-limit / image-rejection / cancellation is NOT retried here —
		// those fall through to their own handlers below.
		stream, err := streamWithReconnect(ctx, provider, request, reconnectNoticeFor(options))
		if err != nil {
			if isImageRejectionError(err) {
				result.Messages = copyMessages(messages)
				return result, fmt.Errorf("model %s rejected the image: %s. The model may not support image input — try switching to a vision-capable model (claude, gpt-4o, gemini)", options.Model, err.Error())
			}
			// REACTIVE compaction: a context-limit failure on the call itself
			// can be recovered by compacting once and retrying the same turn.
			if compacted, retried, retryErr := compactor.recover(ctx, provider, messages, request.Tools, err.Error()); retried {
				messages = compacted
				if retryErr != nil {
					result.Messages = copyMessages(messages)
					return result, retryErr
				}
				// Rebuild from the compacted messages but reuse the SAME active-mode
				// partition (exposed) and reminder computed for this turn: they depend
				// on registry+loaded, not on the messages, so they stay valid after
				// compaction. Using the bare toolDefinitions here would route through an
				// empty-loaded partition, re-hiding every already-loaded deferred tool
				// and dropping the reminder when deferral is active.
				request = zeroruntime.CompletionRequest{
					Messages:        copyMessages(messages),
					Tools:           exposed,
					ReasoningEffort: options.ReasoningEffort,
				}
				if reminder != "" {
					// Append to the per-turn retry copy only — NEVER to persistent
					// messages — matching the main path's reminder-not-persisted invariant.
					request.Messages = append(request.Messages, zeroruntime.Message{
						Role:    zeroruntime.MessageRoleUser,
						Content: reminder,
					})
				}
				// Pre-content connect after a context-limit compaction: route through the
				// reconnect helper so a transient upstream hiccup here doesn't fail the
				// whole run and re-burn every token (AUDIT-L1).
				stream, err = streamWithReconnect(ctx, provider, request, reconnectNoticeFor(options))
			}
			if err != nil {
				result.Messages = copyMessages(messages)
				return result, err
			}
		}

		collected := zeroruntime.CollectStreamWithOptions(ctx, stream, zeroruntime.CollectOptions{
			OnText:          options.OnText,
			OnReasoning:     options.OnReasoning,
			OnUsage:         options.OnUsage,
			OnToolCallStart: options.OnToolCallStart,
			OnToolCallDelta: options.OnToolCallDelta,
		})
		if collected.Error != "" {
			collectedErr := errors.New(collected.Error)
			if isImageRejectionError(collectedErr) {
				result.Messages = copyMessages(messages)
				return result, fmt.Errorf("model %s rejected the image: %s. The model may not support image input — try switching to a vision-capable model (claude, gpt-4o, gemini)", options.Model, collected.Error)
			}
			// REACTIVE compaction: the streamed error may also be a context
			// limit (some providers surface it mid-stream). Compact and retry
			// the same turn once before giving up.
			if compacted, retried, retryErr := compactor.recover(ctx, provider, messages, request.Tools, collected.Error); retried {
				messages = compacted
				if retryErr != nil {
					result.Messages = copyMessages(messages)
					return result, retryErr
				}
				// Reuse the SAME active-mode partition (exposed) and reminder from this
				// turn rather than the bare toolDefinitions: exposed/reminder depend on
				// registry+loaded (not the messages), so they stay valid after compaction.
				// Routing through an empty-loaded partition here would re-hide every
				// already-loaded deferred tool and drop the reminder when deferral is active.
				retryRequest := zeroruntime.CompletionRequest{
					Messages:        copyMessages(messages),
					Tools:           exposed,
					ReasoningEffort: options.ReasoningEffort,
				}
				if reminder != "" {
					// Append to the per-turn retry copy only — NEVER to persistent
					// messages — matching the main path's reminder-not-persisted invariant.
					retryRequest.Messages = append(retryRequest.Messages, zeroruntime.Message{
						Role:    zeroruntime.MessageRoleUser,
						Content: reminder,
					})
				}
				// Pre-content reconnect of a mid-stream disconnect: route the connect
				// through the reconnect helper too (AUDIT-L1).
				retryStream, retryStreamErr := streamWithReconnect(ctx, provider, retryRequest, reconnectNoticeFor(options))
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
		// Check ctx first: on cancellation helpers.go sets collected.Error to
		// ctx.Err().Error(), so returning errors.New(collected.Error) would lose
		// the wrapped sentinel and break errors.Is(err, context.Canceled).
		if ctx.Err() != nil {
			result.Messages = copyMessages(messages)
			return result, ctx.Err()
		}
		if collected.Error != "" {
			result.Messages = copyMessages(messages)
			return result, errors.New(collected.Error)
		}

		// Carry the turn's terminal stop reason so a final answer cut off at the
		// output token cap (or by a content filter) is reported as truncated. A
		// tool-call turn normalizes to "" and clears any prior reason.
		result.FinishReason = collected.FinishReason

		messages = append(messages, zeroruntime.Message{
			Role:      zeroruntime.MessageRoleAssistant,
			Content:   collected.Text,
			ToolCalls: historySafeToolCalls(collected.ToolCalls),
			// Preserve thinking blocks so the next turn can replay them; providers
			// that use extended thinking reject tool conversations that drop them.
			Reasoning: collected.ReasoningBlocks,
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
		// turnRequestedModel records the FIRST mid-run escalation target requested
		// during this turn's tool batch. The actual provider switch happens once,
		// after the batch, so every tool_result is recorded first and at most one
		// switch occurs per turn.
		turnRequestedModel := ""
		// changedFilesThisBatch aggregates every file the turn's mutating tools
		// touched, so post-edit self-correction runs ONCE over the union after the
		// batch — one AfterEdit call keeps the per-run attempt budget accurate and
		// avoids verifying an intermediate edit a later call in the same turn already
		// superseded. Its feedback is appended after the loop so every advertised
		// tool call keeps its tool_result contiguous (a user message interleaved
		// between tool_results breaks strict provider replay) — same after-batch
		// rationale as turnRequestedModel above.
		var changedFilesThisBatch []string
		for index, call := range collected.ToolCalls {
			if options.OnToolCall != nil {
				options.OnToolCall(call)
			}
			toolResult, abortErr := executeToolCall(ctx, registry, call, permissionMode, options)
			if options.OnToolResult != nil {
				options.OnToolResult(toolResult)
			}
			// Union the deferred tools this result asked to load into the per-run
			// set BEFORE any abort/stop/guard branch, so a load that coincides with
			// a turn-ending result is still recorded for the next turn's partition.
			for _, name := range toolResult.LoadedTools {
				loaded[name] = true
			}
			if turnRequestedModel == "" && toolResult.RequestedModel != "" {
				turnRequestedModel = toolResult.RequestedModel
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
			if stopReason := stopReasonFromToolResult(toolResult); stopReason != "" {
				messages = appendAbortedToolResults(messages, collected.ToolCalls[index+1:])
				result.FinalAnswer = toolResult.Output
				result.StopReason = stopReason
				result.Messages = copyMessages(messages)
				return result, nil
			}

			// Repeated-failure guard: if a tool keeps failing the same way, hint
			// once (with its schema) then halt — so no model loops on a bad call.
			// Only RETRIABLE failures (bad arguments / execution errors) drive it:
			// policy refusals (disabled tool, permission denial, sandbox block)
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

			// Post-edit self-correction: collect the files this successful mutating
			// tool changed; verification runs once over the union after the batch.
			// A read-only tool (no ChangedFiles) never contributes.
			if options.SelfCorrect != nil && toolResult.Status == tools.StatusOK && len(toolResult.ChangedFiles) > 0 {
				changedFilesThisBatch = append(changedFilesThisBatch, toolResult.ChangedFiles...)
			}
		}

		// Run post-edit self-correction once over the union of files this turn
		// changed, then append any feedback after every tool_result is recorded so
		// the assistant's tool_results stay contiguous (a user message between
		// tool_results breaks strict provider replay). nil SelfCorrect is a no-op.
		if options.SelfCorrect != nil && len(changedFilesThisBatch) > 0 {
			if feedback, _ := options.SelfCorrect.AfterEdit(ctx, dedupeStrings(changedFilesThisBatch)); feedback != "" {
				messages = append(messages, zeroruntime.Message{
					Role:    zeroruntime.MessageRoleUser,
					Content: feedback,
				})
			}
		}

		// Mid-run model escalation: if a tool asked to switch models this turn and
		// a switcher is wired, rebuild the provider on the new model for the rest
		// of the run. At most one switch per turn (first request wins). A switcher
		// error is NON-FATAL — record a brief note and continue on the current
		// model. nil switcher ⇒ requests are ignored entirely (escalation off).
		if turnRequestedModel != "" && options.ModelSwitcher != nil {
			newProvider, switchErr := options.ModelSwitcher(ctx, turnRequestedModel)
			if switchErr != nil {
				messages = append(messages, zeroruntime.Message{
					Role:    zeroruntime.MessageRoleUser,
					Content: escalationFailedNoticePrefix + " (" + turnRequestedModel + "): " + switchErr.Error() + ". Continuing on " + options.Model + ".",
				})
			} else if newProvider != nil {
				// Reassign the local provider so the next turn's StreamCompletion and
				// compaction use it; update options.Model so subsequent RunOptions.Model,
				// context-window sizing, and usage attribution follow the new model.
				provider = newProvider
				options.Model = turnRequestedModel
				// KNOWN LIMITATION (deferred): the compactor's context-window budget
				// is fixed at run start from options.ContextWindow and is NOT updated
				// here, so a switch to a model with a different window keeps compacting
				// against the original budget. Fixing it needs a ModelSwitcher contract
				// change (return the new window) — out of scope for this change.
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
		} else if reminder := guards.progressReminder(); reminder != "" {
			messages = append(messages, zeroruntime.Message{
				Role:    zeroruntime.MessageRoleUser,
				Content: reminder,
			})
		} else if reminder := guards.planReminder(result.Turns); reminder != "" {
			messages = append(messages, zeroruntime.Message{
				Role:    zeroruntime.MessageRoleUser,
				Content: reminder,
			})
		}
	}

	if ctx.Err() != nil {
		result.Messages = copyMessages(messages)
		return result, ctx.Err()
	}
	if answer, finalMessages, finishReason := finalAnswerAfterMaxTurns(ctx, provider, messages, options); strings.TrimSpace(answer) != "" {
		result.FinalAnswer = answer
		result.FinishReason = finishReason
		result.Messages = copyMessages(finalMessages)
		return result, nil
	}

	result.FinalAnswer = maxTurnsAnswer
	result.Messages = copyMessages(messages)
	return result, nil
}

func finalAnswerAfterMaxTurns(ctx context.Context, provider Provider, messages []zeroruntime.Message, options Options) (string, []zeroruntime.Message, string) {
	finalMessages := copyMessages(messages)
	finalMessages = append(finalMessages, zeroruntime.Message{
		Role:    zeroruntime.MessageRoleUser,
		Content: maxTurnsFinalAnswerPrompt,
	})
	// The max-turns final-answer call is a pre-content connect, often after a long
	// autonomous/cron run — route it through the reconnect helper so a single
	// transient hiccup doesn't drop the final summary (AUDIT-L1).
	stream, err := streamWithReconnect(ctx, provider, zeroruntime.CompletionRequest{
		Messages:        copyMessages(finalMessages),
		ReasoningEffort: options.ReasoningEffort,
	}, reconnectNoticeFor(options))
	if err != nil {
		return "", messages, ""
	}
	collected := zeroruntime.CollectStreamWithOptions(ctx, stream, zeroruntime.CollectOptions{
		OnText:          options.OnText,
		OnReasoning:     options.OnReasoning,
		OnUsage:         options.OnUsage,
		OnToolCallStart: options.OnToolCallStart,
		OnToolCallDelta: options.OnToolCallDelta,
	})
	if ctx.Err() != nil || collected.Error != "" || strings.TrimSpace(collected.Text) == "" {
		return "", messages, ""
	}
	finalMessages = append(finalMessages, zeroruntime.Message{
		Role:    zeroruntime.MessageRoleAssistant,
		Content: collected.Text,
	})
	return collected.Text, finalMessages, collected.FinishReason
}

func historySafeToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	safe := make([]ToolCall, len(calls))
	for i, call := range calls {
		safe[i] = call
		args := strings.TrimSpace(call.Arguments)
		if args == "" || !json.Valid([]byte(args)) {
			safe[i].Arguments = "{}"
		}
	}
	return safe
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
	// tool_search is the gateway to the allowlisted deferred tools, so a non-empty
	// EnabledTools allowlist that omits it must NOT reject the call — otherwise the
	// reminder points the model at a tool the dispatch gate rejects (an inescapable
	// dead-end). The allowlist is exempted; an explicit DisabledTools entry for
	// tool_search is still honored (only the allowlist is exempted, not the denylist).
	toolSearchAllowed := call.Name == tools.ToolSearchToolName && !containsToolName(options.DisabledTools, tools.ToolSearchToolName)
	if !toolSearchAllowed && !ToolAllowedByFilters(call.Name, options.EnabledTools, options.DisabledTools) {
		return ToolResult{
			ToolCallID:   call.ID,
			Name:         call.Name,
			Status:       tools.StatusError,
			Output:       `Error: Tool "` + call.Name + `" is not enabled for this run.`,
			DenialReason: DenialFiltered,
		}, nil
	}
	tool, toolFound := registry.Get(call.Name)
	if permissionMode == PermissionModeSpecDraft && toolFound && !ToolAdvertised(tool, permissionMode) {
		return ToolResult{
			ToolCallID:   call.ID,
			Name:         call.Name,
			Status:       tools.StatusError,
			Output:       `Error: Tool "` + call.Name + `" is not available in spec-draft mode.`,
			DenialReason: DenialFiltered,
		}, nil
	}
	if toolFound {
		if rejecter, ok := tool.(tools.PrePermissionRejecter); ok {
			if result, rejected := rejecter.RejectBeforePermission(args); rejected {
				return toolResultFromPrePermissionReject(call, result), nil
			}
		}
	}

	// ask_user is intercepted in the loop (like permissions) so the question can
	// be routed to an interactive front-end instead of blocking inside the tool.
	// When no front-end is wired up it degrades to the tool's own graceful Run().
	if call.Name == "ask_user" {
		return executeAskUser(ctx, registry, call, args, permissionMode, options)
	}
	if call.Name == tools.RequestPermissionsToolName {
		return executeRequestPermissions(ctx, call, args, permissionMode, options)
	}

	permissionGranted := permissionMode == PermissionModeUnsafe
	if toolFound && effectivePermission(tool, args) == tools.PermissionAllow {
		permissionGranted = true
	}

	decisionReason := ""
	var decisionAction PermissionDecisionAction
	var decisionCommandPrefix []string
	var permissionCleanups []func()
	defer func() {
		for i := len(permissionCleanups) - 1; i >= 0; i-- {
			if permissionCleanups[i] != nil {
				permissionCleanups[i]()
			}
		}
	}()
	if toolFound && !permissionGranted {
		if grant, ok, session := matchCommandPrefix(call.Name, args, options); ok {
			permissionGranted = true
			if session {
				decisionAction = PermissionDecisionAllowPrefix
				decisionReason = "session command prefix approval matched"
			} else {
				decisionAction = PermissionDecisionAlwaysAllowPrefix
				decisionReason = "persistent command prefix approval matched"
			}
			decisionCommandPrefix = grant.Prefix
		}
	}

	var preflightDecision *sandbox.Decision
	if toolFound && options.Sandbox != nil {
		decision := options.Sandbox.Evaluate(ctx, sandboxRequest(call.Name, tool, args, permissionGranted, permissionMode, options))
		preflightDecision = &decision
	}

	if toolFound && options.OnPermissionRequest != nil && shouldRequestPermission(tool, permissionGranted, preflightDecision) {
		requestEvent, ok := buildPermissionEvent(call, tool, args, permissionGranted, permissionMode, options, preflightDecision)
		if !ok {
			requestEvent = fallbackPermissionEvent(call, tool, args, permissionMode, options)
		}
		request := permissionRequestFromEvent(requestEvent, args, options)
		decision, err := requestPermission(ctx, request, options)
		if err != nil {
			decision = PermissionDecision{Action: PermissionDecisionDeny, Reason: err.Error()}
		}
		decision.Action = normalizePermissionDecisionAction(decision.Action)
		decisionReason = strings.TrimSpace(decision.Reason)
		decisionAction = decision.Action
		switch decision.Action {
		case PermissionDecisionAllow, PermissionDecisionAllowStrict:
			permissionGranted = true
			requestEvent.DecisionAction = decision.Action
			cleanup, err := grantNetworkForSandboxPrompt(requestEvent, sandbox.PermissionGrantScopeTurn, options)
			if err != nil {
				reason := "failed to grant network permission: " + err.Error()
				emitDeniedPermission(options, call, requestEvent, reason)
				return deniedPermissionResult(call, reason, requestEvent), nil
			}
			permissionCleanups = append(permissionCleanups, cleanup)
			cleanup, err = grantFilesystemForSandboxPrompt(requestEvent, sandbox.PermissionGrantScopeTurn, options)
			if err != nil {
				reason := "failed to grant filesystem permission: " + err.Error()
				emitDeniedPermission(options, call, requestEvent, reason)
				return deniedPermissionResult(call, reason, requestEvent), nil
			}
			permissionCleanups = append(permissionCleanups, cleanup)
		case PermissionDecisionAllowForSession:
			permissionGranted = true
			requestEvent.DecisionAction = decision.Action
			if networkSandboxPrompt(requestEvent) {
				cleanup, err := grantNetworkForSandboxPrompt(requestEvent, sandbox.PermissionGrantScopeSession, options)
				if err != nil {
					reason := "failed to grant session network permission: " + err.Error()
					emitDeniedPermission(options, call, requestEvent, reason)
					return deniedPermissionResult(call, reason, requestEvent), nil
				}
				permissionCleanups = append(permissionCleanups, cleanup)
			} else if filesystemSandboxPrompt(requestEvent) {
				cleanup, err := grantFilesystemForSandboxPrompt(requestEvent, sandbox.PermissionGrantScopeSession, options)
				if err != nil {
					reason := "failed to grant session filesystem permission: " + err.Error()
					emitDeniedPermission(options, call, requestEvent, reason)
					return deniedPermissionResult(call, reason, requestEvent), nil
				}
				permissionCleanups = append(permissionCleanups, cleanup)
			} else if options.Sandbox != nil {
				// The current call stays allowed if recording the session grant
				// fails; the user is simply prompted again for a later match.
				if grant, err := persistSessionPermissionGrant(call.Name, args, decisionReason, options); err == nil {
					requestEvent.GrantMatched = true
					requestEvent.Grant = &grant
				}
			}
		case PermissionDecisionAllowPrefix:
			if len(request.CommandPrefix) == 0 {
				emitDeniedPermission(options, call, requestEvent, decisionReason)
				return deniedPermissionResult(call, decisionReason, requestEvent), nil
			}
			permissionGranted = true
			requestEvent.DecisionAction = decision.Action
			decisionCommandPrefix = append([]string(nil), request.CommandPrefix...)
			if options.Sandbox != nil && len(decisionCommandPrefix) > 0 {
				options.Sandbox.GrantCommandPrefixForSession(call.Name, decisionCommandPrefix)
			}
			cleanup, err := grantNetworkForSandboxPrompt(requestEvent, sandbox.PermissionGrantScopeTurn, options)
			if err != nil {
				reason := "failed to grant network permission: " + err.Error()
				emitDeniedPermission(options, call, requestEvent, reason)
				return deniedPermissionResult(call, reason, requestEvent), nil
			}
			permissionCleanups = append(permissionCleanups, cleanup)
		case PermissionDecisionAlwaysAllowPrefix:
			if len(request.CommandPrefix) == 0 {
				emitDeniedPermission(options, call, requestEvent, decisionReason)
				return deniedPermissionResult(call, decisionReason, requestEvent), nil
			}
			permissionGranted = true
			requestEvent.DecisionAction = decision.Action
			decisionCommandPrefix = append([]string(nil), request.CommandPrefix...)
			if options.Sandbox != nil && len(decisionCommandPrefix) > 0 {
				if grant, err := persistCommandPrefixGrant(call.Name, decisionCommandPrefix, decisionReason, options); err == nil {
					decisionCommandPrefix = append([]string(nil), grant.Prefix...)
				}
			}
			cleanup, err := grantNetworkForSandboxPrompt(requestEvent, sandbox.PermissionGrantScopeTurn, options)
			if err != nil {
				reason := "failed to grant network permission: " + err.Error()
				emitDeniedPermission(options, call, requestEvent, reason)
				return deniedPermissionResult(call, reason, requestEvent), nil
			}
			permissionCleanups = append(permissionCleanups, cleanup)
		case PermissionDecisionAlwaysAllow:
			permissionGranted = true
			requestEvent.DecisionAction = decision.Action
			// "Always allow" also persists a grant so FUTURE calls skip the prompt.
			// With no sandbox engine there is nowhere to persist it, and persistence
			// can also fail — neither must deny what the user explicitly allowed, so
			// honor the allow for THIS call regardless and just skip remembering the
			// grant (the user is re-prompted next time rather than denied now). This
			// call's permission event is built from the sandbox decision below
			// (buildPermissionEvent reads decision.GrantMatched/Grant), so the
			// persisted grant does not need to be recorded on requestEvent here.
			if options.Sandbox != nil {
				if grant, err := persistPermissionGrant(call.Name, args, decisionReason, options); err == nil {
					requestEvent.GrantMatched = true
					requestEvent.Grant = &grant
				}
			}
		case PermissionDecisionCancel:
			emitCanceledPermission(options, call, requestEvent, decisionReason)
			return canceledPermissionResult(call, decisionReason, requestEvent), fmt.Errorf("%w for %s", errPermissionApprovalCanceled, call.Name)
		default:
			emitDeniedPermission(options, call, requestEvent, decisionReason)
			return deniedPermissionResult(call, decisionReason, requestEvent), nil
		}
	}

	// beforeTool hooks may veto the call before it runs (a non-zero exit blocks).
	if toolFound {
		if outcome, blocked := dispatchBeforeTool(ctx, options, call, args); blocked {
			return blockedByHookResult(call, outcome), nil
		}
	}

	// Task tool: wire progress callback so the TUI sees live tool-call events
	// from the specialist child process.
	var progressCallback func(streamjson.Event)
	if call.Name == "Task" && options.OnToolProgress != nil {
		toolCallID := call.ID
		onProgress := options.OnToolProgress
		progressCallback = func(event streamjson.Event) {
			onProgress(toolCallID, event)
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
		// Per-session file version tracker so write_file/edit_file refuse to clobber
		// a file that changed on disk outside Zero since it was last read.
		FileTracker: options.FileTracker,
		// Forward the run's operator tool filters so a filter-aware tool
		// (tool_search) never discloses or loads an operator-hidden deferred tool.
		EnabledTools:  options.EnabledTools,
		DisabledTools: options.DisabledTools,
		Progress:      progressCallback,
		// The sandbox decision (if any) is returned synchronously on the Result and
		// used here for permission event building.
	})
	sandboxDecision := result.SandboxDecision
	if toolFound && options.OnPermission != nil {
		if event, ok := buildPermissionEvent(call, tool, args, permissionGranted, permissionMode, options, sandboxDecision); ok {
			event.DecisionReason = decisionReason
			if decisionAction != "" {
				event.DecisionAction = decisionAction
			}
			if len(decisionCommandPrefix) > 0 {
				event.CommandPrefix = append([]string(nil), decisionCommandPrefix...)
			}
			options.OnPermission(event)
		}
	}
	// afterTool hooks run once the tool has executed; their output (e.g. a
	// formatter or vet result) is surfaced back to the model on the result.
	if toolFound {
		if feedback := dispatchAfterTool(ctx, options, call, args, result); feedback != "" {
			var didRedact bool
			result.Output, didRedact = appendHookFeedback(result.Output, feedback)
			if didRedact {
				result.Redacted = true
			}
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
		LoadedTools:  loadedToolsFromResult(result.Meta),
		// A tool may signal a mid-run model escalation by carrying the target id
		// in Meta["escalate_to_model"]. Lift it into the typed loop-level field;
		// the Run turn loop performs the actual provider switch. Empty for every
		// ordinary tool result.
		RequestedModel: result.Meta["escalate_to_model"],
	}, nil
}

func toolResultFromPrePermissionReject(call ToolCall, result tools.Result) ToolResult {
	output, outputRedacted := scrubInterceptedOutput(result.Output)
	display := result.Display
	summary, summaryRedacted := scrubInterceptedOutput(display.Summary)
	display.Summary = summary

	meta := result.Meta
	metaRedacted := false
	if len(result.Meta) > 0 {
		meta = make(map[string]string, len(result.Meta))
		for key, value := range result.Meta {
			scrubbed := redaction.RedactString(value, redaction.Options{})
			if scrubbed != value {
				metaRedacted = true
			}
			meta[key] = scrubbed
		}
	}

	return ToolResult{
		ToolCallID:     call.ID,
		Name:           call.Name,
		Status:         result.Status,
		Output:         output,
		Meta:           meta,
		Redacted:       result.Redacted || outputRedacted || summaryRedacted || metaRedacted,
		ChangedFiles:   result.ChangedFiles,
		Display:        display,
		LoadedTools:    loadedToolsFromResult(meta),
		RequestedModel: meta["escalate_to_model"],
	}
}

// dispatchBeforeTool runs configured beforeTool hooks for a tool call. A hook
// that exits non-zero vetoes the call: the returned bool is true and the tool
// must not run. A nil dispatcher (no hooks wired) is a no-op.
func dispatchBeforeTool(ctx context.Context, options Options, call ToolCall, args map[string]any) (hooks.DispatchOutcome, bool) {
	if options.Hooks == nil {
		return hooks.DispatchOutcome{}, false
	}
	outcome := options.Hooks.Dispatch(ctx, hooks.DispatchInput{
		Event:      hooks.EventBeforeTool,
		ToolName:   call.Name,
		ToolCallID: call.ID,
		Payload: map[string]any{
			"event":      string(hooks.EventBeforeTool),
			"tool":       call.Name,
			"toolCallId": call.ID,
			"sessionId":  options.SessionID,
			"cwd":        options.Cwd,
			"args":       args,
		},
	})
	return outcome, outcome.Blocked
}

// dispatchAfterTool runs configured afterTool hooks once a tool has executed and
// returns any advisory output (e.g. a formatter or vet result) to surface back
// to the model. afterTool hooks never block. A nil dispatcher is a no-op.
func dispatchAfterTool(ctx context.Context, options Options, call ToolCall, args map[string]any, result tools.Result) string {
	if options.Hooks == nil {
		return ""
	}
	outcome := options.Hooks.Dispatch(ctx, hooks.DispatchInput{
		Event:      hooks.EventAfterTool,
		ToolName:   call.Name,
		ToolCallID: call.ID,
		Payload: map[string]any{
			"event":        string(hooks.EventAfterTool),
			"tool":         call.Name,
			"toolCallId":   call.ID,
			"sessionId":    options.SessionID,
			"cwd":          options.Cwd,
			"status":       string(result.Status),
			"changedFiles": result.ChangedFiles,
		},
	})
	return strings.TrimSpace(strings.Join(outcome.Messages, "\n"))
}

// blockedByHookResult is the tool result for a call vetoed by a beforeTool hook.
func blockedByHookResult(call ToolCall, outcome hooks.DispatchOutcome) ToolResult {
	// The hook reason (its stdout/stderr) is model-visible here, an intercepted
	// path that bypasses the registry's output redaction boundary — so scrub it
	// like every other string that crosses into the transcript, and flag Redacted
	// when scrubbing changed it (matching the registry's contract).
	scrubbed := redaction.RedactString(outcome.Reason, redaction.Options{})
	redacted := scrubbed != outcome.Reason
	reason := strings.TrimSpace(scrubbed)
	if reason == "" {
		reason = "blocked by a beforeTool hook"
	}
	message := fmt.Sprintf("Error: %q was blocked by hook %q: %s", call.Name, outcome.BlockedBy, reason)
	return ToolResult{
		ToolCallID:   call.ID,
		Name:         call.Name,
		Status:       tools.StatusError,
		Output:       message,
		Redacted:     redacted,
		DenialReason: DenialHookBlocked,
	}
}

// appendHookFeedback appends afterTool hook output to a tool result's output,
// scrubbed for secrets like every other string crossing the tool boundary. The
// returned bool reports whether scrubbing changed the feedback, so the caller can
// set ToolResult.Redacted to match the registry's redaction contract.
func appendHookFeedback(output string, feedback string) (string, bool) {
	scrubbed := redaction.RedactString(feedback, redaction.Options{})
	redacted := scrubbed != feedback
	if strings.TrimSpace(scrubbed) == "" {
		return output, redacted
	}
	if strings.TrimSpace(output) == "" {
		return "Hook output:\n" + scrubbed, redacted
	}
	return output + "\n\nHook output:\n" + scrubbed, redacted
}

// isRetriableToolError reports whether a failed tool result is one the model can
// plausibly fix by changing its next call (argument or execution failure), as
// opposed to a policy refusal (disabled tool, permission denial, sandbox
// block) that no reformatting will satisfy. Only retriable failures should
// drive the repeated-failure schema hint / stop.
func isRetriableToolError(result ToolResult) bool {
	if result.Status != tools.StatusError {
		return false
	}
	// A categorized denial (filtered / permission / sandbox) is a policy decision,
	// not a transient failure — never retry it. This is robust to message wording
	// (the text checks below remain as a fallback for results lacking the field).
	if result.DenialReason != DenialNone {
		return false
	}
	if result.Meta["permission_action"] == string(PermissionActionDeny) {
		return false
	}
	switch {
	case strings.Contains(result.Output, "is not enabled for this run"),
		strings.Contains(result.Output, "Permission denied for "),
		strings.Contains(result.Output, "Permission required for "),
		strings.Contains(result.Output, "Sandbox block"),
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
		Args:              args,
		Reason:            safety.Reason,
	}
}

// effectivePermission returns the permission for THIS specific call. A tool that
// implements tools.ArgsPermissioner may relax its static permission for arguments
// it can prove are safe (the Task tool auto-allows a read-only sub-agent); every
// other tool falls back to its static Safety().Permission unchanged.
func effectivePermission(tool tools.Tool, args map[string]any) tools.Permission {
	if p, ok := tool.(tools.ArgsPermissioner); ok {
		return p.PermissionForArgs(args)
	}
	return tool.Safety().Permission
}

func shouldRequestPermission(tool tools.Tool, permissionGranted bool, decision *sandbox.Decision) bool {
	if decision != nil && decision.Action == sandbox.ActionPrompt {
		return true
	}
	if tool.Safety().Permission != tools.PermissionPrompt {
		return false
	}
	if decision != nil {
		if sandboxDecisionRequiresExplicitPermission(decision) {
			return true
		}
		if permissionGranted {
			return false
		}
		return decision.Action == sandbox.ActionPrompt
	}
	if permissionGranted {
		return false
	}
	return true
}

func sandboxDecisionRequiresExplicitPermission(decision *sandbox.Decision) bool {
	return decision != nil && decision.Action == sandbox.ActionPrompt && decision.Reason == sandbox.ReasonNetworkBlocked
}

func requestPermission(ctx context.Context, request PermissionRequest, options Options) (PermissionDecision, error) {
	if options.OnPermissionRequest == nil {
		return PermissionDecision{Action: PermissionDecisionDeny, Reason: request.Reason}, nil
	}
	return options.OnPermissionRequest(ctx, request)
}

func normalizePermissionDecisionAction(action PermissionDecisionAction) PermissionDecisionAction {
	switch action {
	case PermissionDecisionAllow, PermissionDecisionAllowStrict, PermissionDecisionAllowForSession, PermissionDecisionAllowPrefix, PermissionDecisionAlwaysAllowPrefix, PermissionDecisionAlwaysAllow, PermissionDecisionCancel:
		return action
	default:
		return PermissionDecisionDeny
	}
}

type requestPermissionsArgs struct {
	EnvironmentID string                           `json:"environment_id"`
	Reason        string                           `json:"reason"`
	Permissions   sandbox.RequestPermissionProfile `json:"permissions"`
}

func executeRequestPermissions(ctx context.Context, call ToolCall, args map[string]any, permissionMode PermissionMode, options Options) (ToolResult, error) {
	parsed, err := parseRequestPermissionsArgs(args)
	if err != nil {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusError,
			Output:     "Error: Invalid arguments for request_permissions: " + err.Error(),
		}, nil
	}
	normalized, err := sandbox.NormalizeRequestPermissionProfile(parsed.Permissions, options.Cwd)
	if err != nil {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusError,
			Output:     "Error: Invalid permissions for request_permissions: " + err.Error(),
		}, nil
	}
	if normalized.Empty() {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusError,
			Output:     "Error: request_permissions requires at least one permission.",
		}, nil
	}
	grantProfile, err := sandbox.RequestPermissionGrantProfile(normalized)
	if err != nil {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusError,
			Output:     "Error: Invalid permissions for request_permissions: " + err.Error(),
		}, nil
	}

	request := requestPermissionsPrompt(call, parsed, grantProfile, permissionMode, options)
	if options.OnPermissionRequest == nil {
		response := sandbox.RequestPermissionsResponse{
			Permissions: sandbox.RequestPermissionProfile{},
			Scope:       sandbox.PermissionGrantScopeTurn,
		}
		return requestPermissionsResult(call, response, false), nil
	}

	decision, err := requestPermission(ctx, request, options)
	if err != nil {
		decision = PermissionDecision{Action: PermissionDecisionDeny, Reason: err.Error()}
	}
	decision.Action = normalizePermissionDecisionAction(decision.Action)
	decisionReason := strings.TrimSpace(decision.Reason)
	if decisionReason == "" {
		decisionReason = request.Reason
	}

	response := sandbox.RequestPermissionsResponse{
		Permissions: sandbox.RequestPermissionProfile{},
		Scope:       sandbox.PermissionGrantScopeTurn,
	}
	permissionGranted := false
	switch decision.Action {
	case PermissionDecisionAllow:
		response.Permissions = grantProfile
		response.Scope = sandbox.PermissionGrantScopeTurn
		permissionGranted = true
	case PermissionDecisionAllowStrict:
		response.Permissions = grantProfile
		response.Scope = sandbox.PermissionGrantScopeTurn
		response.StrictAutoReview = true
		permissionGranted = true
	case PermissionDecisionAllowForSession:
		response.Permissions = grantProfile
		response.Scope = sandbox.PermissionGrantScopeSession
		permissionGranted = true
	case PermissionDecisionCancel:
		decision.Action = PermissionDecisionDeny
		decisionReason = firstNonEmptyString(decisionReason, "continued without granting permissions")
	default:
		decision.Action = PermissionDecisionDeny
		decisionReason = firstNonEmptyString(decisionReason, "continued without granting permissions")
	}

	if permissionGranted {
		if options.Sandbox == nil {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Status:     tools.StatusError,
				Output:     "Error: request_permissions cannot grant permissions because the sandbox engine is not configured.",
			}, nil
		}
		cleanup, err := options.Sandbox.GrantRequestPermissions(response.Permissions, response.Scope)
		if err != nil {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Status:     tools.StatusError,
				Output:     "Error: Failed to grant requested permissions: " + err.Error(),
			}, nil
		}
		if response.Scope == sandbox.PermissionGrantScopeTurn && options.runPermissions != nil {
			options.runPermissions.add(cleanup)
		}
	}

	emitRequestPermissionsDecision(options, call, request, decision.Action, decisionReason, permissionGranted)
	return requestPermissionsResult(call, response, false), nil
}

func parseRequestPermissionsArgs(args map[string]any) (requestPermissionsArgs, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return requestPermissionsArgs{}, err
	}
	var parsed requestPermissionsArgs
	if err := json.Unmarshal(data, &parsed); err != nil {
		return requestPermissionsArgs{}, err
	}
	if parsed.EnvironmentID == "" {
		if raw, ok := args["environmentId"].(string); ok {
			parsed.EnvironmentID = raw
		}
	}
	return parsed, nil
}

func requestPermissionsPrompt(call ToolCall, parsed requestPermissionsArgs, profile sandbox.RequestPermissionProfile, permissionMode PermissionMode, options Options) PermissionRequest {
	autonomy := options.Autonomy
	reason := strings.TrimSpace(parsed.Reason)
	if reason == "" {
		reason = "The agent is requesting additional permissions."
	}
	return PermissionRequest{
		ToolCallID:     call.ID,
		ToolName:       tools.RequestPermissionsToolName,
		Action:         PermissionActionPrompt,
		Permission:     string(tools.PermissionPrompt),
		PermissionMode: permissionMode,
		Autonomy:       autonomy,
		SideEffect:     "permissions",
		Reason:         reason,
		Scope:          requestPermissionsScope(profile),
		Args: map[string]any{
			"environment_id": parsed.EnvironmentID,
			"reason":         parsed.Reason,
			"permissions":    profile,
		},
		AvailableDecisions: []PermissionDecisionAction{
			PermissionDecisionAllow,
			PermissionDecisionAllowStrict,
			PermissionDecisionAllowForSession,
			PermissionDecisionDeny,
		},
	}
}

func requestPermissionsScope(profile sandbox.RequestPermissionProfile) string {
	parts := []string{}
	if profile.Network != nil && profile.Network.Enabled != nil && *profile.Network.Enabled {
		parts = append(parts, "network all outbound access")
	}
	if profile.FileSystem != nil {
		for _, path := range profile.FileSystem.Read {
			parts = append(parts, "read "+path)
		}
		for _, path := range profile.FileSystem.Write {
			parts = append(parts, "write "+path)
		}
		for _, path := range profile.FileSystem.DenyRead {
			parts = append(parts, "deny read "+path)
		}
	}
	return strings.Join(parts, ", ")
}

func emitRequestPermissionsDecision(options Options, call ToolCall, request PermissionRequest, action PermissionDecisionAction, reason string, granted bool) {
	if options.OnPermission == nil {
		return
	}
	eventAction := PermissionActionDeny
	if granted {
		eventAction = PermissionActionAllow
	}
	options.OnPermission(PermissionEvent{
		ToolCallID:        call.ID,
		ToolName:          call.Name,
		Action:            eventAction,
		DecisionAction:    action,
		Permission:        request.Permission,
		PermissionGranted: granted,
		PermissionMode:    request.PermissionMode,
		Autonomy:          request.Autonomy,
		SideEffect:        request.SideEffect,
		Reason:            reason,
		Scope:             request.Scope,
		DecisionReason:    reason,
	})
}

func requestPermissionsResult(call ToolCall, response sandbox.RequestPermissionsResponse, redacted bool) ToolResult {
	data, err := json.Marshal(response)
	if err != nil {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusError,
			Output:     "Error: Failed to serialize request_permissions response: " + err.Error(),
		}
	}
	output, didRedact := scrubInterceptedOutput(string(data))
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusOK,
		Output:     output,
		Redacted:   redacted || didRedact,
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func persistPermissionGrant(toolName string, args map[string]any, reason string, options Options) (sandbox.Grant, error) {
	if options.Sandbox == nil {
		return sandbox.Grant{}, errors.New("sandbox engine is not configured")
	}
	// Scope the grant to exactly what the permission card showed (the file or
	// directory the call touches); engine.Grant anchors it to the workspace. A
	// call with no path-like argument yields an empty scope — a tool-wide grant.
	scope, kind := sandbox.DeriveScope(toolName, args)
	return options.Sandbox.Grant(sandbox.GrantInput{
		ToolName:  toolName,
		Decision:  sandbox.GrantAllow,
		Reason:    reason,
		Scope:     scope,
		ScopeKind: kind,
	})
}

func persistSessionPermissionGrant(toolName string, args map[string]any, reason string, options Options) (sandbox.Grant, error) {
	if options.Sandbox == nil {
		return sandbox.Grant{}, errors.New("sandbox engine is not configured")
	}
	scope, kind := sandbox.DeriveScope(toolName, args)
	return options.Sandbox.GrantForSession(sandbox.GrantInput{
		ToolName:  toolName,
		Decision:  sandbox.GrantAllow,
		Reason:    reason,
		Scope:     scope,
		ScopeKind: kind,
	})
}

func persistCommandPrefixGrant(toolName string, prefix []string, reason string, options Options) (sandbox.CommandPrefixGrant, error) {
	if options.Sandbox == nil {
		return sandbox.CommandPrefixGrant{}, errors.New("sandbox engine is not configured")
	}
	return options.Sandbox.GrantCommandPrefix(sandbox.CommandPrefixInput{
		ToolName: toolName,
		Prefix:   prefix,
		Reason:   reason,
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
	event.DecisionAction = PermissionDecisionDeny
	event.PermissionGranted = false
	event.DecisionReason = reason
	options.OnPermission(event)
}

func emitCanceledPermission(options Options, call ToolCall, requestEvent PermissionEvent, reason string) {
	if options.OnPermission == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled in TUI"
	}
	event := requestEvent
	event.ToolCallID = call.ID
	event.ToolName = call.Name
	event.Action = PermissionActionCancel
	event.DecisionAction = PermissionDecisionCancel
	event.PermissionGranted = false
	event.DecisionReason = reason
	event.Reason = reason
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
	event.DecisionAction = PermissionDecisionDeny
	event.PermissionGranted = false
	event.DecisionReason = reason
	if event.Risk.Level == "" {
		event.Risk = sandbox.Risk{Level: sandbox.RiskMedium, Reason: reason}
	}
	if requestEvent.ToolName == "" {
		event.ToolName = call.Name
	}
	// A denial driven by a sandbox block is categorized distinctly from a
	// plain approval-declined so a surface can tell policy from user choice.
	denial := DenialPermissionDenied
	if requestEvent.Block != nil {
		denial = DenialSandboxBlock
	}
	return ToolResult{
		ToolCallID:   call.ID,
		Name:         call.Name,
		Status:       tools.StatusError,
		Output:       "Error: Permission denied for " + call.Name + ": " + reason,
		DenialReason: denial,
		Meta: map[string]string{
			"permission_action": string(event.Action),
		},
	}
}

func canceledPermissionResult(call ToolCall, reason string, requestEvent PermissionEvent) ToolResult {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled in TUI"
	}
	event := requestEvent
	event.Action = PermissionActionCancel
	event.DecisionAction = PermissionDecisionCancel
	event.PermissionGranted = false
	event.DecisionReason = reason
	event.Reason = reason
	if requestEvent.ToolName == "" {
		event.ToolName = call.Name
	}
	return ToolResult{
		ToolCallID:   call.ID,
		Name:         call.Name,
		Status:       tools.StatusError,
		Output:       "Error: Permission approval cancelled for " + call.Name + ": " + reason,
		DenialReason: DenialApprovalCanceled,
		Meta: map[string]string{
			"permission_action": string(event.Action),
		},
	}
}

// permissionScope returns a concise, human-readable description of what a tool
// call will actually touch -- a file path, directory, working dir, or network
// host lifted from its arguments -- so the permission card and persisted
// decision can show the user exactly what "allow" covers. It is empty when the
// tool exposes no scoped argument (the grant is then plainly tool-wide). The
// first matching key wins, ordered most-specific (a concrete file) first.
func permissionScope(toolName string, args map[string]any) string {
	// sandbox.DeriveScope is the single source of truth for which arguments carry
	// a scope, shared with grant persistence and matching so the card display can
	// never diverge from what an "always allow" actually covers.
	raw, _ := sandbox.DeriveScope(toolName, args)
	if raw == "" {
		return ""
	}
	return truncateScope(raw)
}

func truncateScope(value string) string {
	const maxScopeRunes = 80
	runes := []rune(value)
	if len(runes) <= maxScopeRunes {
		return value
	}
	return string(runes[:maxScopeRunes-1]) + "…"
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
		Args:              args,
		Reason:            safety.Reason,
	})
	var block *sandbox.Block
	grantMatched := false
	var grant *sandbox.Grant

	if decision != nil {
		action = permissionActionFromSandbox(decision.Action)
		if decision.Reason != "" {
			reason = decision.Reason
		}
		risk = decision.Risk
		block = decision.Block
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

	if safety.Permission == tools.PermissionAllow && action == PermissionActionAllow && !grantMatched && block == nil {
		return PermissionEvent{}, false
	}

	return PermissionEvent{
		ToolCallID:        call.ID,
		ToolName:          call.Name,
		Action:            action,
		DecisionAction:    grantDecisionAction(grant),
		Permission:        string(safety.Permission),
		PermissionGranted: permissionGranted,
		PermissionMode:    permissionMode,
		Autonomy:          options.Autonomy,
		SideEffect:        string(safety.SideEffect),
		Reason:            reason,
		Scope:             permissionScope(call.Name, args),
		Risk:              risk,
		Block:             block,
		GrantMatched:      grantMatched,
		Grant:             grant,
		CommandPrefix:     proposedCommandPrefix(call.Name, args),
	}, true
}

func grantDecisionAction(grant *sandbox.Grant) PermissionDecisionAction {
	if grant == nil {
		return ""
	}
	if grant.Session {
		return PermissionDecisionAllowForSession
	}
	if grant.Decision == sandbox.GrantAllow {
		return PermissionDecisionAlwaysAllow
	}
	return ""
}

func fallbackPermissionEvent(call ToolCall, tool tools.Tool, args map[string]any, permissionMode PermissionMode, options Options) PermissionEvent {
	event, _ := buildPermissionEvent(call, tool, args, false, permissionMode, options, nil)
	return event
}

func permissionRequestFromEvent(event PermissionEvent, args map[string]any, options Options) PermissionRequest {
	return PermissionRequest{
		ToolCallID:         event.ToolCallID,
		ToolName:           event.ToolName,
		Action:             event.Action,
		Permission:         event.Permission,
		PermissionMode:     event.PermissionMode,
		Autonomy:           event.Autonomy,
		SideEffect:         event.SideEffect,
		Reason:             event.Reason,
		Scope:              event.Scope,
		Risk:               event.Risk,
		Args:               cloneArgs(args),
		Block:              event.Block,
		GrantMatched:       event.GrantMatched,
		Grant:              event.Grant,
		CommandPrefix:      append([]string(nil), event.CommandPrefix...),
		AvailableDecisions: availablePermissionDecisions(event, options),
	}
}

func availablePermissionDecisions(event PermissionEvent, options Options) []PermissionDecisionAction {
	if event.Action != PermissionActionPrompt {
		return nil
	}
	decisions := []PermissionDecisionAction{PermissionDecisionAllow}
	if options.Sandbox != nil {
		decisions = append(decisions, PermissionDecisionAllowForSession)
		if isShellCommandTool(event.ToolName) && len(event.CommandPrefix) > 0 && !networkSandboxPrompt(event) {
			decisions = append(decisions, PermissionDecisionAllowPrefix)
			if options.Sandbox.CanPersistGrants() {
				decisions = append(decisions, PermissionDecisionAlwaysAllowPrefix)
			}
		}
		if options.Sandbox.CanPersistGrants() && permissionSupportsPersistentDecision(event.ToolName) && !filesystemSandboxPrompt(event) {
			decisions = append(decisions, PermissionDecisionAlwaysAllow)
		}
	}
	switch {
	case isShellCommandTool(event.ToolName):
		decisions = append(decisions, PermissionDecisionDeny, PermissionDecisionCancel)
	case event.ToolName == "apply_patch":
		decisions = append(decisions, PermissionDecisionCancel)
	default:
		decisions = append(decisions, PermissionDecisionDeny)
	}
	return decisions
}

func networkSandboxPrompt(event PermissionEvent) bool {
	return isShellCommandTool(event.ToolName) &&
		event.Reason == sandbox.ReasonNetworkBlocked &&
		sandbox.HasRiskCategory(event.Risk, "network")
}

func grantNetworkForSandboxPrompt(event PermissionEvent, scope sandbox.PermissionGrantScope, options Options) (func(), error) {
	if !networkSandboxPrompt(event) || options.Sandbox == nil {
		return nil, nil
	}
	enabled := true
	return options.Sandbox.GrantRequestPermissions(sandbox.RequestPermissionProfile{
		Network: &sandbox.NetworkPermissions{Enabled: &enabled},
	}, scope)
}

func filesystemSandboxPrompt(event PermissionEvent) bool {
	return event.Block != nil &&
		event.Block.Code == sandbox.BlockOutsideWorkspace &&
		event.Block.Recoverable &&
		strings.TrimSpace(event.Block.Path) != ""
}

func grantFilesystemForSandboxPrompt(event PermissionEvent, scope sandbox.PermissionGrantScope, options Options) (func(), error) {
	if !filesystemSandboxPrompt(event) || options.Sandbox == nil {
		return nil, nil
	}
	path := event.Block.Path
	fs := sandbox.FileSystemPermissions{}
	switch sandbox.SideEffect(event.SideEffect) {
	case sandbox.SideEffectRead:
		fs.Read = []string{path}
	case sandbox.SideEffectWrite, sandbox.SideEffectOutOfWorkspace:
		fs.Write = []string{path}
	default:
		return nil, nil
	}
	return options.Sandbox.GrantRequestPermissions(sandbox.RequestPermissionProfile{
		FileSystem: &fs,
	}, scope)
}

func permissionSupportsPersistentDecision(toolName string) bool {
	switch toolName {
	case "bash", "exec_command", "write_stdin", "apply_patch":
		return false
	default:
		return true
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

// partitionTools builds the per-turn advertised tool list and an optional
// deferred-tools reminder. INACTIVE (DeferThreshold <= 0 or the eligible count is
// below it): every visible tool is exposed with its full schema EXCEPT tool_search
// (dropped so it is never advertised when it cannot help), and the reminder is
// empty — byte-identical to the pre-deferral output. ACTIVE: a deferred-eligible
// tool is exposed only when loaded[name]; otherwise it is hidden and its compact
// line goes into the reminder. Non-deferred tools (including tool_search) are
// always exposed. The exposed slice is alpha-sorted by name, matching the legacy
// order so the inactive path is stable.
func partitionTools(registry *tools.Registry, permissionMode PermissionMode, options Options, loaded map[string]bool) ([]zeroruntime.ToolDefinition, string) {
	registeredTools := registry.All()

	visible := make([]tools.Tool, 0, len(registeredTools))
	eligible := 0
	for _, tool := range registeredTools {
		if !ToolVisible(tool, permissionMode, options.EnabledTools, options.DisabledTools) {
			continue
		}
		visible = append(visible, tool)
		// Count by deferral-ELIGIBILITY, not current deferred state: a tool that
		// un-defers at runtime (e.g. swarm coordination tools once a swarm is
		// active) still counts, so it can't drop `eligible` below the threshold
		// and force-expose every other deferred tool. Whether a tool is actually
		// hidden is decided by IsDeferred in the exposure loop below.
		if tools.IsDeferralEligible(tool) {
			eligible++
		}
	}

	// Deferral may activate only when tool_search is actually runnable; otherwise
	// the loop would hide deferred tools behind a loader the dispatch gate rejects
	// — an inescapable dead-end. "Runnable" mirrors executeToolCall's gate:
	// registered, not in DisabledTools, and advertised in the current permission
	// mode (e.g. not spec-draft, where tool_search is not advertised). The
	// EnabledTools allowlist is intentionally NOT checked here — tool_search is
	// exempt from the allowlist at dispatch, so an allowlist that omits it must
	// not disable deferral.
	loader, loaderFound := registry.Get(tools.ToolSearchToolName)
	loaderUsable := loaderFound &&
		!containsToolName(options.DisabledTools, tools.ToolSearchToolName) &&
		ToolAdvertised(loader, permissionMode)

	active := options.DeferThreshold > 0 && eligible >= options.DeferThreshold && loaderUsable

	definitions := make([]zeroruntime.ToolDefinition, 0, len(visible))
	exposedNames := make(map[string]bool, len(visible))
	var hiddenLines []string
	for _, tool := range visible {
		name := tool.Name()
		deferred := tools.IsDeferred(tool)

		if !active {
			// Inactive: byte-identical to legacy, but tool_search is never advertised.
			if name == tools.ToolSearchToolName {
				continue
			}
			definitions = append(definitions, zeroruntime.ToolDefinition{
				Name:        name,
				Description: tool.Description(),
				Parameters:  schemaToRuntimeMap(tool.Parameters()),
			})
			exposedNames[name] = true
			continue
		}

		// Active path.
		if deferred && !loaded[name] {
			hiddenLines = append(hiddenLines, tools.DeferredLine(tool))
			continue
		}
		definitions = append(definitions, zeroruntime.ToolDefinition{
			Name:        name,
			Description: tool.Description(),
			Parameters:  schemaToRuntimeMap(tool.Parameters()),
		})
		exposedNames[name] = true
	}

	// On the ACTIVE path tool_search is guaranteed runnable (active implies
	// loaderUsable), so it must ALWAYS be reachable — even when a non-empty
	// EnabledTools allowlist omits it (the operator allowlisted the deferred
	// tools, not the loader). Expose its full definition whenever it is not
	// already in the exposed set. This never runs on the inactive path, so the
	// byte-identical below-threshold output is preserved.
	if active && !exposedNames[tools.ToolSearchToolName] {
		definitions = append(definitions, zeroruntime.ToolDefinition{
			Name:        loader.Name(),
			Description: loader.Description(),
			Parameters:  schemaToRuntimeMap(loader.Parameters()),
		})
	}

	sort.Slice(definitions, func(left int, right int) bool {
		return definitions[left].Name < definitions[right].Name
	})

	reminder := ""
	if active {
		reminder = tools.BuildDeferredReminder(hiddenLines)
	}
	return definitions, reminder
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
	if permissionMode == PermissionModeSpecDraft {
		return toolAdvertisedInSpecDraft(tool)
	}
	if permissionMode == PermissionModeAuto {
		return tool.Safety().Permission == tools.PermissionAllow || tool.Safety().AdvertiseInAuto
	}
	return true
}

func toolAdvertisedInSpecDraft(tool tools.Tool) bool {
	switch tool.Name() {
	case "ask_user", "submit_spec":
		return true
	case "update_plan":
		return false
	}
	safety := tool.Safety()
	return safety.SideEffect == tools.SideEffectRead && safety.Permission == tools.PermissionAllow
}

func stopReasonFromToolResult(result ToolResult) StopReason {
	if result.Meta == nil {
		return ""
	}
	if result.Meta[toolResultMetaControl] == toolResultControlSpecReview {
		return StopReasonSpecReviewRequired
	}
	return ""
}

// loadedToolsFromResult extracts the deferred-tool names a tool (tool_search)
// asked the loop to expose next turn from Meta["load_tools"] (comma-separated).
// It trims each name and drops empties; returns nil when the key is absent or
// yields no names, so an ordinary result keeps a nil LoadedTools. Mirrors the
// Meta-driven control signal read by stopReasonFromToolResult.
func loadedToolsFromResult(meta map[string]string) []string {
	raw := meta["load_tools"]
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var names []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
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

// dedupeStrings returns values with duplicates removed, preserving first-seen
// order so the deduped list (e.g. a turn's changed files) stays deterministic.
func dedupeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func copyMessages(messages []Message) []Message {
	copied := make([]Message, len(messages))
	for index, message := range messages {
		copied[index] = message
		if message.ToolCalls != nil {
			copied[index].ToolCalls = append([]ToolCall{}, message.ToolCalls...)
		}
		if message.Reasoning != nil {
			copied[index].Reasoning = append([]zeroruntime.ReasoningBlock{}, message.Reasoning...)
		}
		// Deep-copy image attachments (slice AND each Data byte slice) so the
		// raw image bytes are never aliased across history/request/result copies.
		copied[index].Images = zeroruntime.CloneImageBlocks(message.Images)
	}
	return copied
}
