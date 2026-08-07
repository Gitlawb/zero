package agent

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestProjectCompactionInputDropsRecoverableToolBodies(t *testing.T) {
	largeBody := strings.Repeat("recoverable source line\n", 1000)
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleUser, Content: "Inspect the parser and preserve its current behavior."},
		{Role: zeroruntime.MessageRoleAssistant, Content: "I will inspect before editing.", ToolCalls: []zeroruntime.ToolCall{{ID: "read", Name: "read_file", Arguments: `{"path":"internal/parser.go"}`}}},
		{Role: zeroruntime.MessageRoleTool, ToolCallID: "read", Content: largeBody},
		{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{{ID: "test", Name: "exec_command", Arguments: `{"cmd":"go test ./internal/parser"}`}}},
		{Role: zeroruntime.MessageRoleTool, ToolCallID: "test", Content: "Error: parser regression\nlong diagnostic details"},
	}

	projected := projectCompactionInput(messages)
	if len(projected) != 1 {
		t.Fatalf("projection = %#v, want one compact message", projected)
	}
	content := projected[0].Content
	for _, want := range []string{"Inspect the parser", `read_file "internal/parser.go"`, `exec_command "go test ./internal/parser"`, "Error: parser regression"} {
		if !strings.Contains(content, want) {
			t.Fatalf("projection missing %q: %q", want, content)
		}
	}
	if strings.Contains(content, "recoverable source line") || strings.Contains(content, "long diagnostic details") {
		t.Fatalf("projection retained reconstructible or overlong tool output: %q", content)
	}
	if estimateTokens(projected)*20 >= estimateTokens(messages) {
		t.Fatalf("projection did not materially reduce tokens: before=%d after=%d", estimateTokens(messages), estimateTokens(projected))
	}
}

func TestProjectCompactionInputRetainsStructurallyMarkedToolErrors(t *testing.T) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{{ID: "git", Name: "exec_command", Arguments: `{"cmd":"git status"}`}}},
		{Role: zeroruntime.MessageRoleTool, ToolCallID: "git", Content: "fatal: not a git repository", IsError: true},
	}

	projected := projectCompactionInput(messages)
	if len(projected) != 1 || !strings.Contains(projected[0].Content, "fatal: not a git repository") {
		t.Fatalf("structured tool error was dropped from projection: %#v", projected)
	}
}

func TestProjectCompactionInputCapsToolCallsPerTurn(t *testing.T) {
	calls := make([]zeroruntime.ToolCall, 12)
	for index := range calls {
		calls[index] = zeroruntime.ToolCall{Name: "read_file", Arguments: `{"path":"file.go"}`}
	}
	projected := projectCompactionInput([]zeroruntime.Message{{Role: zeroruntime.MessageRoleAssistant, ToolCalls: calls}})
	content := projected[0].Content
	if strings.Count(content, "* read_file") != compactionToolCallsPerTurn || !strings.Contains(content, "4 earlier tool calls omitted") {
		t.Fatalf("tool-call tail was not bounded: %q", content)
	}
}

func TestProjectCompactionInputCarriesPreviousSummaryOutsideBriefCap(t *testing.T) {
	messages := []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: summaryLabel + "\nkeep the earlier architecture decision\n\n" + preservedStateLabel + "\n{}"}}
	for range compactionBriefMaxLines {
		messages = append(messages, zeroruntime.Message{Role: zeroruntime.MessageRoleAssistant, Content: "later transcript detail"})
		messages = append(messages, zeroruntime.Message{Role: zeroruntime.MessageRoleUser, Content: "later request"})
	}

	content := projectCompactionInput(messages)[0].Content
	if !strings.Contains(content, "[previous summary]\nkeep the earlier architecture decision") {
		t.Fatalf("previous summary was lost when the brief tail was capped: %q", content)
	}
	if !strings.Contains(content, "earlier lines omitted") {
		t.Fatalf("oversized brief was not capped: %q", content)
	}
}
