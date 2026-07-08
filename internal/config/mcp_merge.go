package config

import (
	"fmt"
	"slices"
	"strings"
)

func mergeMCPConfig(dst *MCPConfig, src MCPConfig) {
	if len(src.Servers) == 0 {
		return
	}
	if dst.Servers == nil {
		dst.Servers = map[string]MCPServerConfig{}
	}
	for name, server := range src.Servers {
		dst.Servers[name] = mergeMCPServer(dst.Servers[name], server)
	}
}

func mergeProjectMCPConfig(dst *MCPConfig, src MCPConfig) error {
	if len(src.Servers) == 0 {
		return nil
	}
	if dst.Servers == nil {
		dst.Servers = map[string]MCPServerConfig{}
	}
	for name, server := range src.Servers {
		base := dst.Servers[name]
		candidate := mergeMCPServer(base, server)
		if projectMCPServerTargetChanges(base, server) && hasInheritedMCPCredentialMaterial(server, candidate) {
			return fmt.Errorf("project MCP server %q cannot override target while inheriting user credentials; set headers/env/oauth explicitly or use a new server name", name)
		}
		candidate.ProjectConfigured = true
		dst.Servers[name] = candidate
	}
	return nil
}

func mergeMCPServer(base MCPServerConfig, next MCPServerConfig) MCPServerConfig {
	if strings.TrimSpace(next.Type) != "" {
		base.Type = next.Type
	}
	if strings.TrimSpace(next.Command) != "" {
		base.Command = next.Command
	}
	if next.Args != nil {
		base.Args = append([]string{}, next.Args...)
	}
	if next.Env != nil {
		base.Env = copyMCPStringMap(next.Env)
	}
	if strings.TrimSpace(next.URL) != "" {
		base.URL = next.URL
	}
	if next.Headers != nil {
		base.Headers = copyMCPStringMap(next.Headers)
	}
	if strings.TrimSpace(next.Auth) != "" {
		base.Auth = next.Auth
	}
	if next.OAuth != nil {
		base.OAuth = next.OAuth
	}
	if next.disabledSet || next.Disabled {
		base.Disabled = next.Disabled
	}
	if next.configured {
		base.configured = true
	}
	if next.ProjectConfigured {
		base.ProjectConfigured = true
	}
	return base
}

func projectMCPServerTargetChanges(base MCPServerConfig, next MCPServerConfig) bool {
	if strings.TrimSpace(next.Type) != "" && mcpServerTransportKind(base) != mcpServerTransportKind(mergeMCPServer(base, next)) {
		return true
	}
	if strings.TrimSpace(next.URL) != "" && strings.TrimSpace(next.URL) != strings.TrimSpace(base.URL) {
		return true
	}
	if strings.TrimSpace(next.Command) != "" && strings.TrimSpace(next.Command) != strings.TrimSpace(base.Command) {
		return true
	}
	if next.Args != nil && !slices.Equal(trimMCPArgs(next.Args), trimMCPArgs(base.Args)) {
		return true
	}
	return false
}

func mcpServerTransportKind(server MCPServerConfig) string {
	if typ := strings.TrimSpace(strings.ToLower(server.Type)); typ != "" {
		return typ
	}
	if strings.TrimSpace(server.URL) != "" {
		return "http"
	}
	return "stdio"
}

func hasInheritedMCPCredentialMaterial(project MCPServerConfig, candidate MCPServerConfig) bool {
	if project.Headers == nil && hasMCPStringMapMaterial(candidate.Headers) {
		return true
	}
	if project.Env == nil && hasMCPStringMapMaterial(candidate.Env) {
		return true
	}
	if project.OAuth == nil && hasMCPOAuthMaterial(candidate.OAuth) {
		return true
	}
	return false
}

func hasMCPStringMapMaterial(values map[string]string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func hasMCPOAuthMaterial(oauth *MCPOAuthConfig) bool {
	if oauth == nil {
		return false
	}
	if strings.TrimSpace(oauth.ClientID) != "" ||
		strings.TrimSpace(oauth.ClientSecret) != "" ||
		strings.TrimSpace(oauth.AuthorizationEndpoint) != "" ||
		strings.TrimSpace(oauth.TokenEndpoint) != "" ||
		strings.TrimSpace(oauth.RegistrationEndpoint) != "" ||
		strings.TrimSpace(oauth.IssuerURL) != "" {
		return true
	}
	for _, scope := range oauth.Scopes {
		if strings.TrimSpace(scope) != "" {
			return true
		}
	}
	return false
}

func trimMCPArgs(values []string) []string {
	if values == nil {
		return nil
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}

func copyMCPStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
