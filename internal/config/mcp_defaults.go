package config

import (
	"reflect"
	"strings"
)

// DefaultMCPServers returns the MCP servers Zero ships ENABLED by default so web
// search and scraping work out of the box with no setup and no API key. They are
// seeded before user/project config is merged (see ResolveMCP), so a user can
// override any field — for example point firecrawl at a self-hosted instance, or
// add an API-key header to lift the free-tier limit — or disable it entirely with
// `zero mcp disable <name>` (which writes `"disabled": true`).
//
// Keyless Firecrawl routes requests through firecrawl.dev (1,000 free credits per
// month, no account). Self-host Firecrawl (AGPL-3.0) for unlimited and private
// use. Zero only calls it over the network, so Firecrawl's license never reaches
// into Zero's own code.
func DefaultMCPServers() map[string]MCPServerConfig {
	return map[string]MCPServerConfig{
		"firecrawl": {
			Type: "http",
			URL:  "https://mcp.firecrawl.dev/v2/mcp",
		},
	}
}

// IsDefaultMCPServer reports whether name is one of Zero's built-in default MCP
// servers. The config commands use it so a default can be disabled/enabled even
// though it is not written to the user's config file until overridden.
func IsDefaultMCPServer(name string) bool {
	_, ok := DefaultMCPServers()[strings.TrimSpace(name)]
	return ok
}

// IsUnconfiguredDefault reports whether server is exactly the built-in default
// for name — i.e. the user never wrote anything for this server into their
// config, so it is running with whatever Zero ships (e.g. keyless Firecrawl,
// no credentials). mergeMCPServer only overwrites fields the user actually set,
// so an untouched default survives merge byte-identical to DefaultMCPServers().
// Callers use this to tell "server we turned on for the user" apart from
// "server the user configured themselves," e.g. to avoid warning loudly when
// an out-of-the-box default that was never given credentials fails to connect.
func IsUnconfiguredDefault(name string, server MCPServerConfig) bool {
	def, ok := DefaultMCPServers()[strings.TrimSpace(name)]
	return ok && reflect.DeepEqual(def, server)
}
