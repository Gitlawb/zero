package mcp

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

func TestNormalizeConfigValidatesTransportBoundaries(t *testing.T) {
	valid := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {
			Type:    "stdio",
			Command: "docs-mcp",
			Args:    []string{"--workspace", "."},
			Env:     map[string]string{"ZERO_DOCS_TOKEN": "test"},
		},
		"web": {
			Type:    "http",
			URL:     "https://example.com/mcp",
			Headers: map[string]string{"Authorization": "Bearer test"},
		},
		"events": {
			Type: "sse",
			URL:  "https://example.com/sse",
		},
		"disabled": {
			Type:     "stdio",
			Command:  "disabled-mcp",
			Disabled: true,
		},
	}}

	servers, err := NormalizeConfig(valid)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("servers = %#v, want disabled server skipped", servers)
	}
	if servers[0].Name != "docs" || servers[0].Identity == "" {
		t.Fatalf("docs server = %#v, want stable identity", servers[0])
	}
	if servers[1].Name != "events" || servers[2].Name != "web" {
		t.Fatalf("servers sorted by name = %#v", servers)
	}

	for _, tc := range []struct {
		name string
		cfg  config.MCPConfig
		want string
	}{
		{
			name: "stdio-without-command",
			cfg:  config.MCPConfig{Servers: map[string]config.MCPServerConfig{"docs": {Type: "stdio"}}},
			want: "requires command",
		},
		{
			name: "stdio-with-headers",
			cfg:  config.MCPConfig{Servers: map[string]config.MCPServerConfig{"docs": {Type: "stdio", Command: "docs-mcp", Headers: map[string]string{"Authorization": "Bearer test"}}}},
			want: "headers are only supported",
		},
		{
			name: "http-without-url",
			cfg:  config.MCPConfig{Servers: map[string]config.MCPServerConfig{"docs": {Type: "http"}}},
			want: "requires url",
		},
		{
			name: "http-with-env",
			cfg:  config.MCPConfig{Servers: map[string]config.MCPServerConfig{"docs": {Type: "http", URL: "https://example.com/mcp", Env: map[string]string{"TOKEN": "test"}}}},
			want: "env is only supported",
		},
		{
			name: "bad-url",
			cfg:  config.MCPConfig{Servers: map[string]config.MCPServerConfig{"docs": {Type: "sse", URL: "file:///tmp/mcp"}}},
			want: "http or https",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeConfig(tc.cfg)
			if err == nil {
				t.Fatal("NormalizeConfig() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

func TestNormalizeConfigFlagsUnconfiguredDefault(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"firecrawl": config.DefaultMCPServers()["firecrawl"],
		"web": {
			Type: "http",
			URL:  "https://example.com/mcp",
		},
	}}

	servers, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}

	byName := make(map[string]Server, len(servers))
	for _, server := range servers {
		byName[server.Name] = server
	}

	if !byName["firecrawl"].UnconfiguredDefault {
		t.Fatal("an untouched firecrawl default should be flagged UnconfiguredDefault")
	}
	if byName["web"].UnconfiguredDefault {
		t.Fatal("a server the user configured must not be flagged UnconfiguredDefault")
	}
}

func TestServerIdentityChangesWithTransportFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		first  config.MCPServerConfig
		second config.MCPServerConfig
	}{
		{
			name:   "command",
			first:  config.MCPServerConfig{Type: "stdio", Command: "docs-mcp"},
			second: config.MCPServerConfig{Type: "stdio", Command: "other-docs-mcp"},
		},
		{
			name:   "args",
			first:  config.MCPServerConfig{Type: "stdio", Command: "docs-mcp", Args: []string{"--one"}},
			second: config.MCPServerConfig{Type: "stdio", Command: "docs-mcp", Args: []string{"--two"}},
		},
		{
			name:   "env",
			first:  config.MCPServerConfig{Type: "stdio", Command: "docs-mcp", Env: map[string]string{"TOKEN": "one"}},
			second: config.MCPServerConfig{Type: "stdio", Command: "docs-mcp", Env: map[string]string{"TOKEN": "two"}},
		},
		{
			name:   "url",
			first:  config.MCPServerConfig{Type: "http", URL: "https://one.example/mcp"},
			second: config.MCPServerConfig{Type: "http", URL: "https://two.example/mcp"},
		},
		{
			name:   "headers",
			first:  config.MCPServerConfig{Type: "http", URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "Bearer one"}},
			second: config.MCPServerConfig{Type: "http", URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "Bearer two"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, err := NormalizeConfig(config.MCPConfig{Servers: map[string]config.MCPServerConfig{"docs": tc.first}})
			if err != nil {
				t.Fatal(err)
			}
			second, err := NormalizeConfig(config.MCPConfig{Servers: map[string]config.MCPServerConfig{"docs": tc.second}})
			if err != nil {
				t.Fatal(err)
			}
			if first[0].Identity == second[0].Identity {
				t.Fatalf("identity did not change when %s changed: %s", tc.name, first[0].Identity)
			}
		})
	}
}

func TestNormalizeConfigCarriesOAuth(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"remote": {
			Type: "http",
			URL:  "https://example.com/mcp",
			Auth: "oauth",
			OAuth: &config.MCPOAuthConfig{
				ClientID:      "client-123",
				Scopes:        []string{" read ", "write", "  "},
				TokenEndpoint: " https://example.com/token ",
			},
		},
	}}

	servers, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %#v", servers)
	}
	server := servers[0]
	if server.Auth != ServerAuthOAuth {
		t.Fatalf("auth = %q, want oauth", server.Auth)
	}
	if server.OAuth == nil {
		t.Fatal("OAuth = nil, want carried config")
	}
	if server.OAuth.ClientID != "client-123" {
		t.Fatalf("client id = %q", server.OAuth.ClientID)
	}
	if server.OAuth.TokenEndpoint != "https://example.com/token" {
		t.Fatalf("token endpoint = %q, want trimmed", server.OAuth.TokenEndpoint)
	}
	if len(server.OAuth.Scopes) != 2 || server.OAuth.Scopes[0] != "read" || server.OAuth.Scopes[1] != "write" {
		t.Fatalf("scopes = %#v, want trimmed and filtered", server.OAuth.Scopes)
	}
}

func TestNormalizeConfigRejectsUnsupportedAuth(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"remote": {Type: "http", URL: "https://example.com/mcp", Auth: "basic"},
	}}
	_, err := NormalizeConfig(cfg)
	if err == nil {
		t.Fatal("NormalizeConfig() error = nil, want unsupported auth error")
	}
	if !strings.Contains(err.Error(), "unsupported auth") {
		t.Fatalf("error = %q, want unsupported auth", err.Error())
	}
}

func TestNormalizeConfigRejectsAuthOnStdio(t *testing.T) {
	cfg := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"local": {Type: "stdio", Command: "local-mcp", Auth: "oauth"},
	}}
	_, err := NormalizeConfig(cfg)
	if err == nil {
		t.Fatal("NormalizeConfig() error = nil, want auth-on-stdio error")
	}
	if !strings.Contains(err.Error(), "auth is only supported") {
		t.Fatalf("error = %q, want auth-on-stdio error", err.Error())
	}
}

func TestCopyStringMapTrimsKeysAndPreservesValues(t *testing.T) {
	copied := copyStringMap(map[string]string{
		" TOKEN ": "  keep surrounding spaces  ",
		"   ":     "ignored",
	})
	if len(copied) != 1 {
		t.Fatalf("copied = %#v, want one trimmed key", copied)
	}
	if copied["TOKEN"] != "  keep surrounding spaces  " {
		t.Fatalf("copied[TOKEN] = %q, want value preserved verbatim", copied["TOKEN"])
	}
}
