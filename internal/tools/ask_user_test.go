package tools

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestParseAskUserRecommended(t *testing.T) {
	// recommended is kept only when it resolves to one of the options (canonical
	// option text), case-insensitively; otherwise it is dropped so Recommended stays
	// either empty or a member of Options.
	cases := []struct {
		name        string
		options     []any
		recommended any
		wantOptions []string
		wantRec     string
	}{
		{"exact match", []any{"Postgres", "SQLite"}, "SQLite", []string{"Postgres", "SQLite"}, "SQLite"},
		{"case-insensitive maps to canonical", []any{"Postgres", "SQLite"}, "sqlite", []string{"Postgres", "SQLite"}, "SQLite"},
		{"non-member dropped", []any{"Postgres", "SQLite"}, "Mongo", []string{"Postgres", "SQLite"}, ""},
		{"no options drops recommended", nil, "SQLite", nil, ""},
		{"non-string recommended ignored", []any{"A", "B"}, 3, []string{"A", "B"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			question := map[string]any{"question": "Which one?"}
			if c.options != nil {
				question["options"] = c.options
			}
			if c.recommended != nil {
				question["recommended"] = c.recommended
			}
			parsed, err := ParseAskUserQuestions(map[string]any{"questions": []any{question}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(parsed) != 1 {
				t.Fatalf("expected one question, got %d", len(parsed))
			}
			if !slices.Equal(parsed[0].Options, c.wantOptions) {
				t.Fatalf("options = %#v, want %#v", parsed[0].Options, c.wantOptions)
			}
			if parsed[0].Recommended != c.wantRec {
				t.Fatalf("recommended = %q, want %q", parsed[0].Recommended, c.wantRec)
			}
		})
	}
}

func TestAskUserToolAdvertisesRecommended(t *testing.T) {
	schema := NewAskUserTool().Parameters()
	if _, ok := schema.Properties["questions"].Items.Properties["recommended"]; !ok {
		t.Fatal("expected question item to document a recommended field")
	}
}

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

func TestAskUserToolAdvertisesNonEmptyQuestionSchema(t *testing.T) {
	data, err := json.Marshal(NewAskUserTool().Parameters())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	schema := string(data)
	for _, want := range []string{`"minItems":1`, `"minLength":1`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("ask_user schema missing %s: %s", want, schema)
		}
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

func TestCoreReadOnlyToolsIncludeAskUser(t *testing.T) {
	found := false
	for _, tool := range CoreReadOnlyToolsScoped(t.TempDir(), nil) {
		if tool.Name() == "ask_user" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected ask_user in core read-only tools")
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

	// multiSelect is a UI hint: an uncoercible value must NOT fail the call; it
	// defaults to false (best-effort, like options).
	qs, err = ParseAskUserQuestions(map[string]any{
		"questions": []any{map[string]any{"question": "q", "multiSelect": "maybe"}},
	})
	if err != nil {
		t.Fatalf("uncoercible multiSelect must not error: %v", err)
	}
	if qs[0].MultiSelect {
		t.Fatalf("uncoercible multiSelect should default to false")
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
