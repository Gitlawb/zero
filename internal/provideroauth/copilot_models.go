package provideroauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// APIFormat values a Copilot model can require. They match the config
// ProviderProfile.APIFormat strings the provider factory routes on.
const (
	copilotAPIFormatChat      = "chat-completions"
	copilotAPIFormatResponses = "responses"
)

const (
	// copilotModelCacheTTL keeps the /models capability map warm across model
	// switches within a session without re-fetching on every provider rebuild.
	// Short enough that a newly-granted model shows up on the next switch.
	copilotModelCacheTTL = 5 * time.Minute
)

// copilotModelsURL builds the /models endpoint for the given account-specific
// base URL, falling back to the public default host when baseURL is empty.
func copilotModelsURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = copilotDefaultBaseURL
	}
	return strings.TrimRight(baseURL, "/") + "/models"
}

var (
	copilotModelCacheMu   sync.Mutex
	copilotModelFormats   map[string]string
	copilotModelFetchedAt time.Time
)

// CopilotModelAPIFormat reports which transport a GitHub Copilot model must use
// — "responses" or "chat-completions" — based on the model's supported_endpoints
// in <baseURL>/models. A model that does NOT list /chat/completions
// but DOES list /responses (e.g. gpt-5.4-mini, gpt-5.3-codex,
// mai-code-1-flash-picker) must be driven via the Responses API; everything else
// (including models that list no endpoints, and the failure/offline case) uses
// chat-completions, preserving prior behavior.
//
// bearer is the minted Copilot token (the same value the OAuth resolver returns
// for the model call). baseURL is the account-specific host derived from that
// token (empty falls back to the public default). Results are cached
// process-wide for a short TTL so repeated provider rebuilds — e.g. every
// `/model` switch — don't re-probe.
func CopilotModelAPIFormat(ctx context.Context, httpClient *http.Client, bearer, baseURL, model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || strings.TrimSpace(bearer) == "" {
		return copilotAPIFormatChat
	}
	formats := copilotModelFormatMap(ctx, httpClient, bearer, baseURL)
	if format, ok := formats[model]; ok {
		return format
	}
	return copilotAPIFormatChat
}

// copilotModelFormatMap returns the cached model→api-format map, fetching and
// caching it when stale. On any fetch error it returns the last good map (or an
// empty map), so callers degrade to the chat-completions default rather than
// failing provider construction.
func copilotModelFormatMap(ctx context.Context, httpClient *http.Client, bearer, baseURL string) map[string]string {
	copilotModelCacheMu.Lock()
	defer copilotModelCacheMu.Unlock()

	if copilotModelFormats != nil && time.Since(copilotModelFetchedAt) < copilotModelCacheTTL {
		return copilotModelFormats
	}

	fetched, err := fetchCopilotModelFormats(ctx, httpClient, bearer, baseURL)
	if err != nil || len(fetched) == 0 {
		if copilotModelFormats != nil {
			return copilotModelFormats
		}
		return map[string]string{}
	}
	copilotModelFormats = fetched
	copilotModelFetchedAt = time.Now()
	return copilotModelFormats
}

// fetchCopilotModelFormats GETs /models and derives each model's api-format from
// its supported_endpoints, applying the same rule as CopilotModelAPIFormat.
func fetchCopilotModelFormats(ctx context.Context, httpClient *http.Client, bearer, baseURL string) (map[string]string, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotModelsURL(baseURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	req.Header.Set("Accept", "application/json")
	for key, value := range copilotChatHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, copilotMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &copilotModelsError{status: resp.StatusCode}
	}
	var payload struct {
		Data []struct {
			ID                 string   `json:"id"`
			SupportedEndpoints []string `json:"supported_endpoints"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	formats := make(map[string]string, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.ToLower(strings.TrimSpace(item.ID))
		if id == "" {
			continue
		}
		formats[id] = copilotEndpointsAPIFormat(item.SupportedEndpoints)
	}
	return formats, nil
}

// copilotEndpointsAPIFormat maps a model's supported_endpoints to the transport
// Zero should use. Chat-completions wins when present (it is the simpler, more
// broadly compatible path); otherwise a /responses-capable model uses the
// Responses transport; anything else falls back to chat-completions.
func copilotEndpointsAPIFormat(endpoints []string) string {
	if len(endpoints) == 0 {
		return copilotAPIFormatChat
	}
	hasResponses := false
	for _, endpoint := range endpoints {
		switch strings.ToLower(strings.TrimSpace(endpoint)) {
		case "/chat/completions":
			return copilotAPIFormatChat
		case "/responses", "ws:/responses":
			hasResponses = true
		}
	}
	if hasResponses {
		return copilotAPIFormatResponses
	}
	return copilotAPIFormatChat
}

type copilotModelsError struct{ status int }

func (e *copilotModelsError) Error() string {
	return "copilot /models returned status " + strings.TrimSpace(http.StatusText(e.status))
}
