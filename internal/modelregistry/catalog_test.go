package modelregistry

import (
	"slices"
	"strings"
	"testing"
)

func TestDefaultRegistryCoversM1ModelCatalog(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	models := registry.List(ListOptions{})
	if len(models) < 10 {
		t.Fatalf("default registry has %d active/preview models, want at least 10", len(models))
	}
	for _, model := range models {
		if model.Status == ModelStatusDeprecated {
			t.Fatalf("List without include deprecated returned deprecated model %q", model.ID)
		}
		if model.ID == "" || model.DisplayName == "" || model.APIModel == "" {
			t.Fatalf("model should expose complete identity metadata: %#v", model)
		}
		if model.ContextLimits.ContextWindow <= 0 || model.ContextLimits.MaxOutputTokens <= 0 {
			t.Fatalf("model should expose context limits: %#v", model)
		}
		if !model.Supports(ModelCapabilityChat) || !model.Supports(ModelCapabilityStreaming) {
			t.Fatalf("model %q should expose chat and streaming capabilities", model.ID)
		}
		if model.Cost.Currency != "USD" || model.Cost.Unit != "per_1m_tokens" {
			t.Fatalf("model %q should expose USD token pricing: %#v", model.ID, model.Cost)
		}
		if !strings.HasPrefix(model.Cost.Source, "https://") {
			t.Fatalf("model %q should expose source URL: %#v", model.ID, model.Cost)
		}
	}

	providers := map[ProviderKind]bool{}
	for _, model := range models {
		providers[model.Provider] = true
	}
	for _, provider := range []ProviderKind{ProviderOpenAI, ProviderAnthropic, ProviderGoogle} {
		if !providers[provider] {
			t.Fatalf("default registry missing provider %q", provider)
		}
	}

	if _, err := registry.Require("gpt-4-turbo"); err != nil {
		t.Fatalf("deprecated models should remain resolvable for migration/history: %v", err)
	}
	if !containsModelID(registry.List(ListOptions{IncludeDeprecated: true}), "gpt-4-turbo") {
		t.Fatal("include deprecated list should retain migration metadata")
	}
	if DefaultModelID != "gpt-4.1" {
		t.Fatalf("DefaultModelID = %q, want gpt-4.1", DefaultModelID)
	}

	haiku, err := registry.Require("claude-haiku-4.5")
	if err != nil {
		t.Fatalf("claude-haiku-4.5 should be resolvable: %v", err)
	}
	if haiku.APIModel != "claude-haiku-4-5-20251001" {
		t.Fatalf("claude-haiku-4.5 API model = %q, want claude-haiku-4-5-20251001", haiku.APIModel)
	}
	if haiku.ContextLimits.ContextWindow != 200_000 || haiku.ContextLimits.MaxOutputTokens != 64_000 {
		t.Fatalf("claude-haiku-4.5 limits = %#v, want 200k context and 64k max output", haiku.ContextLimits)
	}
}

func TestDefaultRegistryResolvesAliasesAndStableFilters(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	if id, ok := registry.ResolveID(" OPENAI:GPT-4.1 "); !ok || id != "gpt-4.1" {
		t.Fatalf("ResolveID(openai alias) = %q/%v, want gpt-4.1/true", id, ok)
	}
	if id, ok := registry.ResolveID("sonnet-4.5"); !ok || id != "claude-sonnet-4.5" {
		t.Fatalf("ResolveID(sonnet alias) = %q/%v, want claude-sonnet-4.5/true", id, ok)
	}
	if id, ok := registry.ResolveID("gemini-flash"); !ok || id != "gemini-2.5-flash" {
		t.Fatalf("ResolveID(gemini alias) = %q/%v, want gemini-2.5-flash/true", id, ok)
	}
	if _, ok := registry.ResolveID("gpt-latest"); ok {
		t.Fatal("registry should not expose speculative latest aliases")
	}

	for _, model := range registry.ListByProvider(ProviderOpenAI) {
		if model.Provider != ProviderOpenAI {
			t.Fatalf("ListByProvider(openai) returned %q", model.Provider)
		}
	}

	visionModels := registry.ListByCapability(ModelCapabilityVision)
	if len(visionModels) == 0 {
		t.Fatal("expected at least one vision model")
	}
	for _, model := range visionModels {
		if !model.Supports(ModelCapabilityVision) {
			t.Fatalf("ListByCapability(vision) returned model without vision: %#v", model)
		}
	}
	if !registry.SupportsCapability("gemini-2.5-pro", ModelCapabilityReasoning) {
		t.Fatal("gemini-2.5-pro should expose reasoning capability")
	}
}

func TestDefaultRegistryReasoningAndProviderAssertions(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	efforts := registry.ReasoningEfforts("gemini-2.5-pro")
	wantEfforts := []ReasoningEffort{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}
	if len(efforts) != len(wantEfforts) {
		t.Fatalf("ReasoningEfforts length = %d, want %d: %#v", len(efforts), len(wantEfforts), efforts)
	}
	for index, effort := range wantEfforts {
		if efforts[index] != effort {
			t.Fatalf("ReasoningEfforts[%d] = %q, want %q", index, efforts[index], effort)
		}
	}
	if efforts := registry.ReasoningEfforts("gpt-4o"); len(efforts) != 0 {
		t.Fatalf("gpt-4o reasoning efforts = %#v, want empty", efforts)
	}

	model, err := registry.RequireProvider("gpt-4.1", ProviderOpenAI)
	if err != nil {
		t.Fatalf("RequireProvider(openai) returned error: %v", err)
	}
	if model.ID != "gpt-4.1" {
		t.Fatalf("RequireProvider returned %q, want gpt-4.1", model.ID)
	}
	if _, err := registry.RequireProvider("gpt-4.1", ProviderAnthropic); err == nil {
		t.Fatal("RequireProvider should reject provider mismatches")
	} else if !strings.Contains(err.Error(), "belongs to openai") {
		t.Fatalf("provider mismatch error = %q, want belongs to openai", err.Error())
	}
	if _, err := registry.Require("unknown-model"); err == nil {
		t.Fatal("Require should reject unknown models")
	} else if !strings.Contains(err.Error(), "unknown Zero model") {
		t.Fatalf("unknown model error = %q, want unknown Zero model", err.Error())
	}
}

func TestDefaultRegistryResolvesFuzzyMatchPatterns(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	cases := map[string]string{
		"sonnet 4.5": "claude-sonnet-4.5",
		"Sonnet 4.5": "claude-sonnet-4.5",
		"opus 4.1":   "claude-opus-4.1",
		"gemini pro": "gemini-2.5-pro",
	}
	for input, want := range cases {
		model, ok := registry.Resolve(input)
		if !ok || model.ID != want {
			t.Fatalf("Resolve(%q) = %q/%v, want %q", input, model.ID, ok, want)
		}
	}
}

func TestDefaultRegistryDeprecatedModelsRedirect(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	cases := map[string]string{
		"gpt-4-turbo":      "gpt-4.1",
		"claude-haiku-3.5": "claude-haiku-4.5",
	}
	for input, want := range cases {
		model, notice, ok := registry.ResolveWithFallback(input)
		if !ok {
			t.Fatalf("ResolveWithFallback(%q) failed", input)
		}
		if model.ID != want {
			t.Fatalf("ResolveWithFallback(%q) = %q, want fallback %q", input, model.ID, want)
		}
		if notice == "" {
			t.Fatalf("ResolveWithFallback(%q) should return a deprecation notice", input)
		}
	}
}

func TestDefaultRegistryReasoningModelsHaveDefaultEffort(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}
	for _, id := range []string{"claude-sonnet-4.5", "claude-opus-4.1", "gemini-2.5-pro"} {
		model, err := registry.Require(id)
		if err != nil {
			t.Fatalf("Require(%q) returned error: %v", id, err)
		}
		if model.DefaultReasoningEffort == "" {
			t.Fatalf("reasoning model %q should declare a default reasoning effort", id)
		}
		if !reasoningEffortAllowedIn(model.ReasoningEfforts, model.DefaultReasoningEffort) {
			t.Fatalf("model %q default effort %q is not among supported efforts %v", id, model.DefaultReasoningEffort, model.ReasoningEfforts)
		}
	}
}

func reasoningEffortAllowedIn(efforts []ReasoningEffort, want ReasoningEffort) bool {
	return slices.Contains(efforts, want)
}

func TestDefaultRegistryUpgradeTargets(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	// Seeded escalation chains across the concrete catalog ids.
	chains := map[string]string{
		"claude-haiku-4.5":      "claude-sonnet-4.5",
		"claude-sonnet-4.5":     "claude-opus-4.1",
		"gpt-4.1-mini":          "gpt-4.1",
		"gpt-4.1-nano":          "gpt-4.1-mini",
		"gpt-4o-mini":           "gpt-4o",
		"gemini-2.5-flash-lite": "gemini-2.5-flash",
		"gemini-2.5-flash":      "gemini-2.5-pro",
	}
	for source, want := range chains {
		target, ok := registry.UpgradeTarget(source)
		if !ok {
			t.Fatalf("UpgradeTarget(%q) returned no target, want %q", source, want)
		}
		if target.ID != want {
			t.Fatalf("UpgradeTarget(%q) = %q, want %q", source, target.ID, want)
		}
	}

	// Top-tier models declare no escalation target.
	for _, topTier := range []string{"claude-opus-4.1", "gpt-4.1", "gemini-2.5-pro"} {
		if target, ok := registry.UpgradeTarget(topTier); ok {
			t.Fatalf("top-tier model %q should have no upgrade target, got %q", topTier, target.ID)
		}
	}

	// The seeded field is present on the decorated entry, not only via the method.
	haiku, err := registry.Require("claude-haiku-4.5")
	if err != nil {
		t.Fatalf("Require(claude-haiku-4.5) returned error: %v", err)
	}
	if haiku.UpgradeTargetID != "claude-sonnet-4.5" {
		t.Fatalf("claude-haiku-4.5 UpgradeTargetID = %q, want claude-sonnet-4.5", haiku.UpgradeTargetID)
	}
}

func containsModelID(models []ModelEntry, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}
