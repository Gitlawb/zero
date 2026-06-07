package agent

import (
	"strings"
	"testing"
)

func TestModelFamilyClassification(t *testing.T) {
	cases := map[string]string{
		"gpt-5":                  familyOpenAI,
		"gpt-4o":                 familyOpenAI,
		"o3-mini":                familyOpenAI,
		"gemini-2.5-pro":         familyGemini,
		"claude-opus-4-6":        familyAnthropic,
		"anthropic/claude-haiku": familyAnthropic,
		"some-unknown-model":     "",
		"":                       "",
	}
	for model, want := range cases {
		if got := modelFamily(model); got != want {
			t.Errorf("modelFamily(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestBuildSystemPromptAppendsModelAddendum(t *testing.T) {
	// Assert on the addendum constants themselves (the core prompt shares phrases
	// like "one tool call per file", so substring checks can't distinguish them).
	if got := buildSystemPrompt(Options{Model: "gpt-5"}); !strings.Contains(got, openAIPromptAddendum) {
		t.Fatalf("expected the OpenAI addendum in the gpt-5 prompt")
	}
	claude := buildSystemPrompt(Options{Model: "claude-opus-4-6"})
	if !strings.Contains(claude, anthropicPromptAddendum) {
		t.Fatalf("expected the Anthropic addendum in the claude prompt")
	}
	if strings.Contains(claude, openAIPromptAddendum) {
		t.Fatalf("the claude prompt must not contain the OpenAI addendum")
	}
	// Unknown / unset model gets no family block.
	if got := modelPromptAddendum(""); got != "" {
		t.Fatalf("expected no addendum without a model, got %q", got)
	}
	if strings.Contains(buildSystemPrompt(Options{}), "<model_guidance>") {
		t.Fatalf("expected no <model_guidance> block without a model")
	}
}
