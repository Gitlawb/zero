package agent

import (
	"reflect"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestContextPlannerPreservesProviderRequest(t *testing.T) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: "system"},
		{Role: zeroruntime.MessageRoleUser, Content: "inspect this", Images: []zeroruntime.ImageBlock{{MediaType: "image/png", Data: []byte{1, 2, 3}}}},
		{Role: zeroruntime.MessageRoleAssistant, Content: "working", ToolCalls: []zeroruntime.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"main.go"}`}}, Reasoning: []zeroruntime.ReasoningBlock{{Provider: "test", Type: "thinking", Signature: "sig"}}},
	}
	toolDefs := []zeroruntime.ToolDefinition{{Name: "read_file", Description: "Read a file", Parameters: map[string]any{"type": "object"}}}
	planner := newContextPlanner(contextPlannerConfig{
		contextWindow:  128_000,
		promptCacheKey: "session-1",
		promptParts:    systemPromptParts{prompt: "system", baseInstructions: "system"},
	})

	plan := planner.Plan(messages, toolDefs, "medium")
	want := zeroruntime.CompletionRequest{
		Messages:        copyMessages(messages),
		Tools:           toolDefs,
		ReasoningEffort: "medium",
		PromptCacheKey:  "session-1",
	}
	if !reflect.DeepEqual(plan.Request, want) {
		t.Fatalf("planned request changed provider input:\n got: %#v\nwant: %#v", plan.Request, want)
	}

	// A plan owns its request snapshot; later caller mutation cannot change it.
	messages[1].Content = "changed"
	messages[1].Images[0].Data[0] = 9
	if plan.Request.Messages[1].Content != "inspect this" || plan.Request.Messages[1].Images[0].Data[0] != 1 {
		t.Fatalf("planned messages alias caller state: %#v", plan.Request.Messages[1])
	}
}

func TestContextPlannerReportsInspectableBlocks(t *testing.T) {
	planner := newContextPlanner(contextPlannerConfig{contextWindow: 100_000})
	plan := planner.Plan([]zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: filler(400)},
		{Role: zeroruntime.MessageRoleUser, Content: filler(200)},
	}, []zeroruntime.ToolDefinition{{Name: "read_file", Description: "Read a file"}}, "")

	if plan.Breakdown.PrefixInvalidationReason != "initial" || plan.Breakdown.CompletePrefixHash == "" {
		t.Fatalf("prefix evidence = %#v", plan.Breakdown)
	}
	if len(plan.Breakdown.Blocks) != 3 {
		t.Fatalf("blocks = %#v, want system/tools/conversation", plan.Breakdown.Blocks)
	}
	wantKinds := []string{"system", "tools", "conversation"}
	total := 0
	for index, block := range plan.Breakdown.Blocks {
		if block.Kind != wantKinds[index] || block.Tokens <= 0 || block.Reason == "" || block.Authority == "" || block.CacheClass == "" {
			t.Fatalf("block %d = %#v", index, block)
		}
		total += block.Tokens
	}
	if total != plan.Breakdown.TotalTokens {
		t.Fatalf("block tokens = %d, want total %d", total, plan.Breakdown.TotalTokens)
	}
}

func TestContextPlannerExplainsPrefixInvalidation(t *testing.T) {
	planner := newContextPlanner(contextPlannerConfig{promptParts: systemPromptParts{
		prompt: "system", baseInstructions: "base", projectContext: "project",
	}})
	messages := []zeroruntime.Message{{Role: zeroruntime.MessageRoleSystem, Content: "system"}, {Role: zeroruntime.MessageRoleUser, Content: "task"}}
	tools := []zeroruntime.ToolDefinition{{Name: "read_file", Description: "Read", Parameters: map[string]any{"type": "object"}}}

	if got := planner.Plan(messages, tools, "").Breakdown.PrefixInvalidationReason; got != "initial" {
		t.Fatalf("first reason = %q, want initial", got)
	}
	if got := planner.Plan(messages, tools, "").Breakdown.PrefixInvalidationReason; got != "unchanged" {
		t.Fatalf("stable reason = %q, want unchanged", got)
	}
	tools[0].Description = "Read an exact file"
	if got := planner.Plan(messages, tools, "").Breakdown.PrefixInvalidationReason; got != "tools" {
		t.Fatalf("tool reason = %q, want tools", got)
	}
	tools[0].Parameters["required"] = []any{"path"}
	if got := planner.Plan(messages, tools, "").Breakdown.PrefixInvalidationReason; got != "schema" {
		t.Fatalf("schema reason = %q, want schema", got)
	}
	planner.config.promptParts.prompt = "system with changed project"
	planner.config.promptParts.projectContext = "changed project"
	if got := planner.Plan(messages, tools, "").Breakdown.PrefixInvalidationReason; got != "project_context" {
		t.Fatalf("project reason = %q, want project_context", got)
	}
}

func TestExplainPrefixChangeNamesEveryChangedComponent(t *testing.T) {
	baseline := prefixFingerprint{
		SystemPromptHash:       "system",
		BaseInstructionsHash:   "base",
		ConfirmationPolicyHash: "policy",
		ProjectContextHash:     "project",
		SkillsHash:             "skills",
		ToolsHash:              "tools",
		SchemaHash:             "schema",
		CompletePrefixHash:     "complete",
	}
	tests := []struct {
		name   string
		mutate func(*prefixFingerprint)
		want   string
	}{
		{name: "base instructions", mutate: func(value *prefixFingerprint) {
			value.SystemPromptHash, value.BaseInstructionsHash = "changed", "changed"
		}, want: "base_instructions"},
		{name: "confirmation policy", mutate: func(value *prefixFingerprint) {
			value.SystemPromptHash, value.ConfirmationPolicyHash = "changed", "changed"
		}, want: "confirmation_policy"},
		{name: "project context", mutate: func(value *prefixFingerprint) {
			value.SystemPromptHash, value.ProjectContextHash = "changed", "changed"
		}, want: "project_context"},
		{name: "skills", mutate: func(value *prefixFingerprint) { value.SystemPromptHash, value.SkillsHash = "changed", "changed" }, want: "skills"},
		{name: "unclassified system prompt", mutate: func(value *prefixFingerprint) { value.SystemPromptHash = "changed" }, want: "system_prompt"},
		{name: "tools and schema", mutate: func(value *prefixFingerprint) { value.ToolsHash, value.SchemaHash = "changed", "changed" }, want: "tools,schema"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := baseline
			test.mutate(&current)
			current.CompletePrefixHash = "changed"
			if got := explainPrefixChange(&baseline, current); got != test.want {
				t.Fatalf("reason = %q, want %q", got, test.want)
			}
		})
	}
}
