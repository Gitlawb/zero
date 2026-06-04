package modelregistry

import (
	"fmt"
	"strings"
	"time"
)

type ProviderKind string

const (
	ProviderOpenAI           ProviderKind = "openai"
	ProviderAnthropic        ProviderKind = "anthropic"
	ProviderGoogle           ProviderKind = "google"
	ProviderOpenAICompatible ProviderKind = "openai-compatible"
)

type ReasoningEffort string

const (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
)

type ModelCapability string

const (
	ModelCapabilityChat         ModelCapability = "chat"
	ModelCapabilityStreaming    ModelCapability = "streaming"
	ModelCapabilityToolCalling  ModelCapability = "tool-calling"
	ModelCapabilityVision       ModelCapability = "vision"
	ModelCapabilityJSONMode     ModelCapability = "json-mode"
	ModelCapabilityReasoning    ModelCapability = "reasoning"
	ModelCapabilitySystemPrompt ModelCapability = "system-prompt"
	ModelCapabilityPromptCache  ModelCapability = "prompt-cache"
	ModelCapabilityLongContext  ModelCapability = "long-context"
)

type ModelCapabilities []ModelCapability

type ModelStatus string

const (
	ModelStatusActive     ModelStatus = "active"
	ModelStatusPreview    ModelStatus = "preview"
	ModelStatusDeprecated ModelStatus = "deprecated"
)

type ContextLimits struct {
	ContextWindow   int
	MaxOutputTokens int
}

type ModelCost struct {
	Currency              string
	Unit                  string
	InputPerMillion       float64
	OutputPerMillion      float64
	CachedInputPerMillion float64
	Tiers                 []ModelCostTier
	Source                string
	SourceLastVerified    string
	Notes                 []string
}

type ModelCostTier struct {
	UpToInputTokens       int
	InputPerMillion       float64
	OutputPerMillion      float64
	CachedInputPerMillion float64
	Note                  string
}

type ModelEntry struct {
	ID               string
	DisplayName      string
	APIModel         string
	Provider         ProviderKind
	APIProviders     []ProviderKind
	ContextLimits    ContextLimits
	ReasoningEfforts []ReasoningEffort
	Capabilities     ModelCapabilities
	Cost             ModelCost
	Status           ModelStatus
	Aliases          []string
	Description      string
}

func (model ModelEntry) Validate() error {
	if strings.TrimSpace(model.ID) == "" {
		return fmt.Errorf("model id is required")
	}
	if strings.TrimSpace(model.DisplayName) == "" {
		return fmt.Errorf("model display name is required")
	}
	if strings.TrimSpace(model.APIModel) == "" {
		return fmt.Errorf("api model is required")
	}
	if !ValidPrimaryProviderKind(model.Provider) {
		return fmt.Errorf("unknown primary provider %q", model.Provider)
	}
	if model.ContextLimits.ContextWindow <= 0 {
		return fmt.Errorf("context window must be positive")
	}
	if model.ContextLimits.MaxOutputTokens <= 0 {
		return fmt.Errorf("max output tokens must be positive")
	}
	if model.ContextLimits.MaxOutputTokens > model.ContextLimits.ContextWindow {
		return fmt.Errorf("max output tokens cannot exceed context window")
	}
	if len(model.Capabilities) == 0 {
		return fmt.Errorf("at least one model capability is required")
	}
	for _, capability := range model.Capabilities {
		if !ValidModelCapability(capability) {
			return fmt.Errorf("unknown model capability %q", capability)
		}
	}
	for _, effort := range model.ReasoningEfforts {
		if !ValidReasoningEffort(effort) {
			return fmt.Errorf("unknown reasoning effort %q", effort)
		}
	}
	if err := model.Cost.Validate(); err != nil {
		return err
	}
	if !ValidModelStatus(model.Status) {
		return fmt.Errorf("unknown model status %q", model.Status)
	}
	if len(model.Aliases) == 0 {
		return fmt.Errorf("at least one model alias is required")
	}
	for _, alias := range model.Aliases {
		if strings.TrimSpace(alias) == "" {
			return fmt.Errorf("model aliases cannot be blank")
		}
	}
	for _, provider := range model.APIProviders {
		if !ValidRuntimeProviderKind(provider) {
			return fmt.Errorf("unknown api provider %q", provider)
		}
	}
	if len(model.APIProviders) > 0 && !model.AllowsProvider(model.Provider) {
		return fmt.Errorf("primary provider %q not allowed by api providers", model.Provider)
	}
	return nil
}

func (cost ModelCost) Validate() error {
	if cost.Currency != "USD" {
		return fmt.Errorf("model cost currency must be USD")
	}
	if cost.Unit != "per_1m_tokens" {
		return fmt.Errorf("model cost unit must be per_1m_tokens")
	}
	if cost.InputPerMillion < 0 || cost.OutputPerMillion < 0 || cost.CachedInputPerMillion < 0 {
		return fmt.Errorf("model cost values must be non-negative")
	}
	if len(cost.Tiers) == 0 {
		if cost.InputPerMillion == 0 && cost.OutputPerMillion == 0 {
			return fmt.Errorf("model cost must include base pricing or pricing tiers")
		}
		if cost.InputPerMillion == 0 || cost.OutputPerMillion == 0 {
			return fmt.Errorf("model cost base input and output rates must be positive")
		}
	}
	if err := validateCostTiers(cost.Tiers); err != nil {
		return err
	}
	if strings.TrimSpace(cost.Source) == "" {
		return fmt.Errorf("model cost source is required")
	}
	sourceLastVerified := strings.TrimSpace(cost.SourceLastVerified)
	if sourceLastVerified == "" {
		return fmt.Errorf("model cost source last verified date is required")
	}
	if _, err := time.Parse("2006-01-02", sourceLastVerified); err != nil {
		return fmt.Errorf("model cost source last verified date must use YYYY-MM-DD format")
	}
	return nil
}

func validateCostTiers(tiers []ModelCostTier) error {
	seenFallback := false
	for index, tier := range tiers {
		if tier.UpToInputTokens < 0 {
			return fmt.Errorf("model cost tier input ceiling must be non-negative")
		}
		if tier.InputPerMillion <= 0 || tier.OutputPerMillion <= 0 {
			return fmt.Errorf("model cost tier input and output rates must be positive")
		}
		if tier.CachedInputPerMillion < 0 {
			return fmt.Errorf("model cost tier cached input rate must be non-negative")
		}
		if tier.UpToInputTokens == 0 {
			if seenFallback {
				return fmt.Errorf("model cost tiers can include only one fallback tier")
			}
			if index != len(tiers)-1 {
				return fmt.Errorf("model cost fallback tier must be last")
			}
			seenFallback = true
		}
	}
	return nil
}

func (model ModelEntry) Supports(capability ModelCapability) bool {
	for _, candidate := range model.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func (model ModelEntry) AllowsProvider(provider ProviderKind) bool {
	if len(model.APIProviders) == 0 {
		return model.Provider == provider
	}
	for _, candidate := range model.APIProviders {
		if candidate == provider {
			return true
		}
	}
	return false
}

type Registry struct {
	entries map[string]ModelEntry
	models  []ModelEntry
}

func NewRegistry(entries []ModelEntry) (Registry, error) {
	registry := Registry{
		entries: make(map[string]ModelEntry),
		models:  make([]ModelEntry, 0, len(entries)),
	}
	seenModelIDs := make(map[string]struct{})
	for _, entry := range entries {
		if err := entry.Validate(); err != nil {
			return Registry{}, fmt.Errorf("invalid model %q: %w", entry.ID, err)
		}
		clonedEntry := cloneModelEntry(entry)
		modelID := normalizePattern(entry.ID)
		if modelID == "" {
			return Registry{}, fmt.Errorf("model id is required")
		}
		if _, ok := seenModelIDs[modelID]; ok {
			return Registry{}, fmt.Errorf("duplicate model id %q", modelID)
		}
		seenModelIDs[modelID] = struct{}{}
		if err := registry.register(entry.ID, clonedEntry); err != nil {
			return Registry{}, err
		}
		if err := registry.register(entry.APIModel, clonedEntry); err != nil {
			return Registry{}, err
		}
		for _, alias := range entry.Aliases {
			if err := registry.register(alias, clonedEntry); err != nil {
				return Registry{}, err
			}
		}
		registry.models = append(registry.models, clonedEntry)
	}
	return registry, nil
}

func (registry Registry) Get(pattern string) (ModelEntry, bool) {
	entry, ok := registry.entries[normalizePattern(pattern)]
	if !ok {
		return ModelEntry{}, false
	}
	return cloneModelEntry(entry), true
}

func (registry Registry) register(pattern string, entry ModelEntry) error {
	normalized := normalizePattern(pattern)
	if normalized == "" {
		return nil
	}
	if existing, ok := registry.entries[normalized]; ok && existing.ID != entry.ID {
		return fmt.Errorf("duplicate model lookup key %q for %q and %q", normalized, existing.ID, entry.ID)
	}
	registry.entries[normalized] = entry
	return nil
}

func normalizePattern(pattern string) string {
	return strings.ToLower(strings.TrimSpace(pattern))
}

func ValidPrimaryProviderKind(provider ProviderKind) bool {
	switch provider {
	case ProviderOpenAI, ProviderAnthropic, ProviderGoogle:
		return true
	default:
		return false
	}
}

func ValidRuntimeProviderKind(provider ProviderKind) bool {
	return ValidPrimaryProviderKind(provider) || provider == ProviderOpenAICompatible
}

func ValidReasoningEffort(effort ReasoningEffort) bool {
	switch effort {
	case ReasoningEffortNone, ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh:
		return true
	default:
		return false
	}
}

func ValidModelCapability(capability ModelCapability) bool {
	switch capability {
	case ModelCapabilityChat, ModelCapabilityStreaming, ModelCapabilityToolCalling, ModelCapabilityVision, ModelCapabilityJSONMode, ModelCapabilityReasoning, ModelCapabilitySystemPrompt, ModelCapabilityPromptCache, ModelCapabilityLongContext:
		return true
	default:
		return false
	}
}

func ValidModelStatus(status ModelStatus) bool {
	switch status {
	case ModelStatusActive, ModelStatusPreview, ModelStatusDeprecated:
		return true
	default:
		return false
	}
}
