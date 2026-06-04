package modelregistry

import (
	"strings"
	"testing"
)

func TestModelEntryValidatesPRDShape(t *testing.T) {
	model := ModelEntry{
		ID:          "gpt-4.1-mini",
		DisplayName: "GPT-4.1 mini",
		APIModel:    "gpt-4.1-mini",
		Provider:    ProviderOpenAI,
		APIProviders: []ProviderKind{
			ProviderOpenAI,
			ProviderOpenAICompatible,
		},
		ContextLimits: ContextLimits{
			ContextWindow:   1_047_576,
			MaxOutputTokens: 32_768,
		},
		Capabilities: ModelCapabilities{
			ModelCapabilityChat,
			ModelCapabilityStreaming,
			ModelCapabilityToolCalling,
			ModelCapabilitySystemPrompt,
			ModelCapabilityLongContext,
		},
		Cost: ModelCost{
			Currency:              "USD",
			Unit:                  "per_1m_tokens",
			InputPerMillion:       0.4,
			OutputPerMillion:      1.6,
			CachedInputPerMillion: 0.1,
			Source:                "https://platform.openai.com/docs/pricing/",
			SourceLastVerified:    "2026-06-02",
		},
		Status:      ModelStatusActive,
		Aliases:     []string{"openai:gpt-4.1-mini"},
		Description: "OpenAI lower-cost long-context model for frequent edit loops.",
	}

	if err := model.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if !model.Supports(ModelCapabilityToolCalling) {
		t.Fatal("model should support tool calling")
	}
	if !model.AllowsProvider(ProviderOpenAICompatible) {
		t.Fatal("model should allow OpenAI-compatible runtime adapter")
	}
}

func TestModelEntryRejectsInvalidContextLimits(t *testing.T) {
	model := validModelEntry()
	model.ContextLimits.ContextWindow = 1_000
	model.ContextLimits.MaxOutputTokens = 2_000

	err := model.Validate()
	if err == nil {
		t.Fatal("expected context limit validation error")
	}
	if !strings.Contains(err.Error(), "max output tokens") {
		t.Fatalf("error = %q, want max output tokens", err.Error())
	}
}

func TestModelEntryRejectsOpenAICompatibleAsPrimaryProvider(t *testing.T) {
	model := validModelEntry()
	model.Provider = ProviderOpenAICompatible

	err := model.Validate()
	if err == nil {
		t.Fatal("expected primary provider validation error")
	}
	if !strings.Contains(err.Error(), "primary provider") {
		t.Fatalf("error = %q, want primary provider", err.Error())
	}
}

func TestModelEntryRejectsUnknownContractEnums(t *testing.T) {
	model := validModelEntry()
	model.Capabilities = ModelCapabilities{"telepathy"}

	err := model.Validate()
	if err == nil {
		t.Fatal("expected capability validation error")
	}
	if !strings.Contains(err.Error(), "model capability") {
		t.Fatalf("error = %q, want model capability", err.Error())
	}

	model = validModelEntry()
	model.ReasoningEfforts = []ReasoningEffort{"warp"}

	err = model.Validate()
	if err == nil {
		t.Fatal("expected reasoning effort validation error")
	}
	if !strings.Contains(err.Error(), "reasoning effort") {
		t.Fatalf("error = %q, want reasoning effort", err.Error())
	}

	model = validModelEntry()
	model.Status = "retired"

	err = model.Validate()
	if err == nil {
		t.Fatal("expected model status validation error")
	}
	if !strings.Contains(err.Error(), "model status") {
		t.Fatalf("error = %q, want model status", err.Error())
	}
}

func TestModelRegistryResolvesStablePatterns(t *testing.T) {
	registry := NewRegistry([]ModelEntry{validModelEntry()})

	model, ok := registry.Get("OPENAI:GPT-4.1-MINI")
	if !ok {
		t.Fatal("expected model to resolve from case-insensitive match pattern")
	}
	if model.ID != "gpt-4.1-mini" {
		t.Fatalf("model ID = %q, want gpt-4.1-mini", model.ID)
	}
}

func validModelEntry() ModelEntry {
	return ModelEntry{
		ID:          "gpt-4.1-mini",
		DisplayName: "GPT-4.1 mini",
		APIModel:    "gpt-4.1-mini",
		Provider:    ProviderOpenAI,
		ContextLimits: ContextLimits{
			ContextWindow:   1_047_576,
			MaxOutputTokens: 32_768,
		},
		Capabilities: ModelCapabilities{ModelCapabilityChat},
		Cost: ModelCost{
			Currency:           "USD",
			Unit:               "per_1m_tokens",
			InputPerMillion:    0.4,
			OutputPerMillion:   1.6,
			Source:             "https://platform.openai.com/docs/pricing/",
			SourceLastVerified: "2026-06-02",
		},
		Status:  ModelStatusActive,
		Aliases: []string{"openai:gpt-4.1-mini"},
	}
}
