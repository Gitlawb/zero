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
	"github.com/Gitlawb/zero/internal/redaction"
)

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
	canProbeProvider := openAICompatibleDiscoveryAllowed(profile) && (!provider.RequiresAuth || strings.TrimSpace(profile.APIKey) != "")
	if canProbeProvider {
		liveModels, liveErr := Discover(ctx, profile, options)
		if liveErr == nil {
			return mergeLiveModels(liveModels, catalogModels), nil
		}
		if len(catalogModels) == 0 {
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

func Discover(ctx context.Context, profile config.ProviderProfile, options Options) ([]Model, error) {
	if !openAICompatibleDiscoveryAllowed(profile) {
		return nil, fmt.Errorf("provider %s does not expose OpenAI-compatible model discovery", displayProviderName(profile))
	}
	endpoint, err := modelsEndpoint(profile.BaseURL)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if key := strings.TrimSpace(profile.APIKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	request.Header.Set("Accept", "application/json")

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

func openAICompatibleDiscoveryAllowed(profile config.ProviderProfile) bool {
	kind := config.ProviderKind(strings.TrimSpace(strings.ToLower(string(profile.ProviderKind))))
	if kind == "" {
		kind = config.ProviderKind(strings.TrimSpace(strings.ToLower(profile.Provider)))
	}
	return kind == config.ProviderKindOpenAI || kind == config.ProviderKindOpenAICompatible
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

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
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
		models = append(models, Model{ID: id})
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

func mergeLiveModels(liveModels []Model, catalogModels []Model) []Model {
	byID := map[string]Model{}
	for _, model := range catalogModels {
		byID[model.ID] = model
	}
	result := make([]Model, 0, len(liveModels))
	for _, live := range liveModels {
		if catalog, ok := byID[live.ID]; ok {
			catalog.Source = firstDiscoverySource(catalog.Source, "live")
			result = append(result, catalog)
			continue
		}
		live.Source = firstDiscoverySource(live.Source, "live")
		result = append(result, live)
	}
	return result
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
