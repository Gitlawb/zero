package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const wellKnownAuthServerPath = "/.well-known/oauth-authorization-server"

// ServerMetadata is the subset of an RFC 8414 authorization-server metadata
// document the engine consumes.
type ServerMetadata struct {
	Issuer                      string   `json:"issuer"`
	AuthorizationEndpoint       string   `json:"authorization_endpoint"`
	TokenEndpoint               string   `json:"token_endpoint"`
	DeviceAuthorizationEndpoint string   `json:"device_authorization_endpoint"`
	RegistrationEndpoint        string   `json:"registration_endpoint"`
	ScopesSupported             []string `json:"scopes_supported"`
}

// DiscoverAuthorizationServer fetches the RFC 8414 metadata document at the
// well-known path under baseURL.
func DiscoverAuthorizationServer(ctx context.Context, client *http.Client, baseURL string) (ServerMetadata, error) {
	metadataURL, err := joinWellKnown(baseURL)
	if err != nil {
		return ServerMetadata{}, err
	}
	return fetchMetadata(ctx, client, metadataURL)
}

// fetchMetadata GETs and decodes an authorization-server metadata document at an
// already-resolved URL (no well-known recomputation), so callers can also use it
// for the OIDC openid-configuration path.
func fetchMetadata(ctx context.Context, client *http.Client, metadataURL string) (ServerMetadata, error) {
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return ServerMetadata{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return ServerMetadata{}, fmt.Errorf("oauth: fetch authorization server metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ServerMetadata{}, fmt.Errorf("oauth: authorization server metadata returned HTTP %d", response.StatusCode)
	}
	var metadata ServerMetadata
	if err := json.NewDecoder(io.LimitReader(response.Body, tokenResponseLimit)).Decode(&metadata); err != nil {
		return ServerMetadata{}, fmt.Errorf("oauth: decode authorization server metadata: %w", err)
	}
	return metadata, nil
}

// joinWellKnown builds the RFC 8414 metadata URL, inserting the well-known
// segment between host and any issuer path component (so a path-based issuer is
// probed tenant-scoped, not at the host root).
func joinWellKnown(baseURL string) (string, error) {
	parsed, err := url.Parse(trimmed(baseURL))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("oauth: invalid base URL for metadata discovery: %q", baseURL)
	}
	issuerPath := strings.Trim(parsed.Path, "/")
	parsed.Path = wellKnownAuthServerPath
	if issuerPath != "" {
		parsed.Path = strings.TrimRight(wellKnownAuthServerPath, "/") + "/" + issuerPath
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
