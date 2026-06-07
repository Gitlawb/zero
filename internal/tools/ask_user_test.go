package tools

import (
	"context"
	"strings"
	"testing"
)

func TestAskUserToolSafetyIsReadOnly(t *testing.T) {
	safety := NewAskUserTool().Safety()
	if safety.Permission != PermissionAllow {
		t.Fatalf("expected ask_user to be permission=allow, got %q", safety.Permission)
	}
	if safety.SideEffect != SideEffectRead {
		t.Fatalf("expected ask_user side effect read, got %q", safety.SideEffect)
	}
}

func TestAskUserToolAdvertisesQuestionSchema(t *testing.T) {
	schema := NewAskUserTool().Parameters()
	questions, ok := schema.Properties["questions"]
	if !ok {
		t.Fatal("expected ask_user to advertise a questions property")
	}
	if questions.Type != "array" || questions.Items == nil {
		t.Fatalf("expected questions to be an array with item schema, got %#v", questions)
	}
	if questions.Items.Type != "object" {
		t.Fatalf("expected question items to be objects, got %q", questions.Items.Type)
	}
	if _, ok := questions.Items.Properties["question"]; !ok {
		t.Fatal("expected question item to document a question field")
	}
	if _, ok := questions.Items.Properties["options"]; !ok {
		t.Fatal("expected question item to document an options field")
	}
	if _, ok := questions.Items.Properties["multiSelect"]; !ok {
		t.Fatal("expected question item to document a multiSelect field")
	}
	requiredQuestion := false
	for _, name := range questions.Items.Required {
		if name == "question" {
			requiredQuestion = true
		}
	}
	if !requiredQuestion {
		t.Fatalf("expected question field to be required, got %v", questions.Items.Required)
	}
	requiredQuestions := false
	for _, name := range schema.Required {
		if name == "questions" {
			requiredQuestions = true
		}
	}
	if !requiredQuestions {
		t.Fatalf("expected questions to be required, got %v", schema.Required)
	}
}

func TestAskUserToolRunFallsBackWhenNonInteractive(t *testing.T) {
	// The tool's own Run() is only reached when nothing intercepted the call,
	// i.e. there is no interactive user. It must degrade gracefully, never block.
	result := NewAskUserTool().Run(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{"question": "Which framework?"},
		},
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok status from graceful fallback, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(strings.ToLower(result.Output), "no interactive user") {
		t.Fatalf("expected no-interactive-user message, got %q", result.Output)
	}
	if !strings.Contains(strings.ToLower(result.Output), "assumption") {
		t.Fatalf("expected guidance to proceed with assumptions, got %q", result.Output)
	}
}

func TestAskUserToolRunRejectsMissingQuestions(t *testing.T) {
	result := NewAskUserTool().Run(context.Background(), map[string]any{
		"questions": []any{},
	})
	if result.Status != StatusError {
		t.Fatalf("expected error status when no questions provided, got %s", result.Status)
	}
	if !strings.Contains(strings.ToLower(result.Output), "at least one question") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestAskUserToolIsRegisteredInReadOnlyCore(t *testing.T) {
	found := false
	for _, tool := range CoreReadOnlyTools(t.TempDir()) {
		if tool.Name() == "ask_user" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected ask_user to be part of the core read-only tool set")
	}
}

func TestParseAskUserQuestionsLenientOptions(t *testing.T) {
	// minimax-style: options as array of objects with a label field.
	qs, err := ParseAskUserQuestions(map[string]any{
		"questions": []any{
			map[string]any{"question": "Pick a style", "options": []any{
				map[string]any{"label": "Modern"},
				map[string]any{"value": "Classic"},
				"Minimal",
			}},
		},
	})
	if err != nil {
		t.Fatalf("objects/strings options must not error: %v", err)
	}
	if got := qs[0].Options; len(got) != 3 || got[0] != "Modern" || got[1] != "Classic" || got[2] != "Minimal" {
		t.Fatalf("coerced options = %v, want [Modern Classic Minimal]", got)
	}

	// options as a single newline-delimited string.
	qs, err = ParseAskUserQuestions(map[string]any{
		"questions": []any{map[string]any{"question": "q", "options": "A\nB"}},
	})
	if err != nil {
		t.Fatalf("string options must not error: %v", err)
	}
	if got := qs[0].Options; len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("string-split options = %v, want [A B]", got)
	}

	// no options at all = valid free-text question.
	if _, err := ParseAskUserQuestions(map[string]any{
		"questions": []any{map[string]any{"question": "free text?"}},
	}); err != nil {
		t.Fatalf("missing options must be allowed: %v", err)
	}
}

func TestParseAskUserQuestionsStringItem(t *testing.T) {
	// minimax-style: a question item that is a bare string, not an object.
	qs, err := ParseAskUserQuestions(map[string]any{"questions": []any{"What is your name?"}})
	if err != nil {
		t.Fatalf("string question item must not error: %v", err)
	}
	if len(qs) != 1 || qs[0].Question != "What is your name?" {
		t.Fatalf("bare-string question = %+v", qs)
	}
}
