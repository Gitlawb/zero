// GitHub Copilot token exchange + request headers. See openrouter.go for the
// package doc.
//
// The Copilot login itself is a plain RFC 8628 device-code flow against
// github.com (see the "copilot" preset in internal/oauth/presets.go), run by
// the generic oauth.Manager — `zero auth login copilot` stores the resulting
// GitHub user token under provider:copilot. That token is durable and is NOT
// the bearer for model calls: this file exchanges it for the short-lived
// Copilot token that api.githubcopilot.com actually accepts, and supplies the
// editor headers the Copilot backend requires on every request.
//
// This is an UNDOCUMENTED, reverse-engineered use of GitHub's Copilot API (the
// same one the editor plugins use). It is not a supported developer API and may
// change without notice; bulk/automated use can trip GitHub's abuse detection.
package provideroauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// copilotTokenEndpoint mints the short-lived Copilot token from a GitHub
	// user token. It is a GET whose only credential is the GitHub token in the
	// Authorization header (scheme "token", not "Bearer").
	copilotTokenEndpoint = "https://api.github.com/copilot_internal/v2/token"

	// The editor identity headers the Copilot backend expects. These mirror the
	// values the Copilot Chat plugin sends; the backend rejects requests without
	// a recognized editor-version / integration-id.
	copilotEditorVersion       = "vscode/1.107.0"
	copilotEditorPluginVersion = "copilot-chat/0.35.0"
	copilotUserAgent           = "GitHubCopilotChat/0.35.0"
	copilotIntegrationID       = "vscode-chat"
	copilotAPIVersion          = "2026-06-01"

	// copilotDefaultBaseURL is the public Copilot API host, used when a minted
	// token carries no proxy-ep (see CopilotBaseURLFromToken).
	copilotDefaultBaseURL = "https://api.githubcopilot.com"

	// copilotRefreshBuffer re-mints the Copilot token this long before its hard
	// expiry so an in-flight request never carries an about-to-expire bearer.
	copilotRefreshBuffer = 60 * time.Second

	copilotMaxBody = 1 << 20
)

// copilotChatHeaders returns the non-auth headers api.githubcopilot.com requires
// on every model request (chat completions and /models discovery alike). The
// bearer (the minted Copilot token) is applied separately by the caller's auth
// path. Kept as the single source of truth for both SetCopilotChatHeaders (the
// live request path) and discovery.
func copilotChatHeaders() map[string]string {
	return map[string]string{
		"Editor-Version":         copilotEditorVersion,
		"Editor-Plugin-Version":  copilotEditorPluginVersion,
		"Copilot-Integration-Id": copilotIntegrationID,
		"Openai-Intent":          "conversation-panel",
		"X-Github-Api-Version":   copilotAPIVersion,
		"User-Agent":             copilotUserAgent,
	}
}

// CopilotChatHeaderMap returns a copy of the editor identity headers the Copilot
// backend requires, for callers that attach headers via a map (e.g. model
// discovery's profile.CustomHeaders) rather than mutating an *http.Request.
func CopilotChatHeaderMap() map[string]string {
	src := copilotChatHeaders()
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetCopilotChatHeaders sets the non-auth headers api.githubcopilot.com requires
// on every model request. The bearer (the minted Copilot token) is applied
// separately by the provider's auth path. Installed as the openai provider's
// SetRequestExtra callback by the provider factory.
func SetCopilotChatHeaders(req *http.Request) {
	for key, value := range copilotChatHeaders() {
		req.Header.Set(key, value)
	}
}

// CopilotBaseURLFromToken derives the account-specific Copilot API base URL from
// the minted Copilot token's proxy-ep field. The token is a semicolon-delimited
// set of key=value pairs, one of which is proxy-ep=proxy.<segment>.githubcopilot.com
// where <segment> distinguishes Individual, Business, and Enterprise accounts
// (e.g. proxy.business.githubcopilot.com). The API host is the same value with
// the leading "proxy." replaced by "api." (-> api.business.githubcopilot.com).
// Using the wrong host can hide models or reject requests, so we honor the host
// the backend assigned this token — exactly as the Copilot editor plugins do.
// Falls back to the public default when the token has no proxy-ep.
func CopilotBaseURLFromToken(token string) string {
	for _, part := range strings.Split(token, ";") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "proxy-ep=") {
			continue
		}
		host := strings.TrimSpace(strings.TrimPrefix(part, "proxy-ep="))
		if host == "" {
			return copilotDefaultBaseURL
		}
		if strings.HasPrefix(host, "proxy.") {
			host = "api." + strings.TrimPrefix(host, "proxy.")
		}
		return "https://" + host
	}
	return copilotDefaultBaseURL
}

// SetCopilotDynamicHeaders sets the editor identity headers plus the per-request
// signals the Copilot backend expects on model calls, mirroring the Copilot Chat
// plugin: X-Initiator ("user" when the last message is user-authored, else
// "agent" for tool/assistant follow-ups), Openai-Intent "conversation-edits",
// and Copilot-Vision-Request when the request carries image input. Installed as
// the openai provider's SetRequestExtra callback for Copilot model requests; the
// static SetCopilotChatHeaders remains for /models discovery.
func SetCopilotDynamicHeaders(req *http.Request) {
	SetCopilotChatHeaders(req)
	req.Header.Set("Openai-Intent", "conversation-edits")
	lastRole, hasImages := copilotPeekRequest(req)
	initiator := "user"
	if lastRole != "" && !strings.EqualFold(lastRole, "user") {
		initiator = "agent"
	}
	req.Header.Set("X-Initiator", initiator)
	if hasImages {
		req.Header.Set("Copilot-Vision-Request", "true")
	}
}

// copilotPeekRequest inspects the outgoing request body (without consuming it,
// via GetBody) to report the last message role and whether image input is
// present. It tolerates both the chat-completions ("messages") and Responses
// ("input") shapes and never errors — a body it cannot parse yields the safe
// defaults ("", false), i.e. X-Initiator "user" and no vision header.
func copilotPeekRequest(req *http.Request) (lastRole string, hasImages bool) {
	if req == nil || req.GetBody == nil {
		return "", false
	}
	rc, err := req.GetBody()
	if err != nil {
		return "", false
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, copilotMaxBody))
	if err != nil {
		return "", false
	}
	var payload struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		Input []struct {
			Role string `json:"role"`
		} `json:"input"`
	}
	_ = json.Unmarshal(data, &payload)
	if n := len(payload.Messages); n > 0 {
		lastRole = payload.Messages[n-1].Role
	} else if n := len(payload.Input); n > 0 {
		lastRole = payload.Input[n-1].Role
	}
	lower := strings.ToLower(string(data))
	hasImages = strings.Contains(lower, `"image_url"`) ||
		strings.Contains(lower, `"input_image"`) ||
		strings.Contains(lower, `"type":"image"`)
	return lastRole, hasImages
}

// copilotTokenResponse is the (trimmed) shape of the copilot_internal token
// endpoint's JSON body.
type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	RefreshIn int64  `json:"refresh_in"`
}

// MintCopilotToken exchanges a durable GitHub user token for a short-lived
// Copilot token usable as the bearer against api.githubcopilot.com. It returns
// the token and its hard expiry. The GitHub token is sent with the "token"
// auth scheme (not "Bearer"); the editor headers are required or the endpoint
// 403s.
func MintCopilotToken(ctx context.Context, client *http.Client, githubToken string) (string, time.Time, error) {
	if strings.TrimSpace(githubToken) == "" {
		return "", time.Time{}, errors.New("provideroauth: copilot token exchange requires a GitHub token")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotTokenEndpoint, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	request.Header.Set("Authorization", "token "+strings.TrimSpace(githubToken))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Editor-Version", copilotEditorVersion)
	request.Header.Set("Editor-Plugin-Version", copilotEditorPluginVersion)
	request.Header.Set("User-Agent", copilotUserAgent)
	request.Header.Set("X-Github-Api-Version", copilotAPIVersion)

	response, err := client.Do(request)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("provideroauth: copilot token exchange: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, copilotMaxBody))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		// Never echo the response body (it may carry token-shaped material).
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
			return "", time.Time{}, fmt.Errorf("provideroauth: copilot token exchange returned HTTP %d (no active Copilot subscription for this GitHub account, or the login was revoked — run `zero auth login copilot` again)", response.StatusCode)
		}
		return "", time.Time{}, fmt.Errorf("provideroauth: copilot token exchange returned HTTP %d", response.StatusCode)
	}
	var parsed copilotTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("provideroauth: decode copilot token response: %w", err)
	}
	token := strings.TrimSpace(parsed.Token)
	if token == "" {
		return "", time.Time{}, errors.New("provideroauth: copilot token exchange returned an empty token")
	}
	var expiresAt time.Time
	if parsed.ExpiresAt > 0 {
		expiresAt = time.Unix(parsed.ExpiresAt, 0).UTC()
	}
	return token, expiresAt, nil
}

// CopilotTokenSource mints and caches the short-lived Copilot bearer, re-minting
// it from the durable GitHub token when it is missing, near expiry, or after an
// upstream 401. It is safe for concurrent use. One instance is built per Copilot
// provider construction (see internal/cli/oauth_provider.go).
type CopilotTokenSource struct {
	// HTTPClient performs the token exchange; nil => a client with a sane timeout.
	HTTPClient *http.Client
	// GitHubToken returns the current durable GitHub user token (typically
	// oauth.Manager.GetFresh under provider:copilot). It is consulted on every
	// re-mint so a fresh login is picked up without restarting the agent.
	GitHubToken func(ctx context.Context) (string, error)
	// Now is the time source; nil => time.Now. Injected by tests.
	Now func() time.Time

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
}

// Bearer returns a valid Copilot token, re-minting when the cache is empty,
// within the refresh buffer of expiry, or forceRefresh is set (the single retry
// after an upstream 401). A Copilot token with no known expiry is re-minted on
// every call (fail closed) rather than cached indefinitely.
func (s *CopilotTokenSource) Bearer(ctx context.Context, forceRefresh bool) (string, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !forceRefresh && s.cached != "" && !s.expiresAt.IsZero() && s.expiresAt.After(now().Add(copilotRefreshBuffer)) {
		return s.cached, nil
	}
	if s.GitHubToken == nil {
		return "", errors.New("provideroauth: copilot token source has no GitHub token accessor")
	}
	githubToken, err := s.GitHubToken(ctx)
	if err != nil {
		return "", err
	}
	token, expiresAt, err := MintCopilotToken(ctx, s.HTTPClient, githubToken)
	if err != nil {
		return "", err
	}
	s.cached = token
	s.expiresAt = expiresAt
	return token, nil
}

// EnableCopilotModels unlocks Copilot models that require per-account policy
// acceptance (e.g. Claude, Grok on some plans) so they surface in discovery and
// accept requests. It mints a Copilot token from the durable GitHub token,
// derives the account host from it, lists /models, and POSTs an "enabled" policy
// for every model whose policy gate is not already enabled. Best-effort: it
// returns the ids it enabled and never fails the caller for a model that cannot
// be enabled. Mirrors the Copilot editor plugins' post-login model enablement.
func EnableCopilotModels(ctx context.Context, client *http.Client, githubToken string) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	token, _, err := MintCopilotToken(ctx, client, githubToken)
	if err != nil {
		return nil, err
	}
	baseURL := CopilotBaseURLFromToken(token)
	ids, err := copilotModelsNeedingEnable(ctx, client, baseURL, token)
	if err != nil {
		return nil, err
	}
	enabled := make([]string, 0, len(ids))
	for _, id := range ids {
		if enableCopilotModelPolicy(ctx, client, baseURL, token, id) {
			enabled = append(enabled, id)
		}
	}
	return enabled, nil
}

// copilotModelsNeedingEnable lists model ids whose policy gate is present but not
// "enabled". Models with no policy field are ungated (nothing to enable) and are
// skipped.
func copilotModelsNeedingEnable(ctx context.Context, client *http.Client, baseURL, bearer string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotModelsURL(baseURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	req.Header.Set("Accept", "application/json")
	for key, value := range copilotChatHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, copilotMaxBody))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &copilotModelsError{status: resp.StatusCode}
	}
	var payload struct {
		Data []struct {
			ID     string `json:"id"`
			Policy *struct {
				State string `json:"state"`
			} `json:"policy"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	var ids []string
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || item.Policy == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Policy.State), "enabled") {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// enableCopilotModelPolicy POSTs an "enabled" policy for one model. Reports
// success; a failure is swallowed so one gated model can't abort the batch.
func enableCopilotModelPolicy(ctx context.Context, client *http.Client, baseURL, bearer, modelID string) bool {
	url := strings.TrimRight(baseURL, "/") + "/models/" + modelID + "/policy"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"state":"enabled"}`))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for key, value := range copilotChatHeaders() {
		req.Header.Set(key, value)
	}
	req.Header.Set("Openai-Intent", "chat-policy")
	req.Header.Set("X-Interaction-Type", "chat-policy")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, copilotMaxBody))
	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
}
