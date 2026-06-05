package config

import (
	"encoding/json"
	"strings"
)

const OpenAIBaseURL = "https://api.openai.com/v1"
const AnthropicBaseURL = "https://api.anthropic.com"
const GoogleBaseURL = "https://generativelanguage.googleapis.com"

type ProviderKind string

const (
	ProviderKindOpenAI           ProviderKind = "openai"
	ProviderKindAnthropic        ProviderKind = "anthropic"
	ProviderKindGoogle           ProviderKind = "google"
	ProviderKindOpenAICompatible ProviderKind = "openai-compatible"
)

type ProviderProfile struct {
	Name         string       `json:"name"`
	Provider     string       `json:"provider,omitempty"`
	ProviderKind ProviderKind `json:"provider_kind,omitempty"`
	BaseURL      string       `json:"baseURL,omitempty"`
	APIKey       string       `json:"apiKey,omitempty"`
	Model        string       `json:"model,omitempty"`
	Description  string       `json:"description,omitempty"`
}

type FileConfig struct {
	ActiveProvider string            `json:"activeProvider,omitempty"`
	Providers      []ProviderProfile `json:"providers,omitempty"`
	MaxTurns       int               `json:"maxTurns,omitempty"`
	MCP            MCPConfig         `json:"mcp,omitempty"`
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
}

type ResolvedConfig struct {
	ActiveProvider string
	Providers      []ProviderProfile
	Provider       ProviderProfile
	MaxTurns       int
	MCP            MCPConfig
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

type MCPServerConfig struct {
	Type     string            `json:"type,omitempty"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`
}

func (cfg *FileConfig) UnmarshalJSON(data []byte) error {
	type rawConfig struct {
		ActiveProvider  string                     `json:"activeProvider"`
		Providers       []ProviderProfile          `json:"providers"`
		MaxTurns        int                        `json:"maxTurns"`
		MCP             MCPConfig                  `json:"mcp"`
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
	if cfg.MCP.Servers == nil && (len(raw.MCPServers) > 0 || len(raw.MCPServersSnake) > 0) {
		cfg.MCP.Servers = map[string]MCPServerConfig{}
	}
	for name, server := range raw.MCPServers {
		cfg.MCP.Servers[name] = server
	}
	for name, server := range raw.MCPServersSnake {
		cfg.MCP.Servers[name] = server
	}
	return nil
}

func (profile *ProviderProfile) UnmarshalJSON(data []byte) error {
	type rawProfile struct {
		Name         string `json:"name"`
		Provider     string `json:"provider"`
		ProviderKind string `json:"provider_kind"`
		BaseURL      string `json:"baseURL"`
		BaseURLSnake string `json:"base_url"`
		APIKey       string `json:"apiKey"`
		APIKeySnake  string `json:"api_key"`
		Model        string `json:"model"`
		ModelID      string `json:"model_id"`
		Description  string `json:"description"`
	}

	var raw rawProfile
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	profile.Name = strings.TrimSpace(raw.Name)
	profile.Provider = strings.TrimSpace(raw.Provider)
	profile.ProviderKind = ProviderKind(firstNonEmpty(raw.ProviderKind, raw.Provider))
	profile.BaseURL = strings.TrimSpace(firstNonEmpty(raw.BaseURL, raw.BaseURLSnake))
	profile.APIKey = firstNonEmpty(raw.APIKey, raw.APIKeySnake)
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
