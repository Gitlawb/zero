package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providers/anthropic"
	"github.com/Gitlawb/zero/internal/providers/gemini"
	"github.com/Gitlawb/zero/internal/providers/openai"
	"github.com/Gitlawb/zero/internal/providers/providerio"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Options configures provider construction.
type Options struct {
	UserAgent     string
	HTTPClient    *http.Client
	ModelRegistry *modelregistry.Registry
	// OAuthResolver, when set, lets the provider authenticate model calls with an
	// OAuth bearer token (preferred over the API key). nil => API-key auth only.
	// Applied to the OpenAI and Anthropic providers.
	OAuthResolver providerio.TokenResolver
}

// New creates a runtime provider for a resolved provider profile.
func New(profile config.ProviderProfile, options Options) (zeroruntime.Provider, error) {
	resolved, err := resolveProfile(profile, options)
	if err != nil {
		return nil, err
	}

	// The ChatGPT (Codex) catalog requires the Codex-flavored provider: the
	// Codex backend (chatgpt.com/backend-api/codex) 401s on every request that
	// does not carry the `originator` + `chatgpt-account-id` headers, so a
	// plain openai.New would always fail. We branch off the catalog id here
	// (not the provider kind) so other "openai-compatible" providers keep
	// using the openai.New path unchanged.
	if isCodexCatalog(profile, resolved) {
		return newCodexProvider(profile, resolved, options)
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
			OAuthResolver:   options.OAuthResolver,
			MaxTokens:       resolved.maxOutputTokens,
			HTTPClient:      options.HTTPClient,
			UserAgent:       options.UserAgent,
			ParseThinkTags:  parseThinkTagsForProfile(profile, resolved),
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
			OAuthResolver:   options.OAuthResolver,
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

func parseThinkTagsForProfile(profile config.ProviderProfile, resolved resolvedProfile) bool {
	if profile.ParseThinkTags != nil {
		return *profile.ParseThinkTags
	}
	if resolved.providerKind != config.ProviderKindOpenAICompatible {
		return false
	}
	return modelMayEmitThinkTags(resolved.apiModel)
}

func modelMayEmitThinkTags(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, marker := range []string{
		"deepseek-r1",
		"deepseek-reasoner",
		"gpt-oss",
		"glm-z1",
		"kimi-k2-thinking",
		"magistral",
		"minimax-m3",
		"nemotron",
		"qwen3",
		"qwq",
		"reasoner",
		"reasoning",
		"thinking",
	} {
		if strings.Contains(model, marker) {
			return true
		}
	}
	return false
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

	// baseURL resolves in this order: profile override → catalog default.
	// The catalog default makes the chatgpt Codex backend Just Work when a
	// user runs `zero` with a `catalogID: chatgpt` profile; the user can
	// still pin their own URL (e.g. a self-hosted Codex gateway or a local
	// OAuth proxy) and it wins.
	baseURL := strings.TrimSpace(profile.BaseURL)
	if baseURL == "" {
		if descriptor, ok := providercatalog.Get(profile.CatalogID); ok {
			baseURL = strings.TrimSpace(descriptor.DefaultBaseURL)
		}
	}

	if entry, ok := registry.Get(model); ok {
		modelProvider := configKind(entry.Provider)
		// Adopt the registry entry's provider only when the caller did not pin one.
		// (The old `|| isImplicitOpenAI(...)` clause was dead: explicitProvider==true
		// means ProviderKind or Provider is set, but isImplicitOpenAI required both
		// empty, so it could never add a case.)
		if !explicitProvider {
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
			baseURL:         baseURL,
			maxOutputTokens: entry.ContextLimits.MaxOutputTokens,
		}, nil
	}

	if providerKind == "" {
		providerKind = config.ProviderKindOpenAI
	}
	return resolvedProfile{
		providerKind: providerKind,
		apiModel:     model,
		baseURL:      baseURL,
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

func configKind(provider modelregistry.ProviderKind) config.ProviderKind {
	return config.ProviderKind(provider)
}

func defaultRegistry(registry *modelregistry.Registry) (modelregistry.Registry, error) {
	if registry != nil {
		return *registry, nil
	}
	return modelregistry.DefaultRegistry()
}

// isCodexCatalog reports whether the profile targets the ChatGPT Codex
// backend, which requires the Codex-flavored openai provider. The check uses
// the catalog id (not the provider kind) so the openai-compatible path is
// unaffected for other "openai-compatible" providers — a profile that
// happens to use a /v1 baseURL pointing at chatgpt.com without the chatgpt
// catalog id is the user's explicit misconfiguration, and the openai
// provider's standard error path surfaces it.
func isCodexCatalog(profile config.ProviderProfile, _ resolvedProfile) bool {
	return providercatalog.NormalizeID(profile.CatalogID) == "chatgpt"
}

// newCodexProvider builds a Codex-flavored openai provider for the chatgpt
// catalog. The Codex headers (`originator`, `chatgpt-account-id`) are
// injected by the CodexProvider wrapper. The `chatgpt-account-id` is
// resolved dynamically from the stored OAuth token's Account field on
// every request so a refresh that rotates the token (and its account
// claim) takes effect immediately — a static AccountID captured at
// construction would go stale on the first refresh.
func newCodexProvider(profile config.ProviderProfile, resolved resolvedProfile, options Options) (zeroruntime.Provider, error) {
	resolver := openai.CodexAccountResolver(func(ctx context.Context) (string, bool, error) {
		account := codexAccountFromStore(profile.Name)
		if account == "" {
			return "", false, nil
		}
		return account, true, nil
	})
	return openai.NewCodexProvider(openai.CodexOptions{
		Options: openai.Options{
			BaseURL:         resolved.baseURL,
			Model:           resolved.apiModel,
			AuthHeader:      profile.AuthHeader,
			AuthScheme:      profile.AuthScheme,
			AuthHeaderValue: profile.AuthHeaderValue,
			CustomHeaders:   profile.CustomHeaders,
			OAuthResolver:   options.OAuthResolver,
			MaxTokens:       resolved.maxOutputTokens,
			HTTPClient:      options.HTTPClient,
			UserAgent:       options.UserAgent,
			ParseThinkTags:  parseThinkTagsForProfile(profile, resolved),
		},
		// The chatgpt catalog overrides this with the Codex baseURL
		// (https://chatgpt.com/backend-api/codex) when the user does not
		// pin one, so this branch only needs to handle a user-supplied
		// override. The codex provider's constructor derives the
		// `/responses` endpoint from BaseURL, so the factory stays out of
		// the path.
		AccountResolver: resolver,
	})
}

// codexAccountFromStore reads the chatgpt_account_id from the stored OAuth
// token for the given provider name. It is called per-request (via the
// resolver) so a refresh that rotates the token takes effect immediately
// without restarting the agent. Returns "" when no token is stored (the
// Codex provider then omits the header and the user sees a clear 401).
func codexAccountFromStore(providerName string) string {
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return ""
	}
	token, ok, err := store.Load(oauth.ProviderKey(providerName))
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(token.Account)
}
