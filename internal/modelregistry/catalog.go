package modelregistry

import "fmt"

const DefaultModelID = "gpt-4.1"

const sourceLastVerified = "2026-06-04"

const (
	openAIPricingSource    = "https://platform.openai.com/docs/pricing/"
	anthropicPricingSource = "https://platform.claude.com/docs/en/about-claude/pricing"
	googlePricingSource    = "https://ai.google.dev/gemini-api/docs/pricing"
)

type ListOptions struct {
	IncludeDeprecated bool
	Provider          ProviderKind
	Capability        ModelCapability
}

func DefaultRegistry() (Registry, error) {
	return NewRegistry(DefaultModelEntries())
}

func DefaultModelEntries() []ModelEntry {
	entries := []ModelEntry{
		openAIModel("gpt-4.1", "GPT-4.1", "gpt-4.1", ModelStatusActive, []string{"openai:gpt-4.1"}, ContextLimits{ContextWindow: 1_047_576, MaxOutputTokens: 16_384}, ModelCost{InputPerMillion: 2, CachedInputPerMillion: 0.5, OutputPerMillion: 8}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode, ModelCapabilityLongContext}, "OpenAI stable long-context model for general coding sessions."),
		openAIModel("gpt-4.1-mini", "GPT-4.1 mini", "gpt-4.1-mini", ModelStatusActive, []string{"openai:gpt-4.1-mini"}, ContextLimits{ContextWindow: 1_047_576, MaxOutputTokens: 32_768}, ModelCost{InputPerMillion: 0.4, CachedInputPerMillion: 0.1, OutputPerMillion: 1.6}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode, ModelCapabilityLongContext}, "OpenAI lower-cost long-context model for frequent edit loops."),
		openAIModel("gpt-4.1-nano", "GPT-4.1 nano", "gpt-4.1-nano", ModelStatusActive, []string{"openai:gpt-4.1-nano"}, ContextLimits{ContextWindow: 1_047_576, MaxOutputTokens: 32_768}, ModelCost{InputPerMillion: 0.1, CachedInputPerMillion: 0.025, OutputPerMillion: 0.4}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode, ModelCapabilityLongContext}, "OpenAI smallest GPT-4.1 model for routing, summaries, and light checks."),
		openAIModel("gpt-4o", "GPT-4o", "gpt-4o", ModelStatusActive, []string{"openai:gpt-4o"}, ContextLimits{ContextWindow: 128_000, MaxOutputTokens: 16_384}, ModelCost{InputPerMillion: 2.5, CachedInputPerMillion: 1.25, OutputPerMillion: 10}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode}, "OpenAI multimodal model kept for compatibility with existing configs."),
		openAIModel("gpt-4o-mini", "GPT-4o mini", "gpt-4o-mini", ModelStatusActive, []string{"openai:gpt-4o-mini"}, ContextLimits{ContextWindow: 128_000, MaxOutputTokens: 16_384}, ModelCost{InputPerMillion: 0.15, CachedInputPerMillion: 0.075, OutputPerMillion: 0.6}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode}, "OpenAI low-cost multimodal model for lightweight sessions."),
		openAIModel("gpt-4-turbo", "GPT-4 Turbo", "gpt-4-turbo", ModelStatusDeprecated, []string{"openai:gpt-4-turbo"}, ContextLimits{ContextWindow: 128_000, MaxOutputTokens: 4_096}, ModelCost{InputPerMillion: 10, OutputPerMillion: 30}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode}, "Deprecated OpenAI model retained for config migration and historical usage display."),
		anthropicModel("claude-opus-4.1", "Claude Opus 4.1", "claude-opus-4-1-20250805", ModelStatusActive, []string{"anthropic:claude-opus-4.1", "opus-4.1"}, ContextLimits{ContextWindow: 200_000, MaxOutputTokens: 32_000}, ModelCost{InputPerMillion: 15, CachedInputPerMillion: 1.5, OutputPerMillion: 75}, []ModelCapability{ModelCapabilityVision, ModelCapabilityReasoning, ModelCapabilityPromptCache}, standardReasoningEfforts(), "Anthropic high-capability Opus model for deep coding and planning."),
		anthropicModel("claude-sonnet-4.5", "Claude Sonnet 4.5", "claude-sonnet-4-5-20250929", ModelStatusActive, []string{"anthropic:claude-sonnet-4.5", "sonnet-4.5"}, ContextLimits{ContextWindow: 200_000, MaxOutputTokens: 64_000}, ModelCost{InputPerMillion: 3, CachedInputPerMillion: 0.3, OutputPerMillion: 15}, []ModelCapability{ModelCapabilityVision, ModelCapabilityReasoning, ModelCapabilityPromptCache}, standardReasoningEfforts(), "Anthropic balanced coding model for high-quality daily agent work."),
		anthropicModel("claude-haiku-4.5", "Claude Haiku 4.5", "claude-haiku-4-5-20251001", ModelStatusActive, []string{"anthropic:claude-haiku-4.5", "haiku-4.5"}, ContextLimits{ContextWindow: 200_000, MaxOutputTokens: 64_000}, ModelCost{InputPerMillion: 1, CachedInputPerMillion: 0.1, OutputPerMillion: 5}, []ModelCapability{ModelCapabilityVision, ModelCapabilityReasoning, ModelCapabilityPromptCache}, standardReasoningEfforts(), "Anthropic fast model for lightweight coding support and summaries."),
		anthropicModel("claude-haiku-3.5", "Claude Haiku 3.5", "claude-3-5-haiku-20241022", ModelStatusDeprecated, []string{"anthropic:claude-haiku-3.5", "haiku-3.5"}, ContextLimits{ContextWindow: 200_000, MaxOutputTokens: 8_192}, ModelCost{InputPerMillion: 0.8, CachedInputPerMillion: 0.08, OutputPerMillion: 4}, []ModelCapability{ModelCapabilityVision, ModelCapabilityPromptCache}, nil, "Retired Anthropic Haiku model retained for migration and historical usage display."),
		googleModel("gemini-2.5-pro", "Gemini 2.5 Pro", "gemini-2.5-pro", ModelStatusActive, []string{"google:gemini-2.5-pro", "gemini-pro"}, ContextLimits{ContextWindow: 1_048_576, MaxOutputTokens: 65_536}, ModelCost{Tiers: []ModelCostTier{{UpToInputTokens: 200_000, InputPerMillion: 1.25, CachedInputPerMillion: 0.125, OutputPerMillion: 10, Note: "Prompts up to 200k tokens."}, {InputPerMillion: 2.5, CachedInputPerMillion: 0.25, OutputPerMillion: 15, Note: "Prompts above 200k tokens."}}}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode, ModelCapabilityReasoning, ModelCapabilityLongContext}, standardReasoningEfforts(), "Google general-purpose Pro model with tiered long-context pricing."),
		googleModel("gemini-2.5-flash", "Gemini 2.5 Flash", "gemini-2.5-flash", ModelStatusActive, []string{"google:gemini-2.5-flash", "gemini-flash"}, ContextLimits{ContextWindow: 1_048_576, MaxOutputTokens: 65_536}, ModelCost{InputPerMillion: 0.3, CachedInputPerMillion: 0.03, OutputPerMillion: 2.5}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode, ModelCapabilityReasoning, ModelCapabilityLongContext}, standardReasoningEfforts(), "Google Flash model for low-latency coding interactions."),
		googleModel("gemini-2.5-flash-lite", "Gemini 2.5 Flash-Lite", "gemini-2.5-flash-lite", ModelStatusActive, []string{"google:gemini-2.5-flash-lite", "gemini-flash-lite"}, ContextLimits{ContextWindow: 1_048_576, MaxOutputTokens: 65_536}, ModelCost{InputPerMillion: 0.1, CachedInputPerMillion: 0.01, OutputPerMillion: 0.4}, []ModelCapability{ModelCapabilityVision, ModelCapabilityJSONMode, ModelCapabilityReasoning, ModelCapabilityLongContext}, standardReasoningEfforts(), "Google low-cost Flash model for background routing and summaries."),
	}
	decorateModelDepth(entries)
	return cloneModelEntries(entries)
}

// decorateModelDepth layers slice-3 registry-depth metadata (fuzzy match
// patterns, default reasoning efforts, and deprecation fallbacks) onto the base
// catalog entries. Kept separate from the constructor helpers so the core
// catalog stays terse and the depth wiring is easy to audit.
func decorateModelDepth(entries []ModelEntry) {
	depth := map[string]struct {
		defaultEffort ReasoningEffort
		patterns      []string
		deprecation   *DeprecationRule
	}{
		"claude-sonnet-4.5": {
			defaultEffort: ReasoningEffortMedium,
			patterns:      []string{`(?i)^sonnet[^a-z0-9]*4[.\s]?5$`},
		},
		"claude-opus-4.1": {
			defaultEffort: ReasoningEffortHigh,
			patterns:      []string{`(?i)^opus[^a-z0-9]*4[.\s]?1$`},
		},
		"claude-haiku-4.5": {
			defaultEffort: ReasoningEffortLow,
			patterns:      []string{`(?i)^haiku[^a-z0-9]*4[.\s]?5$`},
		},
		"gemini-2.5-pro": {
			defaultEffort: ReasoningEffortMedium,
			patterns:      []string{`(?i)^gemini[^a-z0-9]*pro$`},
		},
		"gemini-2.5-flash": {
			defaultEffort: ReasoningEffortLow,
		},
		"gpt-4-turbo": {
			deprecation: &DeprecationRule{
				FallbackID: "gpt-4.1",
				SoftDate:   "2025-04-14",
				WarningMsg: "gpt-4-turbo is deprecated; using gpt-4.1 instead",
			},
		},
		"claude-haiku-3.5": {
			deprecation: &DeprecationRule{
				FallbackID: "claude-haiku-4.5",
				SoftDate:   "2025-10-01",
				WarningMsg: "claude-haiku-3.5 is deprecated; using claude-haiku-4.5 instead",
			},
		},
	}
	for index := range entries {
		extra, ok := depth[entries[index].ID]
		if !ok {
			continue
		}
		if extra.defaultEffort != "" {
			entries[index].DefaultReasoningEffort = extra.defaultEffort
		}
		if len(extra.patterns) > 0 {
			entries[index].MatchPatterns = append(entries[index].MatchPatterns, extra.patterns...)
		}
		if extra.deprecation != nil {
			entries[index].Deprecation = extra.deprecation.Clone()
		}
	}
}

func (registry Registry) List(options ListOptions) []ModelEntry {
	models := make([]ModelEntry, 0, len(registry.models))
	for _, model := range registry.models {
		if !options.IncludeDeprecated && model.Status == ModelStatusDeprecated {
			continue
		}
		if options.Provider != "" && model.Provider != options.Provider {
			continue
		}
		if options.Capability != "" && !model.Supports(options.Capability) {
			continue
		}
		models = append(models, cloneModelEntry(model))
	}
	return models
}

func (registry Registry) ListByProvider(provider ProviderKind) []ModelEntry {
	return registry.List(ListOptions{Provider: provider})
}

func (registry Registry) ListByCapability(capability ModelCapability) []ModelEntry {
	return registry.List(ListOptions{Capability: capability})
}

func (registry Registry) ResolveID(pattern string) (string, bool) {
	model, ok := registry.Get(pattern)
	if !ok {
		return "", false
	}
	return model.ID, true
}

func (registry Registry) Require(pattern string) (ModelEntry, error) {
	model, ok := registry.Get(pattern)
	if !ok {
		return ModelEntry{}, fmt.Errorf("unknown Zero model %q", pattern)
	}
	return model, nil
}

func (registry Registry) SupportsCapability(pattern string, capability ModelCapability) bool {
	model, ok := registry.Get(pattern)
	return ok && model.Supports(capability)
}

func (registry Registry) ReasoningEfforts(pattern string) []ReasoningEffort {
	model, ok := registry.Get(pattern)
	if !ok {
		return nil
	}
	return append([]ReasoningEffort{}, model.ReasoningEfforts...)
}

func (registry Registry) RequireProvider(pattern string, provider ProviderKind) (ModelEntry, error) {
	model, err := registry.Require(pattern)
	if err != nil {
		return ModelEntry{}, err
	}
	if model.Provider != provider {
		return ModelEntry{}, fmt.Errorf("zero model %s belongs to %s, not %s", model.ID, model.Provider, provider)
	}
	return model, nil
}

func openAIModel(id string, displayName string, apiModel string, status ModelStatus, aliases []string, limits ContextLimits, cost ModelCost, extraCapabilities []ModelCapability, description string) ModelEntry {
	cost.Currency = "USD"
	cost.Unit = "per_1m_tokens"
	cost.Source = openAIPricingSource
	cost.SourceLastVerified = sourceLastVerified
	return ModelEntry{
		ID:            id,
		DisplayName:   displayName,
		APIModel:      apiModel,
		Provider:      ProviderOpenAI,
		APIProviders:  []ProviderKind{ProviderOpenAI, ProviderOpenAICompatible},
		ContextLimits: limits,
		Capabilities:  withBaseCapabilities(extraCapabilities...),
		Cost:          cost,
		Status:        status,
		Aliases:       aliases,
		Description:   description,
	}
}

func anthropicModel(id string, displayName string, apiModel string, status ModelStatus, aliases []string, limits ContextLimits, cost ModelCost, extraCapabilities []ModelCapability, efforts []ReasoningEffort, description string) ModelEntry {
	cost.Currency = "USD"
	cost.Unit = "per_1m_tokens"
	cost.Source = anthropicPricingSource
	cost.SourceLastVerified = sourceLastVerified
	cost.Notes = append(cost.Notes, "Claude cached input pricing models cache hits and refreshes.")
	return ModelEntry{
		ID:               id,
		DisplayName:      displayName,
		APIModel:         apiModel,
		Provider:         ProviderAnthropic,
		ContextLimits:    limits,
		ReasoningEfforts: efforts,
		Capabilities:     withBaseCapabilities(extraCapabilities...),
		Cost:             cost,
		Status:           status,
		Aliases:          aliases,
		Description:      description,
	}
}

func googleModel(id string, displayName string, apiModel string, status ModelStatus, aliases []string, limits ContextLimits, cost ModelCost, extraCapabilities []ModelCapability, efforts []ReasoningEffort, description string) ModelEntry {
	cost.Currency = "USD"
	cost.Unit = "per_1m_tokens"
	cost.Source = googlePricingSource
	cost.SourceLastVerified = sourceLastVerified
	return ModelEntry{
		ID:               id,
		DisplayName:      displayName,
		APIModel:         apiModel,
		Provider:         ProviderGoogle,
		ContextLimits:    limits,
		ReasoningEfforts: efforts,
		Capabilities:     withBaseCapabilities(extraCapabilities...),
		Cost:             cost,
		Status:           status,
		Aliases:          aliases,
		Description:      description,
	}
}

func withBaseCapabilities(extra ...ModelCapability) []ModelCapability {
	capabilities := []ModelCapability{
		ModelCapabilityChat,
		ModelCapabilityStreaming,
		ModelCapabilityToolCalling,
		ModelCapabilitySystemPrompt,
	}
	return append(capabilities, extra...)
}

func standardReasoningEfforts() []ReasoningEffort {
	return []ReasoningEffort{
		ReasoningEffortLow,
		ReasoningEffortMedium,
		ReasoningEffortHigh,
	}
}

func cloneModelEntries(entries []ModelEntry) []ModelEntry {
	cloned := make([]ModelEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = cloneModelEntry(entry)
	}
	return cloned
}

func cloneModelEntry(entry ModelEntry) ModelEntry {
	entry.APIProviders = append([]ProviderKind{}, entry.APIProviders...)
	entry.ReasoningEfforts = append([]ReasoningEffort{}, entry.ReasoningEfforts...)
	entry.Capabilities = append([]ModelCapability{}, entry.Capabilities...)
	entry.Aliases = append([]string{}, entry.Aliases...)
	entry.MatchPatterns = append([]string{}, entry.MatchPatterns...)
	entry.Deprecation = entry.Deprecation.Clone()
	entry.Cost.Tiers = append([]ModelCostTier{}, entry.Cost.Tiers...)
	entry.Cost.Notes = append([]string{}, entry.Cost.Notes...)
	return entry
}
