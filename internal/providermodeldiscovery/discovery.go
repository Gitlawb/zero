package providermodeldiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providermodelcatalog"
	"github.com/Gitlawb/zero/internal/providers/providerio"
	"github.com/Gitlawb/zero/internal/redaction"
)

const anthropicVersion = "2023-06-01"

type Model struct {
	ID               string
	Description      string
	ContextWindow    int
	ToolCall         bool
	Reasoning        bool
	InputModalities  []string
	OutputModalities []string
	InputCost        float64
	OutputCost       float64
	Tags             []string
	Source           string
}

type Options struct {
	HTTPClient     *http.Client
	ModelsDevURL   string
	OpenGatewayURL string
}

func DiscoverCatalog(ctx context.Context, provider providercatalog.Descriptor, profile config.ProviderProfile, options Options) ([]Model, error) {
	catalogModels, catalogErr := fetchCatalogModels(ctx, provider, options)
	canProbeProvider := modelDiscoveryAllowed(profile) && (!provider.RequiresAuth || discoveryHasCredential(profile))
	if canProbeProvider {
		liveModels, liveErr := Discover(ctx, profile, options)
		if liveErr == nil {
			if merged := mergeLiveModels(provider, liveModels, catalogModels); len(merged) > 0 {
				return merged, nil
			}
			// Live probe returned 200 but its model ids didn't match the catalog, so
			// the merge is empty. Fall through to the curated catalog below instead of
			// returning an empty list that collapses the picker to the bare built-in
			// set (and shows a misleading "no model ids" error) (M11).
		} else if len(catalogModels) == 0 {
			return nil, liveErr
		}
	}
	if len(catalogModels) > 0 {
		return catalogModels, nil
	}
	if catalogErr != nil {
		return nil, catalogErr
	}
	return nil, fmt.Errorf("no provider models discovered")
}

// discoveryHasCredential reports whether the profile carries a usable credential
// for an authenticated /models probe. A profile may authenticate via a raw
// auth-header value instead of APIKey, so treat either as present — consistent
// with config credential checks and zerocommands ProviderSnapshot.APIKeySet.
func discoveryHasCredential(profile config.ProviderProfile) bool {
	return strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.AuthHeaderValue) != ""
}

func Discover(ctx context.Context, profile config.ProviderProfile, options Options) ([]Model, error) {
	switch discoveryProviderKind(profile) {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible:
		return discoverOpenAIModels(ctx, profile, options)
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return discoverAnthropicModels(ctx, profile, options)
	default:
		return nil, fmt.Errorf("provider %s does not expose model discovery", displayProviderName(profile))
	}
}

// DiscoverOllamaContextWindow asks a local Ollama daemon for a model's context
// length via its native /api/show endpoint. The generic /v1/models probe
// (parseModelsResponse) only ever returns id/description — OpenAI-compatible
// listings don't carry context-window metadata, and a custom/local model tag
// (e.g. a user's own Ollama pull, including a ":cloud"-tagged model proxied
// through the local daemon) has no curated-catalog entry to borrow one from
// either, so modelContextWindow has nothing to show for it without this. Only
// meaningful for the local Ollama provider (baseURL like
// http://localhost:11434/v1) — Ollama Cloud's hosted API is a different
// service and isn't assumed to expose the same endpoint.
func DiscoverOllamaContextWindow(ctx context.Context, baseURL string, model string, options Options) (int, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return 0, fmt.Errorf("model name is required")
	}
	endpoint, err := ollamaShowEndpoint(baseURL)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(struct {
		Model string `json:"model"`
	}{Model: model})
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("ollama show endpoint returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return parseOllamaShowContextWindow(body)
}

// ollamaShowEndpoint derives the native Ollama API root from the
// OpenAI-compatible base URL Zero stores for the provider (".../v1") and
// appends /api/show.
func ollamaShowEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("provider base URL is required for ollama model discovery")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid provider base URL %q", baseURL)
	}
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/v1")
	parsed.Path = strings.TrimRight(path, "/") + "/api/show"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// parseOllamaShowContextWindow extracts the context length from an
// /api/show response. Ollama reports it under model_info with an
// architecture-prefixed key (e.g. "llama.context_length",
// "qwen2.context_length") — the prefix varies by model family, so this scans
// for any key ending in ".context_length" rather than hardcoding one.
func parseOllamaShowContextWindow(body []byte) (int, error) {
	// model_info mixes value types (strings like "general.architecture", numbers
	// like "*.context_length"), so decode values generically and only try to
	// read a number out of the one key we actually care about.
	var payload struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("decode ollama show response: %w", err)
	}
	for key, value := range payload.ModelInfo {
		if !strings.HasSuffix(key, ".context_length") {
			continue
		}
		if window, ok := value.(float64); ok && window > 0 {
			return int(window), nil
		}
	}
	return 0, fmt.Errorf("ollama show response did not report a context length")
}

func discoverOpenAIModels(ctx context.Context, profile config.ProviderProfile, options Options) ([]Model, error) {
	endpoint, err := modelsEndpoint(profile.BaseURL)
	if err != nil {
		return nil, err
	}
	return fetchProviderModels(ctx, endpoint, profile, options, providerio.AuthHeaders{
		APIKey:            profile.APIKey,
		DefaultAuthHeader: "Authorization",
		DefaultAuthScheme: "Bearer",
		AuthHeader:        profile.AuthHeader,
		AuthScheme:        profile.AuthScheme,
		AuthHeaderValue:   profile.AuthHeaderValue,
		CustomHeaders:     providerio.CopyHeaders(profile.CustomHeaders),
	}, nil)
}

func discoverAnthropicModels(ctx context.Context, profile config.ProviderProfile, options Options) ([]Model, error) {
	endpoint, err := anthropicModelsEndpoint(profile.BaseURL)
	if err != nil {
		return nil, err
	}
	return fetchProviderModels(ctx, endpoint, profile, options, providerio.AuthHeaders{
		APIKey:            profile.APIKey,
		DefaultAuthHeader: "x-api-key",
		AuthHeader:        profile.AuthHeader,
		AuthScheme:        profile.AuthScheme,
		AuthHeaderValue:   profile.AuthHeaderValue,
		CustomHeaders:     profile.CustomHeaders,
	}, func(request *http.Request) {
		request.Header.Set("anthropic-version", anthropicVersion)
	})
}

func fetchProviderModels(ctx context.Context, endpoint string, profile config.ProviderProfile, options Options, auth providerio.AuthHeaders, configure func(*http.Request)) ([]Model, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// Authenticate via either an APIKey or a raw auth-header value / custom
	// headers, matching how the live providers build their requests
	// (internal/providers/providerio). Honoring AuthHeaderValue keeps discovery
	// consistent with the credential-present logic elsewhere.
	providerio.ApplyAuthHeaders(request, auth)
	request.Header.Set("Accept", "application/json")
	if configure != nil {
		configure(request)
	}

	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, redactDiscoveryError(err, profile)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, redactDiscoveryError(err, profile)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, redactDiscoveryError(fmt.Errorf("models endpoint returned %s: %s", response.Status, strings.TrimSpace(string(body))), profile)
	}

	models, err := parseModelsResponse(body)
	if err != nil {
		return nil, redactDiscoveryError(err, profile)
	}
	return models, nil
}

func modelDiscoveryAllowed(profile config.ProviderProfile) bool {
	switch discoveryProviderKind(profile) {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible, config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return true
	default:
		return false
	}
}

func discoveryProviderKind(profile config.ProviderProfile) config.ProviderKind {
	kind := config.ProviderKind(strings.TrimSpace(strings.ToLower(string(profile.ProviderKind))))
	if kind == "" {
		kind = config.ProviderKind(strings.TrimSpace(strings.ToLower(profile.Provider)))
	}
	return kind
}

func modelsEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("provider base URL is required for model discovery")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid provider base URL %q", baseURL)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func anthropicModelsEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("provider base URL is required for model discovery")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid provider base URL %q", baseURL)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(path), "/v1") {
		parsed.Path = path + "/models"
	} else {
		parsed.Path = path + "/v1/models"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

type modelsResponse struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Name        string `json:"name"`
	} `json:"data"`
}

func parseModelsResponse(body []byte) ([]Model, error) {
	var payload modelsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	seen := map[string]bool{}
	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		description := strings.TrimSpace(item.DisplayName)
		if description == "" {
			description = strings.TrimSpace(item.Name)
		}
		models = append(models, Model{ID: id, Description: description})
	}
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	if len(models) == 0 {
		return nil, fmt.Errorf("models endpoint returned no model ids")
	}
	return models, nil
}

func fetchCatalogModels(ctx context.Context, provider providercatalog.Descriptor, options Options) ([]Model, error) {
	models, err := providermodelcatalog.FetchRemote(ctx, provider, providermodelcatalog.FetchOptions{
		HTTPClient:     options.HTTPClient,
		ModelsDevURL:   options.ModelsDevURL,
		OpenGatewayURL: options.OpenGatewayURL,
	})
	if err != nil {
		return nil, err
	}
	return modelsFromCatalog(models), nil
}

func modelsFromCatalog(models []providermodelcatalog.Model) []Model {
	result := make([]Model, 0, len(models))
	for _, model := range models {
		result = append(result, Model{
			ID:               model.ID,
			Description:      model.Description,
			ContextWindow:    model.ContextWindow,
			ToolCall:         model.ToolCall,
			Reasoning:        model.Reasoning,
			InputModalities:  append([]string{}, model.InputModalities...),
			OutputModalities: append([]string{}, model.OutputModalities...),
			InputCost:        model.InputCost,
			OutputCost:       model.OutputCost,
			Tags:             append([]string{}, model.Tags...),
			Source:           model.Source,
		})
	}
	return result
}

func mergeLiveModels(provider providercatalog.Descriptor, liveModels []Model, catalogModels []Model) []Model {
	byID := map[string]Model{}
	for _, model := range catalogModels {
		byID[model.ID] = model
	}
	hasCatalog := len(byID) > 0
	result := make([]Model, 0, len(liveModels))
	for _, live := range liveModels {
		if catalog, ok := byID[live.ID]; ok {
			if !providermodelcatalog.IsCodingModel(catalogModelFromDiscovery(catalog)) {
				continue
			}
			catalog.Source = firstDiscoverySource(catalog.Source, "live")
			result = append(result, catalog)
			continue
		}
		if hasCatalog {
			continue
		}
		if !liveModelAllowedWithoutCatalog(provider, live.ID) {
			continue
		}
		live.Source = firstDiscoverySource(live.Source, "live")
		result = append(result, live)
	}
	return result
}

func liveModelAllowedWithoutCatalog(provider providercatalog.Descriptor, id string) bool {
	if !providermodelcatalog.ModelIDAllowedForProvider(provider.ID, id) {
		return false
	}
	if providermodelcatalog.IsKnownNonCodingModelID(id) {
		return false
	}
	if provider.Local || strings.HasPrefix(provider.ID, "custom-") {
		return true
	}
	return providermodelcatalog.LooksLikeCodingModelID(id)
}

func catalogModelFromDiscovery(model Model) providermodelcatalog.Model {
	return providermodelcatalog.Model{
		ID:               model.ID,
		Description:      model.Description,
		ContextWindow:    model.ContextWindow,
		ToolCall:         model.ToolCall,
		Reasoning:        model.Reasoning,
		InputModalities:  append([]string{}, model.InputModalities...),
		OutputModalities: append([]string{}, model.OutputModalities...),
		InputCost:        model.InputCost,
		OutputCost:       model.OutputCost,
		Tags:             append([]string{}, model.Tags...),
		Source:           model.Source,
	}
}

func firstDiscoverySource(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func redactDiscoveryError(err error, profile config.ProviderProfile) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", redaction.RedactString(err.Error(), redaction.Options{ExtraSecretValues: []string{
		profile.APIKey,
		profile.AuthHeaderValue,
	}}))
}

func displayProviderName(profile config.ProviderProfile) string {
	for _, value := range []string{profile.Name, profile.CatalogID, string(profile.ProviderKind), profile.Provider} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "provider"
}
