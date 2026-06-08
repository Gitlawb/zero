package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Session compaction.
//
// When a running conversation approaches the model's context window, the oldest
// middle of the history is summarized into a single user-role note while the
// system prompt(s) and the most recent turns are kept verbatim. This keeps long
// sessions from blowing the context budget.
//
// Compact is a PURE function: it never talks to a provider. The actual LLM call
// is injected via CompactionOptions.Summarize so it stays trivially testable and
// the agent loop owns the provider wiring.

// defaultCompactionPreserveLast is how many trailing messages are kept verbatim
// when the caller does not specify a preserve count.
const defaultCompactionPreserveLast = 8

// compactionTriggerRatio is the fraction of the context window at which
// proactive compaction kicks in (top of each turn).
const compactionTriggerRatio = 0.8

// summaryLabel prefixes the injected summary so it is unmistakable in the
// transcript (and so tests can assert on it).
const summaryLabel = "[Summary of earlier conversation]"

// summaryInstructions is the system prompt handed to the summarizer model.
const summaryInstructions = "You are compacting a coding-assistant conversation to save context. " +
	"Write a dense, factual summary of the conversation so far. Preserve: the user's goals and explicit constraints; " +
	"decisions made and why; files created or modified (with paths) and key code changes; commands run and their important " +
	"results; and anything still in progress or unresolved. Omit pleasantries. Use terse bullet points. Do not invent details. " +
	"If the conversation already begins with an earlier summary block, treat its facts as established context and carry them " +
	"forward into the new summary — never drop earlier information."

// CompactionOptions configure a single Compact call.
type CompactionOptions struct {
	// PreserveLast is the number of trailing messages to keep verbatim. The
	// preserved suffix is widened (never shrunk) so it begins at a safe
	// user/assistant boundary. <= 0 falls back to defaultCompactionPreserveLast.
	PreserveLast int
	// Summarize turns the to-be-elided middle into a single dense summary. It is
	// injected so Compact stays pure and testable; the agent loop wires it to a
	// real provider call.
	Summarize func(toSummarize []zeroruntime.Message) (string, error)
}

// estimateTokens is a cheap, dependency-free token estimate (~4 chars/token)
// across message content plus tool call names/arguments. It deliberately uses no
// real tokenizer; it only needs to be monotonic and roughly proportional so the
// loop can decide when to compact.
func estimateTokens(messages []zeroruntime.Message) int {
	total := 0
	for _, message := range messages {
		total += len(message.Content) / 4
		for _, call := range message.ToolCalls {
			total += len(call.Name) / 4
			total += len(call.Arguments) / 4
			total += 4 // small per-call overhead
		}
		total += 4 // per-message overhead
	}
	return total
}

// compactionThreshold is the estimated-token level at which proactive
// compaction triggers for a given context window.
func compactionThreshold(contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	return int(float64(contextWindow) * compactionTriggerRatio)
}

// Compact summarizes the oldest middle of a conversation, keeping the leading
// system message(s) and the most recent turns verbatim. The result is:
//
//	[system..., summaryAsUser, preservedSuffix...]
//
// Rules:
//   - Leading system messages stay at the head untouched.
//   - The preserved suffix is widened backward so it never begins with a
//     tool/tool_result message (provider APIs reject a dangling tool result
//     with no preceding assistant tool call).
//   - The summary is injected as a single user-role message labeled with
//     summaryLabel.
//   - If there is nothing to summarize (too few messages once system and the
//     preserved suffix are removed), the input is returned unchanged.
//
// Compact is pure: it performs no provider I/O. A Summarize error is returned to
// the caller, which decides how to recover.
func Compact(messages []zeroruntime.Message, opts CompactionOptions) ([]zeroruntime.Message, error) {
	preserveLast := opts.PreserveLast
	if preserveLast <= 0 {
		preserveLast = defaultCompactionPreserveLast
	}
	if opts.Summarize == nil {
		return nil, errors.New("compaction requires a Summarize function")
	}

	// Leading system messages are kept verbatim at the head.
	systemEnd := 0
	for systemEnd < len(messages) && messages[systemEnd].Role == zeroruntime.MessageRoleSystem {
		systemEnd++
	}

	// Naive boundary: keep the last preserveLast messages. Then widen the suffix
	// backward to a safe boundary so it never starts on a tool result.
	boundary := len(messages) - preserveLast
	if boundary < systemEnd {
		boundary = systemEnd
	}
	boundary = safeSuffixBoundary(messages, systemEnd, boundary)

	middle := messages[systemEnd:boundary]
	if len(middle) == 0 {
		// Nothing to summarize once system + preserved suffix are removed.
		return messages, nil
	}

	summary, err := opts.Summarize(middle)
	if err != nil {
		return nil, err
	}
	summary = strings.TrimSpace(summary)

	// Preserve structured state (active plan + loaded skills) from the elided
	// middle verbatim, so it is not lost or paraphrased away by the prose summary.
	content := appendPreservedState(summaryLabel+"\n"+summary, middle)

	compacted := make([]zeroruntime.Message, 0, systemEnd+1+(len(messages)-boundary))
	compacted = append(compacted, messages[:systemEnd]...)
	compacted = append(compacted, zeroruntime.Message{
		Role:    zeroruntime.MessageRoleUser,
		Content: content,
	})
	compacted = append(compacted, messages[boundary:]...)
	return compacted, nil
}

// safeSuffixBoundary walks the preserve boundary backward (toward systemEnd) so
// the preserved suffix begins on a user or assistant message rather than a
// tool/tool_result message. A tool result with no preceding assistant tool call
// is rejected by provider APIs, so the boundary must land on a safe turn start.
// It never moves the boundary forward (the suffix only grows), and never crosses
// systemEnd.
func safeSuffixBoundary(messages []zeroruntime.Message, systemEnd int, boundary int) int {
	// Walk back so the preserved suffix begins with an assistant message. The
	// summary is injected as a user-role message, so a user- or tool-led suffix
	// would create consecutive same-role turns that strict providers (Anthropic)
	// reject. Stopping on an assistant keeps user/assistant alternation valid;
	// if no assistant exists above systemEnd, boundary lands at systemEnd and the
	// middle is empty, so Compact no-ops (no summary is injected).
	for boundary > systemEnd && messages[boundary].Role != zeroruntime.MessageRoleAssistant {
		boundary--
	}
	return boundary
}

// isContextLimitError reports whether a provider error string looks like a
// context-window / prompt-too-long error from a common provider. Matching is
// substring-based and case-insensitive so it tolerates phrasing differences
// across OpenAI, Anthropic, and Google providers.
func isContextLimitError(message string) bool {
	lowered := strings.ToLower(strings.TrimSpace(message))
	if lowered == "" {
		return false
	}
	needles := []string{
		"context length",
		"context window",
		"context_length_exceeded",
		"maximum context",
		"context limit",
		"prompt is too long",
		"too many tokens",
		"reduce the length of the messages",
		"exceeds the maximum",
		"input is too long",
	}
	for _, needle := range needles {
		if strings.Contains(lowered, needle) {
			return true
		}
	}
	return false
}

// compactionState carries the per-run state the agent loop needs to compact a
// conversation safely. It is created once per Run and is a no-op whenever
// options.ContextWindow <= 0.
type compactionState struct {
	enabled      bool
	threshold    int
	preserveLast int
	// lowWaterMark is the estimated token size at (or below) which we will NOT
	// proactively compact again. It is the size right after the last compaction;
	// the loop only compacts when the history has grown past it AND is over the
	// threshold. This prevents compacting on every turn once near the limit.
	lowWaterMark int
	// reactiveAttempted guards the reactive path so it fires at most once per
	// run. Without this a provider that keeps returning context-limit errors
	// (even after compaction) could loop indefinitely; one attempt then the
	// original error surfaces.
	reactiveAttempted bool
}

func newCompactionState(options Options) *compactionState {
	return &compactionState{
		enabled:      options.ContextWindow > 0,
		threshold:    compactionThreshold(options.ContextWindow),
		preserveLast: options.CompactionPreserveLast,
	}
}

// maybeCompact runs proactive compaction at the top of a turn. It returns the
// (possibly compacted) message slice. It is a no-op when compaction is disabled,
// when the history is under threshold, or when the history has not grown past
// the low-water mark since the last compaction (the infinite-loop guard).
func (state *compactionState) maybeCompact(
	ctx context.Context,
	provider Provider,
	messages []zeroruntime.Message,
) []zeroruntime.Message {
	if !state.enabled {
		return messages
	}
	size := estimateTokens(messages)
	if size <= state.threshold {
		return messages
	}
	// Only compact when the history has grown past where we last left it. This
	// stops the loop from re-summarizing an already-compacted history every turn
	// when it sits just over the threshold.
	if state.lowWaterMark > 0 && size <= state.lowWaterMark {
		return messages
	}

	compacted, err := Compact(messages, CompactionOptions{
		PreserveLast: state.preserveLast,
		Summarize:    summarizeClosure(ctx, provider),
	})
	if err != nil {
		// Summarizer failed: keep the original history. The reactive path (or a
		// later turn) can try again; we never drop messages on failure here.
		return messages
	}
	newSize := estimateTokens(compacted)
	if newSize >= size {
		// Compaction did not actually shrink anything (e.g. nothing to
		// summarize). Leave the history untouched and don't churn next turn.
		state.lowWaterMark = size
		return messages
	}
	state.lowWaterMark = newSize
	return compacted
}

// recover runs reactive compaction after a provider/stream error. It compacts
// at most once per run when the error looks like a context-limit error and the
// history can actually be shrunk. The returned booleans are (compacted, retried) and the
// error is non-nil only when compaction itself failed (so the loop should give
// up). When retried is false the caller keeps its original error.
func (state *compactionState) recover(
	ctx context.Context,
	provider Provider,
	messages []zeroruntime.Message,
	errorMessage string,
) (compacted []zeroruntime.Message, retried bool, err error) {
	if !state.enabled {
		// Compaction disabled (ContextWindow==0): stay a strict no-op so a
		// context-limit error never triggers an unexpected summarization call.
		return messages, false, nil
	}
	if state.reactiveAttempted {
		return messages, false, nil
	}
	if !isContextLimitError(errorMessage) {
		return messages, false, nil
	}

	result, compactErr := Compact(messages, CompactionOptions{
		PreserveLast: state.preserveLast,
		Summarize:    summarizeClosure(ctx, provider),
	})
	if compactErr != nil {
		// A genuine compaction attempt was made (and failed): the budget is spent
		// so the loop gives up rather than retrying a failing summarizer forever.
		state.reactiveAttempted = true
		return messages, true, compactErr
	}
	if estimateTokens(result) >= estimateTokens(messages) {
		// Nothing to compact; the retry would just fail again. Signal "not
		// retried" so the caller surfaces the original context-limit error. Do NOT
		// consume the one-shot budget here: a no-op recover (history too small to
		// shrink) must not disable a later recovery once the history has grown.
		return messages, false, nil
	}
	// Success: a real compaction shrank the history and we will retry. Consume the
	// one-shot budget now so a provider that keeps returning context-limit errors
	// after a successful compaction can't loop forever.
	state.reactiveAttempted = true
	state.lowWaterMark = estimateTokens(result)
	return result, true, nil
}

// summarizeClosure builds a Summarize function backed by a focused, tool-less
// provider call. The summary stream intentionally does NOT forward OnText /
// OnUsage callbacks, so compaction stays invisible on the user-facing surface.
func summarizeClosure(ctx context.Context, provider Provider) func([]zeroruntime.Message) (string, error) {
	return func(toSummarize []zeroruntime.Message) (string, error) {
		return summarizeWithFallback(ctx, provider, toSummarize)
	}
}

// summarizeWithFallback summarizes messages in a single provider call. If that
// call fails with a context-limit error — the slice to summarize is itself too
// large for the model's input window — it splits the slice in half, summarizes
// each half recursively, and joins the partial summaries. This keeps compaction
// working when the elided middle is bigger than the summarizer's own context.
// Non-context-limit errors (and a single message that still won't fit) surface
// to the caller unchanged.
func summarizeWithFallback(ctx context.Context, provider Provider, messages []zeroruntime.Message) (string, error) {
	summary, err := summarizeMessagesOnce(ctx, provider, messages)
	if err == nil {
		return summary, nil
	}
	if len(messages) < 2 || !isContextLimitError(err.Error()) {
		return "", err
	}

	mid := len(messages) / 2
	left, leftErr := summarizeWithFallback(ctx, provider, messages[:mid])
	if leftErr != nil {
		return "", leftErr
	}
	right, rightErr := summarizeWithFallback(ctx, provider, messages[mid:])
	if rightErr != nil {
		return "", rightErr
	}

	// Re-summarize the two partial summaries into ONE so the persisted summary
	// stays a single, further-summarizable unit — not an ever-growing concatenated
	// blob that a later compaction (which can't split a single message) would fail
	// on. If even the combined partials don't fit (extreme), fall back to the
	// joined text: still better than failing, and each half is already compacted.
	combined := strings.TrimSpace(left + "\n\n" + right)
	reduced, reduceErr := summarizeMessagesOnce(ctx, provider, []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleUser, Content: combined},
	})
	if reduceErr != nil {
		return combined, nil
	}
	return reduced, nil
}

// summarizeMessagesOnce performs a single tool-less summarization call.
func summarizeMessagesOnce(ctx context.Context, provider Provider, messages []zeroruntime.Message) (string, error) {
	request := zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleSystem, Content: summaryInstructions},
			{Role: zeroruntime.MessageRoleUser, Content: "Summarize this conversation:\n\n" + renderTranscript(messages)},
		},
		// No tools: this is a plain text summarization call.
	}
	stream, err := provider.StreamCompletion(ctx, request)
	if err != nil {
		return "", err
	}
	collected := zeroruntime.CollectStream(ctx, stream)
	if collected.Error != "" {
		return "", errors.New(collected.Error)
	}
	summary := strings.TrimSpace(collected.Text)
	if summary == "" {
		return "", errors.New("summarizer returned no text")
	}
	return summary, nil
}

// renderTranscript flattens messages into a plain-text transcript for the
// summarizer. Secret scrubbing already happened upstream at the tool boundary.
func renderTranscript(messages []zeroruntime.Message) string {
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case zeroruntime.MessageRoleAssistant:
			line := "assistant: " + message.Content
			if len(message.ToolCalls) > 0 {
				calls := make([]string, 0, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					calls = append(calls, call.Name+"("+call.Arguments+")")
				}
				line += "\n[tool calls: " + strings.Join(calls, "; ") + "]"
			}
			lines = append(lines, line)
		case zeroruntime.MessageRoleTool:
			lines = append(lines, "tool result: "+message.Content)
		default:
			lines = append(lines, string(message.Role)+": "+message.Content)
		}
	}
	return strings.Join(lines, "\n\n")
}
