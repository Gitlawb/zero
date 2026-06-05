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

func TestServerIdentityChangesWithTransportFields(t *testing.T) {
	first, err := NormalizeConfig(config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp", Args: []string{"--one"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NormalizeConfig(config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp", Args: []string{"--two"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Identity == second[0].Identity {
		t.Fatalf("identity did not change when args changed: %s", first[0].Identity)
	}
}
