// Package providerhealth validates provider configuration and optionally probes
// the configured provider endpoint with a bounded, non-generating request.
package providerhealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providers"
	"github.com/Gitlawb/zero/internal/providers/providerio"
	"github.com/Gitlawb/zero/internal/redaction"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

type Category string

const (
	CategoryConfig        Category = "config"
	CategoryUnsupported   Category = "unsupported"
	CategoryAuth          Category = "auth"
	CategoryRateLimit     Category = "rate_limit"
	CategoryNetwork       Category = "network"
	CategoryTimeout       Category = "timeout"
	CategoryProvider      Category = "provider_error"
	CategoryProviderError          = CategoryProvider
	CategoryConnectivity  Category = "connectivity"
)

const defaultTimeout = 5 * time.Second

type Options struct {
	Profile      config.ProviderProfile
	Connectivity bool
	HTTPClient   *http.Client
	Timeout      time.Duration
	UserAgent    string
}

type Result struct {
	Status       Status  `json:"status"`
	ProviderName string  `json:"providerName,omitempty"`
	ProviderKind string  `json:"providerKind,omitempty"`
	Model        string  `json:"model,omitempty"`
	APIModel     string  `json:"apiModel,omitempty"`
	BaseURL      string  `json:"baseURL,omitempty"`
	Checks       []Check `json:"checks"`
}

type Check struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Status   Status         `json:"status"`
	Category Category       `json:"category,omitempty"`
	Message  string         `json:"message"`
	Details  map[string]any `json:"details,omitempty"`
}

func (result Result) Check(id string) *Check {
	for index := range result.Checks {
		if result.Checks[index].ID == id {
			return &result.Checks[index]
		}
	}
	return nil
}

func (result Result) PrimaryCheck() *Check {
	if connectivity := result.Check("provider.connectivity"); connectivity != nil {
		return connectivity
	}
	for index := range result.Checks {
		if result.Checks[index].Status == StatusFail {
			return &result.Checks[index]
		}
	}
	for index := range result.Checks {
		if result.Checks[index].Status == StatusWarn {
			return &result.Checks[index]
		}
	}
	if len(result.Checks) == 0 {
		return nil
	}
	return &result.Checks[0]
}

func Probe(ctx context.Context, options Options) Result {
	profile := options.Profile
	result := Result{
		Status:       StatusPass,
		ProviderName: redact(strings.TrimSpace(profile.Name), profile),
		ProviderKind: redact(strings.TrimSpace(string(profile.ProviderKind)), profile),
		Model:        redact(strings.TrimSpace(profile.Model), profile),
		BaseURL:      redactBaseURL(profile.BaseURL, profile),
		Checks:       []Check{},
	}

	if !config.HasProviderProfile(profile) {
		result.add(check("provider.config", "Provider config", StatusFail, CategoryConfig, "No LLM provider is configured.", nil, profile))
		return result.finalize()
	}
	if strings.TrimSpace(profile.Model) == "" {
		result.add(check("provider.config", "Provider config", StatusFail, CategoryConfig, fmt.Sprintf("Provider %s requires model.", providerName(profile)), nil, profile))
		return result.finalize()
	}
	result.add(check("provider.config", "Provider config", StatusPass, CategoryConfig, fmt.Sprintf("Provider config loaded for %s.", providerName(profile)), map[string]any{
		"name":     profile.Name,
		"provider": profile.ProviderKind,
		"baseURL":  profile.BaseURL,
		"model":    profile.Model,
	}, profile))

	if unsupported := unsupportedCatalogCheck(profile); unsupported != nil {
		result.add(*unsupported)
		return result.finalize()
	}

	metadata, err := providers.ResolveRuntimeMetadata(profile, providers.Options{})
	if err != nil {
		result.add(check("provider.runtime", "Provider runtime", StatusFail, CategoryConfig, "Provider runtime did not resolve: "+err.Error(), nil, profile))
		return result.finalize()
	}
	result.ProviderKind = redact(string(metadata.ProviderKind), profile)
	result.APIModel = redact(metadata.APIModel, profile)
	result.add(check("provider.runtime", "Provider runtime", StatusPass, CategoryConfig, fmt.Sprintf("Provider runtime resolves %s as %s.", providerName(profile), metadata.ProviderKind), map[string]any{
		"apiModel":     metadata.APIModel,
		"providerKind": metadata.ProviderKind,
	}, profile))

	if credentialRequired(profile) && !hasCredential(profile) {
		result.add(check("provider.auth", "Provider auth", StatusFail, CategoryAuth, fmt.Sprintf("Provider %s requires API credentials.", providerName(profile)), credentialDetails(profile), profile))
		return result.finalize()
	}
	if hasCredential(profile) {
		result.add(check("provider.auth", "Provider auth", StatusPass, CategoryAuth, fmt.Sprintf("Provider %s has credentials configured.", providerName(profile)), credentialDetails(profile), profile))
	} else {
		result.add(check("provider.auth", "Provider auth", StatusPass, CategoryAuth, fmt.Sprintf("Provider %s does not require API credentials.", providerName(profile)), credentialDetails(profile), profile))
	}

	if !options.Connectivity {
		return result.finalize()
	}
	result.add(connectivityCheck(ctx, profile, metadata.ProviderKind, options))
	return result.finalize()
}

func (result *Result) add(check Check) {
	result.Checks = append(result.Checks, check)
}

func (result Result) finalize() Result {
	status := StatusPass
	for _, check := range result.Checks {
		if check.Status == StatusFail {
			status = StatusFail
			break
		}
		if check.Status == StatusWarn {
			status = StatusWarn
		}
	}
	result.Status = status
	return result
}

func unsupportedCatalogCheck(profile config.ProviderProfile) *Check {
	if strings.TrimSpace(profile.CatalogID) == "" {
		return nil
	}
	descriptor, err := providercatalog.Require(profile.CatalogID)
	if err != nil {
		out := check("provider.runtime", "Provider runtime", StatusFail, CategoryConfig, err.Error(), nil, profile)
		return &out
	}
	if providercatalog.RuntimeSupported(descriptor) {
		return nil
	}
	out := check("provider.runtime", "Provider runtime", StatusFail, CategoryUnsupported, fmt.Sprintf("Provider %q uses transport %q: %s.", descriptor.ID, descriptor.Transport, providercatalog.RuntimeUnsupportedReason(descriptor)), map[string]any{
		"transport": descriptor.Transport,
	}, profile)
	return &out
}

func connectivityCheck(ctx context.Context, profile config.ProviderProfile, kind config.ProviderKind, options Options) Check {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := healthRequest(requestCtx, profile, kind, options)
	if err != nil {
		return check("provider.connectivity", "Provider connectivity", StatusFail, CategoryConfig, err.Error(), nil, profile)
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return classifyTransportError(err, profile)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return check("provider.connectivity", "Provider connectivity", StatusPass, CategoryConnectivity, fmt.Sprintf("Provider endpoint reachable (%d).", response.StatusCode), map[string]any{
			"statusCode": response.StatusCode,
			"endpoint":   request.URL.String(),
		}, profile)
	}

	category := CategoryProvider
	status := StatusFail
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		category = CategoryAuth
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, 529:
		category = CategoryRateLimit
		status = StatusWarn
	}
	message := responseMessage(response.StatusCode, body)
	return check("provider.connectivity", "Provider connectivity", status, category, message, map[string]any{
		"statusCode": response.StatusCode,
		"endpoint":   request.URL.String(),
	}, profile)
}

func healthRequest(ctx context.Context, profile config.ProviderProfile, kind config.ProviderKind, options Options) (*http.Request, error) {
	baseURL, err := resolvedBaseURL(profile, kind)
	if err != nil {
		return nil, err
	}
	endpoint := baseURL + healthPath(kind)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if options.UserAgent != "" {
		request.Header.Set("User-Agent", options.UserAgent)
	}
	applyAuth(request, profile, kind)
	return request, nil
}

func resolvedBaseURL(profile config.ProviderProfile, kind config.ProviderKind) (string, error) {
	baseURL := strings.TrimSpace(profile.BaseURL)
	switch kind {
	case config.ProviderKindOpenAI:
		return providerio.NormalizeBaseURL(baseURL, config.OpenAIBaseURL, "OpenAI")
	case config.ProviderKindAnthropic:
		return providerio.NormalizeBaseURL(baseURL, config.AnthropicBaseURL, "Anthropic")
	case config.ProviderKindGoogle:
		return providerio.NormalizeBaseURL(baseURL, config.GoogleBaseURL, "Google")
	case config.ProviderKindOpenAICompatible, config.ProviderKindAnthropicCompat:
		if baseURL == "" {
			return "", fmt.Errorf("%s provider %s requires baseURL for connectivity probing", kind, providerName(profile))
		}
		return providerio.NormalizeBaseURL(baseURL, "", string(kind))
	default:
		return "", fmt.Errorf("unsupported provider kind %q", kind)
	}
}

func healthPath(kind config.ProviderKind) string {
	switch kind {
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return "/v1/models"
	case config.ProviderKindGoogle:
		return "/v1beta/models"
	default:
		return "/models"
	}
}

func applyAuth(request *http.Request, profile config.ProviderProfile, kind config.ProviderKind) {
	switch kind {
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		request.Header.Set("anthropic-version", "2023-06-01")
		providerio.ApplyAuthHeaders(request, providerio.AuthHeaders{
			APIKey:            profile.APIKey,
			DefaultAuthHeader: "x-api-key",
			AuthHeader:        profile.AuthHeader,
			AuthScheme:        profile.AuthScheme,
			AuthHeaderValue:   profile.AuthHeaderValue,
			CustomHeaders:     profile.CustomHeaders,
		})
	case config.ProviderKindGoogle:
		providerio.ApplyAuthHeaders(request, providerio.AuthHeaders{
			APIKey:            profile.APIKey,
			DefaultAuthHeader: "x-goog-api-key",
			AuthHeader:        profile.AuthHeader,
			AuthScheme:        profile.AuthScheme,
			AuthHeaderValue:   profile.AuthHeaderValue,
			CustomHeaders:     profile.CustomHeaders,
		})
	default:
		providerio.ApplyAuthHeaders(request, providerio.AuthHeaders{
			APIKey:            profile.APIKey,
			DefaultAuthHeader: "Authorization",
			DefaultAuthScheme: "Bearer",
			AuthHeader:        profile.AuthHeader,
			AuthScheme:        profile.AuthScheme,
			AuthHeaderValue:   profile.AuthHeaderValue,
			CustomHeaders:     profile.CustomHeaders,
		})
	}
}

func classifyTransportError(err error, profile config.ProviderProfile) Check {
	category := CategoryNetwork
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		category = CategoryTimeout
	} else {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			category = CategoryTimeout
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			if errors.Is(urlErr.Err, context.DeadlineExceeded) || errors.Is(urlErr.Err, context.Canceled) {
				category = CategoryTimeout
			}
		}
	}
	return check("provider.connectivity", "Provider connectivity", StatusFail, category, "Provider connectivity failed: "+err.Error(), nil, profile)
}

func responseMessage(statusCode int, body []byte) string {
	message := strings.TrimSpace(string(body))
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if parsed.Error.Message != "" {
			message = parsed.Error.Message
		} else if parsed.Message != "" {
			message = parsed.Message
		}
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return fmt.Sprintf("Provider endpoint returned %d: %s", statusCode, message)
}

func check(id string, label string, status Status, category Category, message string, details map[string]any, profile config.ProviderProfile) Check {
	out := Check{
		ID:       id,
		Label:    label,
		Status:   status,
		Category: category,
		Message:  redact(message, profile),
	}
	if len(details) > 0 {
		out.Details = map[string]any{}
		for key, value := range details {
			out.Details[key] = redactAny(key, value, profile)
		}
	}
	return out
}

func redactAny(key string, value any, profile config.ProviderProfile) any {
	if text, ok := value.(string); ok {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if strings.Contains(normalizedKey, "url") || strings.Contains(normalizedKey, "endpoint") {
			return redactBaseURL(text, profile)
		}
		return redact(text, profile)
	}
	return value
}

func redactBaseURL(baseURL string, profile config.ProviderProfile) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err == nil && parsed.User != nil {
		parsed.User = nil
		baseURL = parsed.String()
	}
	return redact(baseURL, profile)
}

func redact(message string, profile config.ProviderProfile) string {
	secrets := providerSecrets(profile)
	return redaction.RedactString(providerio.Redact(message, secrets...), redaction.Options{
		ExtraSecretValues: secrets,
	})
}

func providerSecrets(profile config.ProviderProfile) []string {
	secrets := []string{profile.APIKey, profile.AuthHeaderValue}
	for _, value := range profile.CustomHeaders {
		if strings.TrimSpace(value) != "" {
			secrets = append(secrets, value)
		}
	}
	return secrets
}

func credentialDetails(profile config.ProviderProfile) map[string]any {
	details := map[string]any{}
	if strings.TrimSpace(profile.APIKeyEnv) != "" {
		details["apiKeyEnv"] = profile.APIKeyEnv
	}
	if strings.TrimSpace(profile.AuthHeader) != "" {
		details["authHeader"] = profile.AuthHeader
	}
	return details
}

func hasCredential(profile config.ProviderProfile) bool {
	return strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.AuthHeaderValue) != ""
}

func credentialRequired(profile config.ProviderProfile) bool {
	if strings.TrimSpace(profile.CatalogID) != "" {
		if descriptor, err := providercatalog.Require(profile.CatalogID); err == nil {
			return descriptor.RequiresAuth
		}
	}
	switch profile.ProviderKind {
	case config.ProviderKindOpenAI, config.ProviderKindAnthropic, config.ProviderKindGoogle:
		return true
	default:
		return false
	}
}

func providerName(profile config.ProviderProfile) string {
	if strings.TrimSpace(profile.Name) != "" {
		return strings.TrimSpace(profile.Name)
	}
	if strings.TrimSpace(string(profile.ProviderKind)) != "" {
		return strings.TrimSpace(string(profile.ProviderKind))
	}
	return strings.TrimSpace(profile.Provider)
}
