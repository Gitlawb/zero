package tui

import (
	"net/url"
	"strings"
)

type mcpSetupIntent struct {
	ServerName    string
	ServerType    string
	Endpoint      string
	SourceLabel   string
	SourceURL     string
	Prerequisites []string
}

func detectMCPSetupIntent(prompt string) (mcpSetupIntent, bool) {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" || !looksLikeMCPSetupRequest(normalized) {
		return mcpSetupIntent{}, false
	}
	if strings.Contains(normalized, "stitch.withgoogle.com/docs/mcp/setup") ||
		strings.Contains(normalized, "stitch mcp") ||
		strings.Contains(normalized, "google stitch") {
		return stitchMCPSetupIntent(prompt), true
	}
	for _, entry := range mcpMarketplaceCatalog {
		if !strings.Contains(normalized, strings.ToLower(entry.ID)) &&
			!strings.Contains(normalized, strings.ToLower(entry.Name)) {
			continue
		}
		intent, ok := mcpSetupIntentFromInstallCommand(entry.Name, entry.InstallCommand)
		if ok && promptContainsSetupEndpoint(prompt, intent.Endpoint) {
			return intent, true
		}
	}
	return mcpSetupIntent{}, false
}

func looksLikeMCPSetupRequest(normalized string) bool {
	if !strings.Contains(normalized, "mcp") && !strings.Contains(normalized, "model context protocol") {
		return false
	}
	for _, token := range []string{"add", "setup", "set up", "configure", "install", "connect", "enable"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func stitchMCPSetupIntent(prompt string) mcpSetupIntent {
	return mcpSetupIntent{
		ServerName:  "stitch",
		ServerType:  "stdio",
		Endpoint:    "npx -y @_davideast/stitch-mcp@latest proxy",
		SourceLabel: "Stitch MCP setup",
		SourceURL:   firstURLWithHost(prompt, "stitch.withgoogle.com"),
		Prerequisites: []string{
			"Google Cloud auth configured",
			"Stitch API enabled for the selected project",
			"GOOGLE_CLOUD_PROJECT available when the proxy runs",
		},
	}
}

func mcpSetupIntentFromInstallCommand(label string, command string) (mcpSetupIntent, bool) {
	command = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "/mcp"))
	args, err := splitMCPCommandArgs(command)
	if err != nil || len(args) < 2 || args[0] != "add" {
		return mcpSetupIntent{}, false
	}
	intent := mcpSetupIntent{ServerName: args[1], SourceLabel: label}
	for index := 2; index < len(args); index++ {
		switch {
		case args[index] == "--url" && index+1 < len(args):
			intent.ServerType = "http"
			intent.Endpoint = args[index+1]
			return intent, true
		case strings.HasPrefix(args[index], "--url="):
			intent.ServerType = "http"
			intent.Endpoint = strings.TrimPrefix(args[index], "--url=")
			return intent, true
		case args[index] == "--":
			if index+1 < len(args) {
				intent.ServerType = "stdio"
				intent.Endpoint = strings.Join(args[index+1:], " ")
				return intent, true
			}
		}
	}
	return mcpSetupIntent{}, false
}

func promptContainsSetupEndpoint(prompt string, endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return false
	}
	normalized := strings.ToLower(prompt)
	if !strings.Contains(normalized, strings.ToLower(parsed.Host)) {
		return false
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return true
	}
	return strings.Contains(normalized, strings.ToLower(strings.TrimRight(parsed.Path, "/")))
}

func firstURLWithHost(value string, host string) string {
	for _, field := range strings.Fields(value) {
		candidate := strings.Trim(field, `"'()[]<>.,;`)
		parsed, err := url.Parse(candidate)
		if err == nil && strings.EqualFold(parsed.Host, host) {
			return candidate
		}
	}
	return ""
}
