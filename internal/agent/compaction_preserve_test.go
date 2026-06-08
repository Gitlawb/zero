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
	if !strings.Contains(summary, preservedStateLabel) {
		t.Fatalf("expected preserved-state block, got %q", summary)
	}
	for _, want := range []string{"- [in_progress] write code", "- [pending] add tests"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("plan item %q not preserved in %q", want, summary)
		}
	}
}

func TestCompactPreservesLoadedSkills(t *testing.T) {
	summary := compactStateConversation(t, stateConversation())
	if !strings.Contains(summary, preservedStateLabel) {
		t.Fatalf("expected preserved-state block, got %q", summary)
	}
	if !strings.Contains(summary, `"name":"deploy"`) || !strings.Contains(summary, "make deploy") {
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
	if strings.Contains(summary, preservedStateLabel) {
		t.Fatalf("did not expect a preserved-state block without plan/skill: %q", summary)
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
	if !strings.Contains(newSummary, preservedStateLabel) || !strings.Contains(newSummary, "write code") {
		t.Fatalf("active plan lost on the second compaction: %q", newSummary)
	}
	if !strings.Contains(newSummary, `"name":"deploy"`) || !strings.Contains(newSummary, "make deploy") {
		t.Fatalf("loaded skill lost on the second compaction: %q", newSummary)
	}
}

// TestCompactPreservesSkillBodyWithMarkdownHeadings is CodeRabbit's regression:
// a verbatim skill body containing ## / ### headings (and a code fence) must
// round-trip across TWO compactions without truncation or bogus extra skills —
// which the old markdown-delimited format could not guarantee.
func TestCompactPreservesSkillBodyWithMarkdownHeadings(t *testing.T) {
	body := "## Usage\nrun it\n### Examples\n```\nzero do\n```\n## Notes\ndone"
	conv := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: "system"},
		{Role: zeroruntime.MessageRoleUser, Content: "load a skill"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "loading", ToolCalls: []zeroruntime.ToolCall{
			{ID: "s1", Name: "skill", Arguments: `{"name":"guide"}`},
		}},
		{Role: zeroruntime.MessageRoleTool, Content: body, ToolCallID: "s1"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "done step 1"},
		{Role: zeroruntime.MessageRoleUser, Content: "continue"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "continuing"},
	}

	mustContainBody := func(label string, messages []zeroruntime.Message) []zeroruntime.Message {
		out, err := Compact(messages, CompactionOptions{
			PreserveLast: 2,
			Summarize:    func([]zeroruntime.Message) (string, error) { return "SUMMARY", nil },
		})
		if err != nil {
			t.Fatalf("%s Compact: %v", label, err)
		}
		if len(out) < 2 || out[1].Role != zeroruntime.MessageRoleUser {
			t.Fatalf("%s: unexpected compacted shape: %#v", label, out)
		}
		_, skills := parsePreservedState(out[1].Content)
		if len(skills) != 1 || skills[0].name != "guide" || skills[0].body != body {
			t.Fatalf("%s: skill body not round-tripped intact: %#v", label, skills)
		}
		return out
	}

	first := mustContainBody("first", conv)
	// Second compaction with NO fresh tool calls and a summarizer that drops it.
	second := append(append([]zeroruntime.Message{}, first...),
		zeroruntime.Message{Role: zeroruntime.MessageRoleUser, Content: "more"},
		zeroruntime.Message{Role: zeroruntime.MessageRoleAssistant, Content: "ok"},
		zeroruntime.Message{Role: zeroruntime.MessageRoleUser, Content: "again"},
		zeroruntime.Message{Role: zeroruntime.MessageRoleAssistant, Content: "fine"},
	)
	mustContainBody("second", second)
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

func TestLoadedSkillsSkipsCallsWithoutResult(t *testing.T) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{
			{ID: "s1", Name: "skill", Arguments: `{"name":"ghost"}`}, // no matching tool result
		}},
	}
	if got := loadedSkills(messages); len(got) != 0 {
		t.Fatalf("expected no skills without a result body, got %#v", got)
	}
}
