package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/redaction"
	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

const (
	defaultWebSearchLimit  = 5
	maxWebSearchLimit      = 10
	webSearchTimeout       = 10 * time.Second
	webSearchBodyLimit     = 256 * 1024
	webSearchRedirectLimit = 5
)

// searchResult is one hit returned by a search backend.
type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// searchBackend discovers URLs for a query. It is an interface so any hosted
// search API (or a fake, in tests) can be dropped in without touching the tool.
// nil means no backend is configured.
type searchBackend interface {
	Search(ctx context.Context, query string, limit int) ([]searchResult, error)
}

type webSearchTool struct {
	baseTool
	backend searchBackend
}

// NewWebSearchTool builds the web_search tool with the env-configured backend.
func NewWebSearchTool() Tool {
	return newWebSearchToolWithBackend(defaultSearchBackend())
}

func newWebSearchToolWithBackend(backend searchBackend) Tool {
	return webSearchTool{
		baseTool: baseTool{
			name:        "web_search",
			description: "Search the web for a query and return ranked results (title, URL, snippet). Complements web_fetch, which retrieves a single known URL.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {
						Type:        "string",
						Description: "Search query.",
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of results to return.",
						Default:     defaultWebSearchLimit,
						Minimum:     intPtr(1),
						Maximum:     intPtr(maxWebSearchLimit),
					},
				},
				Required:             []string{"query"},
				AdditionalProperties: false,
			},
			// Network egress, so it carries the same prompt-gated posture as web_fetch
			// (the codebase enforces this for every CoreNetworkTools entry). It only
			// discovers public URLs and mutates nothing, but the query still leaves
			// the machine for an operator-configured endpoint, which warrants a prompt.
			safety: Safety{
				SideEffect:      SideEffectNetwork,
				Permission:      PermissionPrompt,
				Reason:          "Performs a web search over the network.",
				AdvertiseInAuto: true,
			},
		},
		backend: backend,
	}
}

// hostedSearchBackend is implemented by backends that talk to a known HTTP
// endpoint, so the sandbox network policy can be enforced against that host.
type hostedSearchBackend interface {
	endpointHost() string
}

// RunWithSandbox enforces the engine's network policy against the search
// backend's endpoint before searching, so web_search honors the same
// scoped/deny posture as web_fetch and sandboxed shell egress. A nil engine (or
// an unconfigured backend) falls through to the normal Run path.
func (tool webSearchTool) RunWithSandbox(ctx context.Context, args map[string]any, engine *zeroSandbox.Engine) Result {
	if engine != nil && tool.backend != nil {
		host := ""
		if hosted, ok := tool.backend.(hostedSearchBackend); ok {
			host = hosted.endpointHost()
		}
		if err := enforceScopedNetworkPolicy(engine, host); err != nil {
			return errorResult("Error: web_search blocked: " + err.Error())
		}
	}
	return tool.Run(ctx, args)
}

func (tool webSearchTool) Run(ctx context.Context, args map[string]any) Result {
	query, err := stringArg(args, "query", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_search: " + err.Error())
	}
	// max=0 disables intArg's upper bound so an over-cap limit clamps here rather
	// than erroring; min=1 still rejects non-positive limits.
	limit, err := intArg(args, "limit", defaultWebSearchLimit, 1, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_search: " + err.Error())
	}
	if limit > maxWebSearchLimit {
		limit = maxWebSearchLimit
	}

	if tool.backend == nil {
		return errorResult("Error: no search backend configured. Set ZERO_WEBSEARCH_BASE_URL (and ZERO_WEBSEARCH_API_KEY) to enable web_search.")
	}

	runCtx, cancel := context.WithTimeout(ctx, webSearchTimeout)
	defer cancel()

	results, err := tool.backend.Search(runCtx, query, limit)
	if err != nil {
		return errorResult("Error performing web search: " + redactWebSearchText(err.Error()))
	}
	if len(results) == 0 {
		return okResult("No results for query: " + redactWebSearchText(query))
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return okResult(redactWebSearchText(formatSearchResults(results)))
}

// formatSearchResults renders results as a compact numbered list:
// "1. Title — URL" with the snippet indented on the next line.
func formatSearchResults(results []searchResult) string {
	lines := make([]string, 0, len(results)*2)
	for index, result := range results {
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = "(untitled)"
		}
		lines = append(lines, fmt.Sprintf("%d. %s — %s", index+1, title, strings.TrimSpace(result.URL)))
		if snippet := strings.TrimSpace(result.Snippet); snippet != "" {
			lines = append(lines, "   "+snippet)
		}
	}
	return strings.Join(lines, "\n")
}

// defaultSearchBackend returns the env-configured generic backend, or nil when
// ZERO_WEBSEARCH_BASE_URL is unset (the tool then reports it as unconfigured).
func defaultSearchBackend() searchBackend {
	baseURL := strings.TrimSpace(os.Getenv("ZERO_WEBSEARCH_BASE_URL"))
	if baseURL == "" {
		return nil
	}
	return &httpSearchBackend{
		client: &http.Client{
			Timeout:       webSearchTimeout,
			CheckRedirect: sameHostRedirectPolicy,
		},
		baseURL:  baseURL,
		apiKey:   strings.TrimSpace(os.Getenv("ZERO_WEBSEARCH_API_KEY")),
		provider: strings.TrimSpace(os.Getenv("ZERO_WEBSEARCH_PROVIDER")),
	}
}

// sameHostRedirectPolicy confines the search backend to redirects that stay on
// the originally-requested host. RunWithSandbox only policy-checks the configured
// endpoint host, so a cross-host redirect could otherwise egress to a host the
// sandbox network policy never authorized; refusing it keeps the check
// fail-closed across redirects.
func sameHostRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= webSearchRedirectLimit {
		return fmt.Errorf("stopped after %d redirects", webSearchRedirectLimit)
	}
	origin := via[0].URL
	if !strings.EqualFold(req.URL.Hostname(), origin.Hostname()) {
		return fmt.Errorf("refusing cross-host redirect to %q", req.URL.Hostname())
	}
	// Refuse a scheme change too — a same-host https→http downgrade would send the
	// query (and the bearer token Go keeps on same-host redirects) over plaintext.
	if !strings.EqualFold(req.URL.Scheme, origin.Scheme) {
		return fmt.Errorf("refusing redirect that changes scheme %q→%q", origin.Scheme, req.URL.Scheme)
	}
	return nil
}

// httpSearchBackend is the generic JSON backend: POST {query,limit} to a
// configured endpoint and parse an array of {title,url,snippet}. Its shape
// matches common hosted search APIs without copying any of their code; swap in a
// backend-specific implementation by implementing searchBackend.
type httpSearchBackend struct {
	client   *http.Client
	baseURL  string
	apiKey   string
	provider string
}

// endpointHost returns the hostname of the configured search endpoint, or "" if
// the base URL cannot be parsed. Used to enforce the sandbox network policy.
func (backend *httpSearchBackend) endpointHost() string {
	parsed, err := url.Parse(backend.baseURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func (backend *httpSearchBackend) Search(ctx context.Context, query string, limit int) ([]searchResult, error) {
	requestBody := map[string]any{"query": query, "limit": limit}
	// Forward the configured provider so an aggregating endpoint can route the
	// query; without this the ZERO_WEBSEARCH_PROVIDER knob would be inert.
	if backend.provider != "" {
		requestBody["provider"] = backend.provider
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("encode search request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, backend.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build search request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "zero-web-search/0.1")
	if backend.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+backend.apiKey)
	}

	response, err := backend.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, webSearchBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read search response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Status only; the body may echo the request (incl. auth) so it is not surfaced.
		return nil, fmt.Errorf("search backend returned HTTP %d", response.StatusCode)
	}
	return parseSearchResults(body)
}

// parseSearchResults accepts either a bare array [{title,url,snippet}] or a
// wrapped object {"results":[...]}, the two shapes common across providers.
func parseSearchResults(body []byte) ([]searchResult, error) {
	type rawResult struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty search backend response")
	}

	convert := func(raw []rawResult) []searchResult {
		out := make([]searchResult, 0, len(raw))
		for _, item := range raw {
			out = append(out, searchResult{Title: item.Title, URL: item.URL, Snippet: item.Snippet})
		}
		return out
	}

	if trimmed[0] == '[' {
		var bare []rawResult
		if err := json.Unmarshal(trimmed, &bare); err != nil {
			return nil, fmt.Errorf("parse search results: %w", err)
		}
		return convert(bare), nil
	}
	var wrapped struct {
		Results []rawResult `json:"results"`
	}
	if err := json.Unmarshal(trimmed, &wrapped); err != nil {
		return nil, fmt.Errorf("parse search results: %w", err)
	}
	return convert(wrapped.Results), nil
}

func redactWebSearchText(value string) string {
	return redaction.RedactString(value, redaction.Options{})
}
