package config

import (
	"reflect"
	"strings"
)

// DefaultMCPServers returns the MCP servers Zero ships ENABLED by default so
// web search and page fetching work out of the box with no setup and no API
// key. They are seeded before user/project config is merged (see ResolveMCP),
// so a user can override any field — for example add an API-key header to lift
// Exa's anonymous rate limit — or disable it entirely with
// `zero mcp disable <name>` (which writes `"disabled": true`).
//
// Exa's hosted MCP server works anonymously with rate limits. Users can add an
// Exa API key for higher limits.
func DefaultMCPServers() map[string]MCPServerConfig {
	return map[string]MCPServerConfig{
		"exa": {
			Type: "http",
			URL:  "https://mcp.exa.ai/mcp",
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

// IsUnconfiguredDefault reports whether server is one of Zero's built-in
// defaults that the user never wrote an entry for in their config — i.e. it is
// running with whatever Zero ships (e.g. keyless Exa, no credentials).
//
// Both conditions below must hold:
//   - !server.configured: the user's JSON never declared an object for this
//     server key at all (set by MCPServerConfig.UnmarshalJSON only when it
//     actually ran for this key). Any explicit action — including a
//     disable/enable toggle like `zero mcp enable exa` that leaves the
//     resolved value unchanged — sets configured, so it always counts as
//     user-configured, even though the value comparison below could not tell
//     the difference on its own.
//   - reflect.DeepEqual(def, server): the value still matches the default.
//     This is the fallback for callers that construct MCPServerConfig
//     directly rather than through the JSON/merge pipeline (server.configured
//     is then always false) — without it, any hand-built config with
//     different field values would be misreported as unconfigured.
//
// Callers use this to tell "server we turned on for the user" apart from
// "server the user configured themselves," e.g. to avoid warning loudly when
// an out-of-the-box default that was never given credentials fails to connect.
func IsUnconfiguredDefault(name string, server MCPServerConfig) bool {
	def, ok := DefaultMCPServers()[strings.TrimSpace(name)]
	return ok && !server.configured && reflect.DeepEqual(def, server)
}

// legacyDefaultMCPServers maps a built-in default Zero no longer ships to the
// default that replaced it (retired name -> successor name).
//
// A user who ran `zero mcp disable <old default>` made an explicit choice not
// to open that outbound connection. Renaming the default underneath them must
// not silently re-open it under the new name — and the reopened server would
// look like an untouched default, so even the startup warning stays quiet
// (see IsUnconfiguredDefault and issue #552).
var legacyDefaultMCPServers = map[string]string{
	"firecrawl": "exa",
}

// applyLegacyDefaultDisable carries a user's explicit disable of a retired
// built-in default onto the default that replaced it, so upgrading Zero never
// turns a connection back on that the user switched off.
//
// It runs immediately after the user layer merges (see ResolveMCP), which
// scopes it to user-level disables — the only scope whose disable is sticky.
// It applies only when the user never declared the successor themselves: an
// explicit `exa` entry wins whether it enables or disables. Because the
// carried-over disable is recorded as a user-level decision, the lower-trust
// project layer cannot lift it, while `zero mcp enable exa` still can (the CLI
// override scope merges with canReenable=true).
func applyLegacyDefaultDisable(cfg *MCPConfig) {
	for legacy, successor := range legacyDefaultMCPServers {
		retired, ok := cfg.Servers[legacy]
		if !ok || !retired.disabledSet || !retired.Disabled {
			continue
		}
		replacement, ok := cfg.Servers[successor]
		if !ok || replacement.configured {
			continue
		}
		replacement.Disabled = true
		replacement.disabledSet = true
		cfg.Servers[successor] = replacement
	}
}
