package providers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/providers/anthropic"
	"github.com/Gitlawb/zero/internal/providers/gemini"
	"github.com/Gitlawb/zero/internal/providers/openai"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Options configures provider construction.
type Options struct {
	UserAgent     string
	HTTPClient    *http.Client
	ModelRegistry *modelregistry.Registry
}

// New creates a runtime provider for a resolved provider profile.
func New(profile config.ProviderProfile, options Options) (zeroruntime.Provider, error) {
	resolved, err := resolveProfile(profile, options)
	if err != nil {
		return nil, err
	}

	switch resolved.providerKind {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible:
		return openai.New(openai.Options{
			APIKey:          profile.APIKey,
			BaseURL:         resolved.baseURL,
			Model:           resolved.apiModel,
			AuthHeader:      profile.AuthHeader,
			AuthScheme:      profile.AuthScheme,
			AuthHeaderValue: profile.AuthHeaderValue,
			CustomHeaders:   profile.CustomHeaders,
			MaxTokens:       resolved.maxOutputTokens,
			HTTPClient:      options.HTTPClient,
			UserAgent:       options.UserAgent,
		})
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return anthropic.New(anthropic.Options{
			APIKey:          profile.APIKey,
			BaseURL:         resolved.baseURL,
			Model:           resolved.apiModel,
			AuthHeader:      profile.AuthHeader,
			AuthScheme:      profile.AuthScheme,
			AuthHeaderValue: profile.AuthHeaderValue,
			CustomHeaders:   profile.CustomHeaders,
			MaxTokens:       resolved.maxOutputTokens,
			HTTPClient:      options.HTTPClient,
			UserAgent:       options.UserAgent,
		})
	case config.ProviderKindGoogle:
		return gemini.New(gemini.Options{
			APIKey:          profile.APIKey,
			BaseURL:         resolved.baseURL,
			Model:           resolved.apiModel,
			AuthHeader:      profile.AuthHeader,
			AuthScheme:      profile.AuthScheme,
			AuthHeaderValue: profile.AuthHeaderValue,
			CustomHeaders:   profile.CustomHeaders,
			MaxTokens:       resolved.maxOutputTokens,
			HTTPClient:      options.HTTPClient,
			UserAgent:       options.UserAgent,
		})
	default:
		return nil, fmt.Errorf("unsupported provider kind %q", resolved.providerKind)
	}
}

type resolvedProfile struct {
	providerKind    config.ProviderKind
	apiModel        string
	baseURL         string
	maxOutputTokens int
}

// RuntimeMetadata describes the provider identity and concrete API model used
// after Zero model aliases and provider-kind defaults are resolved.
type RuntimeMetadata struct {
	ProviderKind config.ProviderKind
	APIModel     string
}

// ResolveRuntimeMetadata returns the provider kind and API model that New would
// use for a profile, without constructing a network client.
func ResolveRuntimeMetadata(profile config.ProviderProfile, options Options) (RuntimeMetadata, error) {
	resolved, err := resolveProfile(profile, options)
	if err != nil {
		return RuntimeMetadata{}, err
	}
	return RuntimeMetadata{
		ProviderKind: resolved.providerKind,
		APIModel:     resolved.apiModel,
	}, nil
}

func resolveProfile(profile config.ProviderProfile, options Options) (resolvedProfile, error) {
	model := strings.TrimSpace(profile.Model)
	if model == "" {
		return resolvedProfile{}, fmt.Errorf("provider %s requires model", profile.Name)
	}
	providerKind, explicitProvider := explicitProviderKind(profile)
	registry, err := defaultRegistry(options.ModelRegistry)
	if err != nil {
		return resolvedProfile{}, err
	}

	if entry, ok := registry.Get(model); ok {
		modelProvider := configKind(entry.Provider)
		if !explicitProvider || isImplicitOpenAI(profile, providerKind) {
			providerKind = modelProvider
		}
		if providerKind == config.ProviderKindOpenAICompatible {
			if !entry.AllowsProvider(modelregistry.ProviderOpenAICompatible) {
				return resolvedProfile{}, fmt.Errorf("zero model %s belongs to %s, not %s", entry.ID, entry.Provider, modelregistry.ProviderOpenAICompatible)
			}
		} else if providerKind == config.ProviderKindAnthropicCompat {
			if !entry.AllowsProvider(modelregistry.ProviderAnthropic) {
				return resolvedProfile{}, fmt.Errorf("zero model %s belongs to %s, not %s", entry.ID, entry.Provider, providerKind)
			}
		} else if providerKind != modelProvider {
			return resolvedProfile{}, fmt.Errorf("zero model %s belongs to %s, not %s", entry.ID, entry.Provider, providerKind)
		}
		return resolvedProfile{
			providerKind:    providerKind,
			apiModel:        entry.APIModel,
			baseURL:         strings.TrimSpace(profile.BaseURL),
			maxOutputTokens: entry.ContextLimits.MaxOutputTokens,
		}, nil
	}

	if providerKind == "" {
		providerKind = config.ProviderKindOpenAI
	}
	return resolvedProfile{
		providerKind: providerKind,
		apiModel:     model,
		baseURL:      strings.TrimSpace(profile.BaseURL),
	}, nil
}

func explicitProviderKind(profile config.ProviderProfile) (config.ProviderKind, bool) {
	providerKind := config.ProviderKind(strings.TrimSpace(strings.ToLower(string(profile.ProviderKind))))
	if providerKind != "" {
		return providerKind, true
	}
	provider := strings.TrimSpace(strings.ToLower(profile.Provider))
	if provider != "" {
		return config.ProviderKind(provider), true
	}
	return "", false
}

func isImplicitOpenAI(profile config.ProviderProfile, providerKind config.ProviderKind) bool {
	return providerKind == config.ProviderKindOpenAI &&
		strings.TrimSpace(string(profile.ProviderKind)) == "" &&
		strings.TrimSpace(profile.Provider) == "" &&
		strings.TrimSpace(profile.BaseURL) == ""
}

func configKind(provider modelregistry.ProviderKind) config.ProviderKind {
	return config.ProviderKind(provider)
}

func defaultRegistry(registry *modelregistry.Registry) (modelregistry.Registry, error) {
	if registry != nil {
		return *registry, nil
	}
	return modelregistry.DefaultRegistry()
}
