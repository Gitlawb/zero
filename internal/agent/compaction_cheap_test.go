package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// compactableHistory returns a history whose middle is large enough to trip
// the proactive threshold for a 1000-token window.
func compactableHistory() []zeroruntime.Message {
	return []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: "sys"},
		{Role: zeroruntime.MessageRoleUser, Content: strings.Repeat("u", 4000)},
		{Role: zeroruntime.MessageRoleAssistant, Content: strings.Repeat("a", 4000)},
		{Role: zeroruntime.MessageRoleUser, Content: "u2"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "a2"},
		{Role: zeroruntime.MessageRoleUser, Content: "u3"},
	}
}

// Summarization must run on the dedicated (cheap) summarizer when one is
// configured — the main provider sees no summarize traffic at all.
func TestMaybeCompactUsesDedicatedSummarizer(t *testing.T) {
	cheap := &mockProvider{turns: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "CHEAP SUMMARY"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	main := &mockProvider{}
	factoryCalls := 0
	state := newCompactionState(Options{
		ContextWindow:          1000,
		CompactionPreserveLast: 2,
		Summarizer: func(context.Context) (Provider, error) {
			factoryCalls++
			return cheap, nil
		},
	})

	compacted := state.maybeCompact(context.Background(), main, compactableHistory(), nil)

	if len(cheap.requests) == 0 {
		t.Fatal("dedicated summarizer never received the summarize call")
	}
	if len(main.requests) != 0 {
		t.Fatalf("main provider must see no summarize traffic, got %d requests", len(main.requests))
	}
	if factoryCalls != 1 {
		t.Fatalf("summarizer factory calls = %d, want 1 (lazy, memoized)", factoryCalls)
	}
	if estimateTokens(compacted) >= estimateTokens(compactableHistory()) {
		t.Fatal("compaction did not shrink the history")
	}
	found := false
	for _, message := range compacted {
		if strings.Contains(message.Content, "CHEAP SUMMARY") {
			found = true
		}
	}
	if !found {
		t.Fatal("cheap summarizer's summary missing from the compacted history")
	}
}

// A failing summarizer falls back to the main provider for the SAME slice and
// stays broken for the rest of the run — no repeated failed cheap calls.
func TestSummarizerFailureFallsBackToMainAndSticks(t *testing.T) {
	broken := &mockProvider{turns: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventError, Error: "boom: model not found"},
	}}}
	main := &mockProvider{turns: [][]zeroruntime.StreamEvent{
		{{Type: zeroruntime.StreamEventText, Content: "MAIN SUMMARY"}, {Type: zeroruntime.StreamEventDone}},
		{{Type: zeroruntime.StreamEventText, Content: "MAIN SUMMARY 2"}, {Type: zeroruntime.StreamEventDone}},
	}}
	state := newCompactionState(Options{
		ContextWindow:          1000,
		CompactionPreserveLast: 2,
		Summarizer:             func(context.Context) (Provider, error) { return broken, nil },
	})
	summarize := state.summarizeClosureFor(context.Background(), main)
	input := []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "history"}}

	summary, err := summarize(input)
	if err != nil {
		t.Fatalf("fallback must succeed via the main provider: %v", err)
	}
	if summary != "MAIN SUMMARY" {
		t.Fatalf("summary = %q, want the main provider's", summary)
	}
	if len(broken.requests) != 1 {
		t.Fatalf("broken summarizer requests = %d, want exactly 1", len(broken.requests))
	}
	if !state.summarizerBroken {
		t.Fatal("a failed summarizer must be marked broken for the run")
	}

	// Second summarization: straight to main, no cheap retry.
	if _, err := summarize(input); err != nil {
		t.Fatal(err)
	}
	if len(broken.requests) != 1 {
		t.Fatalf("broken summarizer was retried (%d requests)", len(broken.requests))
	}
	if len(main.requests) != 2 {
		t.Fatalf("main provider requests = %d, want 2", len(main.requests))
	}
}

// A factory that errors (misconfigured model) is equivalent to no summarizer:
// main provider used, factory not retried.
func TestSummarizerFactoryErrorFallsBack(t *testing.T) {
	main := &mockProvider{turns: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "MAIN SUMMARY"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	factoryCalls := 0
	state := newCompactionState(Options{
		ContextWindow:          1000,
		CompactionPreserveLast: 2,
		Summarizer: func(context.Context) (Provider, error) {
			factoryCalls++
			return nil, errors.New("unknown model")
		},
	})
	summarize := state.summarizeClosureFor(context.Background(), main)

	summary, err := summarize([]zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "history"}})
	if err != nil || summary != "MAIN SUMMARY" {
		t.Fatalf("summary = %q, err = %v; want main provider fallback", summary, err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1 (sticky failure)", factoryCalls)
	}
	if state.summarizeProvider(context.Background(), main) != Provider(main) {
		t.Fatal("after a factory failure the run must stay on the main provider")
	}
	if factoryCalls != 1 {
		t.Fatalf("factory retried after failure (%d calls)", factoryCalls)
	}
}

// Tool payloads are clamped in the summarizer transcript: results to
// summaryToolResultClamp, call arguments to summaryToolArgsClamp.
func TestRenderTranscriptClampsToolPayloads(t *testing.T) {
	bigResult := strings.Repeat("r", 50_000)
	bigArgs := strings.Repeat("w", 20_000)
	transcript := renderTranscript([]zeroruntime.Message{
		{Role: zeroruntime.MessageRoleAssistant, Content: "editing", ToolCalls: []zeroruntime.ToolCall{
			{ID: "c1", Name: "write_file", Arguments: bigArgs},
		}},
		{Role: zeroruntime.MessageRoleTool, ToolCallID: "c1", Content: bigResult},
		{Role: zeroruntime.MessageRoleUser, Content: "keep going"},
	})

	if len(transcript) > summaryToolResultClamp+summaryToolArgsClamp+1024 {
		t.Fatalf("transcript is %d bytes; clamps did not apply", len(transcript))
	}
	if !strings.Contains(transcript, "chars omitted for summarization") {
		t.Fatal("clamped transcript must carry the omission marker")
	}
	if !strings.Contains(transcript, "keep going") {
		t.Fatal("non-tool content must survive unclamped")
	}
	// Head AND tail of the clamped payloads survive.
	if !strings.HasPrefix(transcript[strings.Index(transcript, "tool result: ")+len("tool result: "):], "r") ||
		!strings.Contains(transcript, "rrr\n\nuser: keep going") {
		t.Fatalf("head/tail of the tool result lost:\n%s", transcript[:200])
	}
}

// A context-limit error is first answered with the free prune stage; the paid
// summarizer only runs when a later error finds nothing left to prune. The
// free retry must not consume the one-shot reactive budget.
func TestRecoverPrunesBeforeSummarizing(t *testing.T) {
	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "SUMMARY"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	state := newCompactionState(Options{ContextWindow: 1000, CompactionPreserveLast: 2})

	// Five big tool results (~40k estimated tokens each): the trailing
	// protection window covers the newest, the older ones are prunable.
	bigBody := strings.Repeat("x", 160_000)
	messages := []zeroruntime.Message{{Role: zeroruntime.MessageRoleSystem, Content: "sys"}}
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		messages = append(messages,
			zeroruntime.Message{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{{ID: id, Name: "read_file", Arguments: "{}"}}},
			zeroruntime.Message{Role: zeroruntime.MessageRoleTool, ToolCallID: id, Content: bigBody},
		)
	}
	messages = append(messages, zeroruntime.Message{Role: zeroruntime.MessageRoleUser, Content: "go"})

	pruned, retried, err := state.recover(context.Background(), provider, messages, nil, "maximum context length exceeded")
	if err != nil {
		t.Fatal(err)
	}
	if !retried {
		t.Fatal("prunable history must produce a free retry")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("free prune stage must not call the summarizer (%d requests)", len(provider.requests))
	}
	if state.reactiveAttempted {
		t.Fatal("a free prune retry must not consume the reactive budget")
	}
	if estimateTokens(pruned) >= estimateTokens(messages) {
		t.Fatal("prune did not shrink the history")
	}

	// Second context-limit error on the pruned history: nothing meaningfully
	// prunable remains, so the paid summarizer fires and consumes the budget.
	_, retried, err = state.recover(context.Background(), provider, pruned, nil, "maximum context length exceeded")
	if err != nil {
		t.Fatal(err)
	}
	if !retried {
		t.Fatal("second recover must compact and retry")
	}
	if len(provider.requests) == 0 {
		t.Fatal("second recover must reach the summarizer")
	}
	if !state.reactiveAttempted {
		t.Fatal("a paid recover must consume the reactive budget")
	}
}

func TestCompactionUpdateWindow(t *testing.T) {
	state := newCompactionState(Options{ContextWindow: 100_000, CompactionPreserveLast: 2})
	state.lowWaterMark = 50_000
	before := state.threshold

	state.updateWindow(0) // unknown model: unchanged
	if state.threshold != before || state.lowWaterMark != 50_000 {
		t.Fatal("updateWindow(0) must be a no-op")
	}

	state.updateWindow(10_000)
	if state.threshold >= before {
		t.Fatalf("threshold = %d, want re-derived below %d", state.threshold, before)
	}
	if state.lowWaterMark != 0 {
		t.Fatal("low-water mark must reset on a window change")
	}
	if !state.enabled {
		t.Fatal("a positive window must keep compaction enabled")
	}
}

// End-to-end: after escalate_model switches providers, the loop re-derives the
// context window via ContextWindowFor, visible in the OnContext report.
func TestRunEscalationRederivesContextWindow(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(escalatingTool{target: "big-model"})
	switched := &mockProvider{turns: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "done"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "escalate"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{}`},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
		{Type: zeroruntime.StreamEventDone},
	}}}

	var windows []int
	result, err := Run(context.Background(), "go", provider, Options{
		Registry:      registry,
		ContextWindow: 100_000,
		Model:         "small-model",
		ModelSwitcher: func(context.Context, string) (Provider, error) { return switched, nil },
		ContextWindowFor: func(modelID string) int {
			if modelID == "big-model" {
				return 400_000
			}
			return 0
		},
		OnContext: func(breakdown ContextBreakdown) { windows = append(windows, breakdown.ContextWindow) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("final answer = %q", result.FinalAnswer)
	}
	if len(windows) < 2 {
		t.Fatalf("expected at least 2 OnContext reports, got %d", len(windows))
	}
	if windows[0] != 100_000 {
		t.Fatalf("turn 1 window = %d, want the original 100000", windows[0])
	}
	if windows[len(windows)-1] != 400_000 {
		t.Fatalf("post-escalation window = %d, want 400000", windows[len(windows)-1])
	}
}
