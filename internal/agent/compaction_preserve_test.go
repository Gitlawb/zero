package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// stateConversation is a long enough conversation that Compact elides a middle
// containing an update_plan call and a loaded skill (call + result).
func stateConversation() []zeroruntime.Message {
	return []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: "system"},
		{Role: zeroruntime.MessageRoleUser, Content: "build the thing"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "planning", ToolCalls: []zeroruntime.ToolCall{
			{ID: "p1", Name: "update_plan", Arguments: `{"plan":[{"content":"write code","status":"in_progress"},{"content":"add tests","status":"pending"}]}`},
		}},
		{Role: zeroruntime.MessageRoleTool, Content: "plan updated", ToolCallID: "p1"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "loading skill", ToolCalls: []zeroruntime.ToolCall{
			{ID: "s1", Name: "skill", Arguments: `{"name":"deploy"}`},
		}},
		{Role: zeroruntime.MessageRoleTool, Content: "Deploy skill: run `make deploy` then tag the release.", ToolCallID: "s1"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "done step 1"},
		{Role: zeroruntime.MessageRoleUser, Content: "continue"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "continuing"},
	}
}

func compactStateConversation(t *testing.T, messages []zeroruntime.Message) string {
	t.Helper()
	compacted, err := Compact(messages, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]zeroruntime.Message) (string, error) { return "SUMMARY", nil },
	})
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	// [system, summaryUserMsg, ...suffix] — the summary is the message after system.
	if len(compacted) < 2 || compacted[1].Role != zeroruntime.MessageRoleUser {
		t.Fatalf("unexpected compacted shape: %#v", compacted)
	}
	if !strings.Contains(compacted[1].Content, summaryLabel) {
		t.Fatalf("summary message missing label: %q", compacted[1].Content)
	}
	return compacted[1].Content
}

func TestCompactPreservesActivePlan(t *testing.T) {
	summary := compactStateConversation(t, stateConversation())
	if !strings.Contains(summary, planPreserveLabel) {
		t.Fatalf("expected plan preserve section, got %q", summary)
	}
	for _, want := range []string{"- [in_progress] write code", "- [pending] add tests"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("plan item %q not preserved in %q", want, summary)
		}
	}
}

func TestCompactPreservesLoadedSkills(t *testing.T) {
	summary := compactStateConversation(t, stateConversation())
	if !strings.Contains(summary, skillsPreserveLabel) {
		t.Fatalf("expected skills preserve section, got %q", summary)
	}
	if !strings.Contains(summary, "### deploy") || !strings.Contains(summary, "make deploy") {
		t.Fatalf("skill name/body not preserved in %q", summary)
	}
}

func TestCompactWithoutStateHasNoPreserveSections(t *testing.T) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: "system"},
		{Role: zeroruntime.MessageRoleUser, Content: "hello"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "hi there"},
		{Role: zeroruntime.MessageRoleUser, Content: "tell me more"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "sure"},
		{Role: zeroruntime.MessageRoleUser, Content: "and more"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "ok"},
	}
	summary := compactStateConversation(t, messages)
	if strings.Contains(summary, planPreserveLabel) || strings.Contains(summary, skillsPreserveLabel) {
		t.Fatalf("did not expect preserve sections without plan/skill: %q", summary)
	}
}

func TestCompactCarriesPreservedStateAcrossRepeatedCompaction(t *testing.T) {
	// First compaction: real update_plan + skill load in the elided middle.
	first, err := Compact(stateConversation(), CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]zeroruntime.Message) (string, error) { return "FIRST SUMMARY", nil },
	})
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}

	// Grow the history so the first summary (which holds the preserved sections,
	// but no real tool calls) falls into the SECOND compaction's middle.
	second := append([]zeroruntime.Message{}, first...)
	second = append(second,
		zeroruntime.Message{Role: zeroruntime.MessageRoleUser, Content: "what next"},
		zeroruntime.Message{Role: zeroruntime.MessageRoleAssistant, Content: "continuing"},
		zeroruntime.Message{Role: zeroruntime.MessageRoleUser, Content: "keep going"},
		zeroruntime.Message{Role: zeroruntime.MessageRoleAssistant, Content: "done"},
	)

	// The second summarizer deliberately DROPS the preserved sections.
	out, err := Compact(second, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]zeroruntime.Message) (string, error) { return "SECOND SUMMARY with no preserved sections", nil },
	})
	if err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if len(out) < 2 || out[1].Role != zeroruntime.MessageRoleUser {
		t.Fatalf("unexpected compacted shape: %#v", out)
	}
	newSummary := out[1].Content
	// Plan and skill must survive even though the summarizer didn't copy them.
	if !strings.Contains(newSummary, planPreserveLabel) || !strings.Contains(newSummary, "write code") {
		t.Fatalf("active plan lost on the second compaction: %q", newSummary)
	}
	if !strings.Contains(newSummary, "### deploy") || !strings.Contains(newSummary, "make deploy") {
		t.Fatalf("loaded skill lost on the second compaction: %q", newSummary)
	}
}

func TestExtractLatestPlanReturnsMostRecent(t *testing.T) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{
			{ID: "a", Name: "update_plan", Arguments: `{"plan":[{"content":"old step","status":"completed"}]}`},
		}},
		{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{
			{ID: "b", Name: "update_plan", Arguments: `{"plan":[{"content":"new step","status":"in_progress"}]}`},
		}},
	}
	got := extractLatestPlan(messages)
	if !strings.Contains(got, "new step") || strings.Contains(got, "old step") {
		t.Fatalf("extractLatestPlan should return the most recent plan, got %q", got)
	}
}

func TestCapBodyShortBodyUnchanged(t *testing.T) {
	body := "short skill body"
	if got := capBody(body); got != body {
		t.Fatalf("capBody changed a short body: %q", got)
	}
	if strings.Contains(capBody(body), "truncated") {
		t.Fatalf("note added without truncation")
	}
}

func TestCapBodyRespectsByteBudgetForMultibyte(t *testing.T) {
	// Each '世' is 3 bytes; build a body well over the byte budget.
	body := strings.Repeat("世", maxPreservedSkillBytes)
	got := capBody(body)
	if len(got) > maxPreservedSkillBytes {
		t.Fatalf("capBody returned %d bytes, want <= %d (byte budget)", len(got), maxPreservedSkillBytes)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation note on an over-budget body")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("capBody split a multibyte rune (invalid UTF-8)")
	}
}

func TestCapBodyNoteOnlyWhenTruncated(t *testing.T) {
	// A body whose RUNE count is under the cap but BYTE length is over it must
	// still be byte-capped (the old rune-based check mishandled this case).
	body := strings.Repeat("世", (maxPreservedSkillBytes/3)+100)
	if len(body) <= maxPreservedSkillBytes {
		t.Fatalf("test setup: body should exceed the byte budget, got %d", len(body))
	}
	got := capBody(body)
	if len(got) > maxPreservedSkillBytes {
		t.Fatalf("capBody returned %d bytes, want <= %d", len(got), maxPreservedSkillBytes)
	}
	if !strings.Contains(got, "truncated") || !utf8.ValidString(got) {
		t.Fatalf("expected a valid, truncated body, got %q", got)
	}
}

func TestExtractLoadedSkillsSkipsCallsWithoutResult(t *testing.T) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{
			{ID: "s1", Name: "skill", Arguments: `{"name":"ghost"}`}, // no matching tool result
		}},
	}
	if got := extractLoadedSkills(messages); got != "" {
		t.Fatalf("expected no skills without a result body, got %q", got)
	}
}
