package modelregistry

import (
	"fmt"
	"strings"
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
	Source                string
	SourceLastVerified    string
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
	if strings.TrimSpace(cost.Source) == "" {
		return fmt.Errorf("model cost source is required")
	}
	if strings.TrimSpace(cost.SourceLastVerified) == "" {
		return fmt.Errorf("model cost source last verified date is required")
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
}

func NewRegistry(entries []ModelEntry) Registry {
	registry := Registry{entries: make(map[string]ModelEntry)}
	for _, entry := range entries {
		registry.register(entry.ID, entry)
		registry.register(entry.APIModel, entry)
		for _, alias := range entry.Aliases {
			registry.register(alias, entry)
		}
	}
	return registry
}

func (registry Registry) Get(pattern string) (ModelEntry, bool) {
	entry, ok := registry.entries[normalizePattern(pattern)]
	return entry, ok
}

func (registry Registry) register(pattern string, entry ModelEntry) {
	normalized := normalizePattern(pattern)
	if normalized == "" {
		return
	}
	registry.entries[normalized] = entry
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
