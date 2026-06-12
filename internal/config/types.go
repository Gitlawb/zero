package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

const OpenAIBaseURL = "https://api.openai.com/v1"
const AnthropicBaseURL = "https://api.anthropic.com"
const GoogleBaseURL = "https://generativelanguage.googleapis.com"

type ProviderKind string

const (
	ProviderKindOpenAI           ProviderKind = "openai"
	ProviderKindAnthropic        ProviderKind = "anthropic"
	ProviderKindAnthropicCompat  ProviderKind = "anthropic-compatible"
	ProviderKindGoogle           ProviderKind = "google"
	ProviderKindOpenAICompatible ProviderKind = "openai-compatible"
)

type ProviderProfile struct {
	Name            string            `json:"name"`
	Provider        string            `json:"provider,omitempty"`
	ProviderKind    ProviderKind      `json:"provider_kind,omitempty"`
	CatalogID       string            `json:"catalogID,omitempty"`
	BaseURL         string            `json:"baseURL,omitempty"`
	APIKey          string            `json:"apiKey,omitempty"`
	APIKeyEnv       string            `json:"apiKeyEnv,omitempty"`
	APIFormat       string            `json:"apiFormat,omitempty"`
	AuthHeader      string            `json:"authHeader,omitempty"`
	AuthScheme      string            `json:"authScheme,omitempty"`
	AuthHeaderValue string            `json:"authHeaderValue,omitempty"`
	CustomHeaders   map[string]string `json:"customHeaders,omitempty"`
	Model           string            `json:"model,omitempty"`
	Description     string            `json:"description,omitempty"`
}

func HasProviderProfile(profile ProviderProfile) bool {
	return strings.TrimSpace(profile.Name) != "" ||
		strings.TrimSpace(profile.Provider) != "" ||
		strings.TrimSpace(string(profile.ProviderKind)) != "" ||
		strings.TrimSpace(profile.CatalogID) != "" ||
		strings.TrimSpace(profile.BaseURL) != "" ||
		strings.TrimSpace(profile.APIKey) != "" ||
		strings.TrimSpace(profile.APIKeyEnv) != "" ||
		strings.TrimSpace(profile.APIFormat) != "" ||
		strings.TrimSpace(profile.AuthHeader) != "" ||
		strings.TrimSpace(profile.AuthScheme) != "" ||
		strings.TrimSpace(profile.AuthHeaderValue) != "" ||
		profile.CustomHeaders != nil ||
		strings.TrimSpace(profile.Model) != "" ||
		strings.TrimSpace(profile.Description) != ""
}

type SandboxConfig struct {
	MaxAutonomy string `json:"maxAutonomy,omitempty"`
	// Network controls whether shell commands classified as network-touching
	// (curl, git push, package installs, …) are allowed: "allow" or "deny".
	// Empty keeps the built-in default (deny). Without this knob the engine's
	// hard-coded NetworkDeny was unreachable from any config surface.
	Network string `json:"network,omitempty"`
	// AdditionalWriteRoots lists directories outside the workspace the sandbox
	// allows writes in. Each entry must be an existing directory; entries are
	// normalized (~-expanded, absolutized, symlink-resolved) at startup and an
	// invalid entry fails the run. Honored from the GLOBAL user config and CLI
	// flags only — deliberately not project config, so a cloned repo cannot
	// grant itself write access. Session-only grants use /add-dir instead.
	AdditionalWriteRoots []string `json:"additionalWriteRoots,omitempty"`
}

type NotifyConfig struct {
	Mode      string `json:"mode,omitempty"`
	FocusMode string `json:"focusMode,omitempty"`
}

type ToolsConfig struct {
	DeferThreshold    int `json:"deferThreshold,omitempty"`
	deferThresholdSet bool
}

type PreferencesConfig struct {
	FavoriteModels []string `json:"favoriteModels,omitempty"`
}

// ToolsOverride builds a ToolsConfig that explicitly overrides the deferred-tool
// threshold (including to 0, which disables deferral). Use this for programmatic
// Overrides — a bare ToolsConfig{DeferThreshold: 0} is indistinguishable from
// "unset" and will not override.
func ToolsOverride(deferThreshold int) ToolsConfig {
	return ToolsConfig{DeferThreshold: deferThreshold, deferThresholdSet: true}
}

func (cfg *ToolsConfig) UnmarshalJSON(data []byte) error {
	type rawTools struct {
		DeferThreshold *int `json:"deferThreshold"`
	}
	var raw rawTools
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	cfg.DeferThreshold = 0
	cfg.deferThresholdSet = false
	if raw.DeferThreshold != nil {
		cfg.DeferThreshold = *raw.DeferThreshold
		cfg.deferThresholdSet = true
	}
	return nil
}

type FileConfig struct {
	ActiveProvider string            `json:"activeProvider,omitempty"`
	Providers      []ProviderProfile `json:"providers,omitempty"`
	MaxTurns       int               `json:"maxTurns,omitempty"`
	MCP            MCPConfig         `json:"mcp,omitempty"`
	Sandbox        SandboxConfig     `json:"sandbox,omitempty"`
	Notify         NotifyConfig      `json:"notify,omitempty"`
	Tools          ToolsConfig       `json:"tools,omitempty"`
	Preferences    PreferencesConfig `json:"preferences,omitempty"`
}

type ResolveOptions struct {
	UserConfigPath    string
	ProjectConfigPath string
	ProviderCommand   string
	Env               map[string]string
	Overrides         Overrides
}

type Overrides struct {
	ActiveProvider string
	Providers      []ProviderProfile
	Provider       ProviderProfile
	MaxTurns       int
	MCP            MCPConfig
	Sandbox        SandboxConfig
	Notify         NotifyConfig
	Tools          ToolsConfig
}

type ResolvedConfig struct {
	ActiveProvider string
	Providers      []ProviderProfile
	Provider       ProviderProfile
	MaxTurns       int
	MCP            MCPConfig
	Sandbox        SandboxConfig
	Notify         NotifyConfig
	Tools          ToolsConfig
	Preferences    PreferencesConfig
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

type MCPServerConfig struct {
	Type        string            `json:"type,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Auth        string            `json:"auth,omitempty"`
	OAuth       *MCPOAuthConfig   `json:"oauth,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	disabledSet bool
}

// MCPOAuthConfig describes how to authenticate to a remote MCP server using an
// OAuth 2.0 + PKCE authorization-code flow. Endpoints may be discovered from the
// authorization server's metadata document; explicit values here override or
// fill in anything discovery cannot provide. Client credentials are optional
// when the server supports dynamic client registration.
type MCPOAuthConfig struct {
	ClientID              string   `json:"clientID,omitempty"`
	ClientSecret          string   `json:"clientSecret,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint,omitempty"`
	TokenEndpoint         string   `json:"tokenEndpoint,omitempty"`
	RegistrationEndpoint  string   `json:"registrationEndpoint,omitempty"`
	IssuerURL             string   `json:"issuerURL,omitempty"`
}

func (cfg *FileConfig) UnmarshalJSON(data []byte) error {
	type rawConfig struct {
		ActiveProvider  string                     `json:"activeProvider"`
		Providers       []ProviderProfile          `json:"providers"`
		MaxTurns        int                        `json:"maxTurns"`
		MCP             MCPConfig                  `json:"mcp"`
		Sandbox         SandboxConfig              `json:"sandbox"`
		Notify          NotifyConfig               `json:"notify"`
		Tools           ToolsConfig                `json:"tools"`
		Preferences     PreferencesConfig          `json:"preferences"`
		MCPServers      map[string]MCPServerConfig `json:"mcpServers"`
		MCPServersSnake map[string]MCPServerConfig `json:"mcp_servers"`
	}

	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	cfg.ActiveProvider = raw.ActiveProvider
	cfg.Providers = raw.Providers
	cfg.MaxTurns = raw.MaxTurns
	cfg.MCP = raw.MCP
	cfg.Sandbox = raw.Sandbox
	cfg.Notify = raw.Notify
	cfg.Tools = raw.Tools
	cfg.Preferences = raw.Preferences
	if cfg.MCP.Servers == nil && (len(raw.MCPServers) > 0 || len(raw.MCPServersSnake) > 0) {
		cfg.MCP.Servers = map[string]MCPServerConfig{}
	}
	for name, server := range raw.MCPServers {
		cfg.MCP.Servers[name] = server
	}
	for name, server := range raw.MCPServersSnake {
		if _, exists := cfg.MCP.Servers[name]; exists {
			return fmt.Errorf("MCP server %q is defined in both mcpServers and mcp_servers; mcp_servers would override mcpServers", name)
		}
		cfg.MCP.Servers[name] = server
	}
	return nil
}

func (server *MCPServerConfig) UnmarshalJSON(data []byte) error {
	type rawServer struct {
		Type     string            `json:"type"`
		Command  string            `json:"command"`
		Args     []string          `json:"args"`
		Env      map[string]string `json:"env"`
		URL      string            `json:"url"`
		Headers  map[string]string `json:"headers"`
		Auth     string            `json:"auth"`
		OAuth    *MCPOAuthConfig   `json:"oauth"`
		Disabled *bool             `json:"disabled"`
	}

	var raw rawServer
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	server.Type = raw.Type
	server.Command = raw.Command
	server.Args = raw.Args
	server.Env = raw.Env
	server.URL = raw.URL
	server.Headers = raw.Headers
	server.Auth = raw.Auth
	server.OAuth = raw.OAuth
	server.Disabled = false
	server.disabledSet = false
	if raw.Disabled != nil {
		server.Disabled = *raw.Disabled
		server.disabledSet = true
	}
	return nil
}

func (profile *ProviderProfile) UnmarshalJSON(data []byte) error {
	type rawProfile struct {
		Name                 string            `json:"name"`
		Provider             string            `json:"provider"`
		ProviderKind         string            `json:"provider_kind"`
		ProviderKindCamel    string            `json:"providerKind"`
		CatalogID            string            `json:"catalogID"`
		CatalogIDSnake       string            `json:"catalog_id"`
		BaseURL              string            `json:"baseURL"`
		BaseURLSnake         string            `json:"base_url"`
		APIKey               string            `json:"apiKey"`
		APIKeySnake          string            `json:"api_key"`
		APIKeyEnv            string            `json:"apiKeyEnv"`
		APIKeyEnvSnake       string            `json:"api_key_env"`
		APIFormat            string            `json:"apiFormat"`
		APIFormatSnake       string            `json:"api_format"`
		AuthHeader           string            `json:"authHeader"`
		AuthHeaderSnake      string            `json:"auth_header"`
		AuthScheme           string            `json:"authScheme"`
		AuthSchemeSnake      string            `json:"auth_scheme"`
		AuthHeaderValue      string            `json:"authHeaderValue"`
		AuthHeaderValueSnake string            `json:"auth_header_value"`
		CustomHeaders        map[string]string `json:"customHeaders"`
		CustomHeadersSnake   map[string]string `json:"custom_headers"`
		Model                string            `json:"model"`
		ModelID              string            `json:"model_id"`
		Description          string            `json:"description"`
	}

	var raw rawProfile
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	profile.Name = strings.TrimSpace(raw.Name)
	profile.Provider = strings.TrimSpace(raw.Provider)
	profile.ProviderKind = ProviderKind(firstNonEmpty(raw.ProviderKind, raw.ProviderKindCamel, raw.Provider))
	profile.CatalogID = strings.TrimSpace(firstNonEmpty(raw.CatalogID, raw.CatalogIDSnake))
	profile.BaseURL = strings.TrimSpace(firstNonEmpty(raw.BaseURL, raw.BaseURLSnake))
	profile.APIKey = firstNonEmpty(raw.APIKey, raw.APIKeySnake)
	profile.APIKeyEnv = strings.TrimSpace(firstNonEmpty(raw.APIKeyEnv, raw.APIKeyEnvSnake))
	profile.APIFormat = strings.TrimSpace(firstNonEmpty(raw.APIFormat, raw.APIFormatSnake))
	profile.AuthHeader = strings.TrimSpace(firstNonEmpty(raw.AuthHeader, raw.AuthHeaderSnake))
	profile.AuthScheme = strings.TrimSpace(firstNonEmpty(raw.AuthScheme, raw.AuthSchemeSnake))
	profile.AuthHeaderValue = firstNonEmpty(raw.AuthHeaderValue, raw.AuthHeaderValueSnake)
	profile.CustomHeaders = firstNonNilMap(raw.CustomHeaders, raw.CustomHeadersSnake)
	profile.Model = strings.TrimSpace(firstNonEmpty(raw.Model, raw.ModelID))
	profile.Description = strings.TrimSpace(raw.Description)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonNilMap(values ...map[string]string) map[string]string {
	for _, value := range values {
		if value != nil {
			return copyStringMap(value)
		}
	}
	return nil
}

func copyStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
