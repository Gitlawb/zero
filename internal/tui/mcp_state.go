package tui

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/tools"
)

type MCPStateOptions struct {
	Config          config.MCPConfig
	Registry        *tools.Registry
	PermissionStore *mcp.PermissionStore
	TokenStore      *mcp.TokenStore
	PermissionMode  string
	PromptCount     int
	DeniedCount     int
	// Skipped are the servers registration could not start. Registration is
	// best-effort so one unreachable server cannot stop Zero launching, which
	// means a failure is recorded here rather than returned. Without it this
	// panel reports configuration instead of reality.
	Skipped []mcp.SkippedServer
}

type mcpServerNamedTool interface {
	MCPServerName() string
}

const mcpRegistryToolPrefix = "mcp_"
const mcpDisplayRedacted = "[REDACTED]"

var mcpStateUnsafeToolNameChars = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func BuildMCPViewState(options MCPStateOptions) MCPViewState {
	toolViews := buildMCPToolViews(options.Config, options.Registry)
	toolCounts := make(map[string]int, len(toolViews))
	for _, tool := range toolViews {
		toolCounts[tool.ServerName]++
	}

	return MCPViewState{
		Servers:     buildMCPServerViews(options.Config, toolCounts, options.Skipped),
		Tools:       toolViews,
		Permissions: buildMCPPermissionSummary(options),
		OAuth:       buildMCPOAuthSummary(options.Config, options.TokenStore),
	}
}

func buildMCPServerViews(cfg config.MCPConfig, toolCounts map[string]int, skipped []mcp.SkippedServer) []MCPServerView {
	failures := make(map[string]error, len(skipped))
	for _, entry := range skipped {
		failures[entry.Name] = entry.Err
	}
	names := sortedMCPServerNames(cfg)
	servers := make([]MCPServerView, 0, len(names))
	for _, name := range names {
		raw := cfg.Servers[name]
		state := "enabled"
		message := ""
		switch {
		case raw.Disabled:
			// Disabled wins: the user turned it off, so it was never expected to
			// connect and reporting it as failed would be misleading.
			state = "disabled"
		default:
			if err, ok := failures[name]; ok {
				state = "failed"
				message = redaction.ErrorMessage(err, redaction.Options{
					ExtraSecretValues: mcpServerSecretValues(raw),
				})
				if strings.TrimSpace(message) == "" {
					message = "server did not start"
				}
			}
		}
		servers = append(servers, MCPServerView{
			Name:      name,
			Transport: mcpServerTransport(raw),
			State:     state,
			Target:    mcpServerTarget(raw),
			Auth:      strings.TrimSpace(raw.Auth),
			ToolCount: toolCounts[name],
			Error:     message,
		})
	}
	return servers
}

func buildMCPToolViews(cfg config.MCPConfig, registry *tools.Registry) []MCPToolView {
	if registry == nil {
		return nil
	}

	serverTokens := mcpServerTokenMap(cfg)
	registered := registry.All()
	views := make([]MCPToolView, 0, len(registered))
	for _, tool := range registered {
		registryName := strings.TrimSpace(tool.Name())
		if !strings.HasPrefix(registryName, mcpRegistryToolPrefix) {
			continue
		}

		serverName := mcpToolServerName(tool, registryName, serverTokens)
		toolName := mcpToolName(registryName, serverName)
		safety := tool.Safety()
		views = append(views, MCPToolView{
			ServerName:   serverName,
			Name:         toolName,
			RegistryName: registryName,
			SideEffect:   string(safety.SideEffect),
			Permission:   string(safety.Permission),
			Description:  tool.Description(),
		})
	}

	sort.SliceStable(views, func(left, right int) bool {
		if views[left].ServerName != views[right].ServerName {
			return views[left].ServerName < views[right].ServerName
		}
		if views[left].Name != views[right].Name {
			return views[left].Name < views[right].Name
		}
		return views[left].RegistryName < views[right].RegistryName
	})
	return views
}

func buildMCPPermissionSummary(options MCPStateOptions) MCPPermissionSummary {
	summary := MCPPermissionSummary{
		Mode:        strings.TrimSpace(options.PermissionMode),
		PromptCount: options.PromptCount,
		DeniedCount: options.DeniedCount,
	}
	if options.PermissionStore == nil {
		return summary
	}

	grants, err := options.PermissionStore.List()
	if err != nil {
		return summary
	}
	summary.GrantCount = len(grants)
	summary.Grants = make([]MCPPermissionGrantView, 0, len(grants))
	for _, grant := range grants {
		switch grant.Scope {
		case mcp.ScopeServer:
			summary.ServerGrants++
		case mcp.ScopeTool:
			summary.ToolGrants++
		}
		summary.Grants = append(summary.Grants, MCPPermissionGrantView{
			Target:     mcpPermissionTarget(grant),
			Autonomy:   string(grant.MaxAutonomy),
			ApprovedAt: grant.ApprovedAt,
		})
	}
	return summary
}

func buildMCPOAuthSummary(cfg config.MCPConfig, tokenStore *mcp.TokenStore) MCPOAuthSummary {
	configured := make(map[string]bool)
	for name, server := range cfg.Servers {
		if strings.EqualFold(strings.TrimSpace(server.Auth), mcp.ServerAuthOAuth) || server.OAuth != nil {
			configured[name] = true
		}
	}

	statuses := map[string]mcp.TokenStatus{}
	if tokenStore != nil {
		if stored, err := tokenStore.Status(); err == nil {
			for _, status := range stored {
				statuses[status.ServerName] = status
			}
		}
	}

	names := make([]string, 0, len(configured)+len(statuses))
	seen := make(map[string]struct{}, len(configured)+len(statuses))
	for name := range configured {
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for name := range statuses {
		if _, ok := seen[name]; ok {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]MCPOAuthServerView, 0, len(names))
	for _, name := range names {
		status := statuses[name]
		servers = append(servers, MCPOAuthServerView{
			ServerName:      name,
			Configured:      configured[name],
			HasToken:        status.HasToken,
			HasRefreshToken: status.HasRefreshToken,
			TokenType:       status.TokenType,
			Scopes:          append([]string{}, status.Scopes...),
			ExpiresAt:       status.ExpiresAt,
			Expired:         status.Expired,
		})
	}
	return MCPOAuthSummary{Servers: servers}
}

func sortedMCPServerNames(cfg config.MCPConfig) []string {
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func mcpServerTransport(server config.MCPServerConfig) string {
	transport := strings.ToLower(strings.TrimSpace(server.Type))
	if transport != "" {
		return transport
	}
	if strings.TrimSpace(server.URL) != "" {
		return string(mcp.ServerTypeHTTP)
	}
	return string(mcp.ServerTypeStdio)
}

func mcpServerTarget(server config.MCPServerConfig) string {
	switch mcpServerTransport(server) {
	case string(mcp.ServerTypeHTTP), string(mcp.ServerTypeSSE):
		parts := []string{}
		if url := strings.TrimSpace(server.URL); url != "" {
			parts = append(parts, redactMCPDisplayURL(url))
		}
		if headers := redactedStringMap(server.Headers); headers != "" {
			parts = append(parts, "headers", headers)
		}
		return strings.Join(parts, " ")
	default:
		parts := []string{}
		if command := strings.TrimSpace(server.Command); command != "" {
			parts = append(parts, command)
		}
		parts = append(parts, redactedCommandArgs(server.Args)...)
		if env := redactedStringMap(server.Env); env != "" {
			parts = append(parts, "env", env)
		}
		return strings.Join(parts, " ")
	}
}

func redactedStringMap(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+mcpDisplayRedacted)
	}
	return strings.Join(parts, " ")
}

func redactedCommandArgs(values []string) []string {
	trimmed := make([]string, 0, len(values))
	redactNext := false
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			if redactNext {
				if looksLikeMCPDisplayURLValue(value) {
					trimmed = append(trimmed, redactMCPDisplayURL(value))
				} else {
					trimmed = append(trimmed, mcpDisplayRedacted)
				}
				redactNext = false
				continue
			}
			if key, rest, ok := strings.Cut(value, "="); ok {
				switch {
				case isSensitiveMCPDisplayKey(key):
					trimmed = append(trimmed, key+"="+mcpDisplayRedacted)
					continue
				case looksLikeMCPDisplayURLValue(rest):
					trimmed = append(trimmed, key+"="+redactMCPDisplayURL(rest))
					continue
				}
			}
			if isSensitiveMCPDisplayFlag(value) {
				trimmed = append(trimmed, value)
				redactNext = true
				continue
			}
			if looksLikeMCPDisplayURLValue(value) {
				trimmed = append(trimmed, redactMCPDisplayURL(value))
				continue
			}
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}

func redactMCPDisplayURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fallbackRedactMCPDisplayURL(raw)
	}
	if parsed.User != nil {
		parsed.User = nil
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = redactMCPDisplayRawQuery(parsed.RawQuery)
	}
	if parsed.Fragment != "" {
		parsed.Fragment = redactMCPDisplayRawQuery(parsed.Fragment)
	}
	out := parsed.String()
	if strings.TrimSpace(out) == "" {
		return fallbackRedactMCPDisplayURL(raw)
	}
	return strings.ReplaceAll(out, "%5BREDACTED%5D", mcpDisplayRedacted)
}

func redactMCPDisplayRawQuery(rawQuery string) string {
	parts := strings.Split(rawQuery, "&")
	for index, part := range parts {
		if part == "" {
			continue
		}
		key, _, hasValue := strings.Cut(part, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			decodedKey = key
		}
		if !isSensitiveMCPDisplayKey(decodedKey) {
			continue
		}
		if hasValue {
			parts[index] = key + "=" + mcpDisplayRedacted
		} else {
			parts[index] = key
		}
	}
	return strings.Join(parts, "&")
}

func looksLikeMCPDisplayURLValue(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	return strings.Contains(value, "://") ||
		strings.HasPrefix(lower, "http:") ||
		strings.HasPrefix(lower, "https:") ||
		strings.Contains(value, "?") ||
		strings.Contains(value, "#")
}

func fallbackRedactMCPDisplayURL(raw string) string {
	out := strings.TrimSpace(raw)
	if out == "" {
		return ""
	}
	if schemeIndex := strings.Index(out, "://"); schemeIndex >= 0 {
		authorityStart := schemeIndex + len("://")
		authorityEnd := len(out)
		for _, marker := range []string{"/", "?", "#"} {
			if index := strings.Index(out[authorityStart:], marker); index >= 0 && authorityStart+index < authorityEnd {
				authorityEnd = authorityStart + index
			}
		}
		if at := strings.LastIndex(out[authorityStart:authorityEnd], "@"); at >= 0 {
			out = out[:authorityStart] + out[authorityStart+at+1:]
		}
	}
	if head, fragment, ok := strings.Cut(out, "#"); ok {
		fragment = redactMCPDisplayRawQuery(fragment)
		out = head + "#" + fragment
	}
	if head, query, ok := strings.Cut(out, "?"); ok {
		query = redactMCPDisplayRawQuery(query)
		out = head + "?" + query
	}
	return out
}

func isSensitiveMCPDisplayFlag(value string) bool {
	value = strings.TrimLeft(strings.ToLower(strings.TrimSpace(value)), "-")
	if key, _, ok := strings.Cut(value, "="); ok {
		value = key
	}
	return isSensitiveMCPDisplayKey(value)
}

func isSensitiveMCPDisplayKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(strings.TrimLeft(key, "-")))
	key = strings.ReplaceAll(key, "-", "_")
	if key == "key" {
		return true
	}
	for _, token := range []string{"token", "secret", "password", "passwd", "api_key", "apikey", "access_key", "auth", "credential"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func mcpServerTokenMap(cfg config.MCPConfig) map[string]string {
	tokens := make(map[string]string, len(cfg.Servers))
	for name := range cfg.Servers {
		tokens[mcpStateSanitizeToolNamePart(name)] = name
	}
	return tokens
}

func mcpToolServerName(tool tools.Tool, registryName string, serverTokens map[string]string) string {
	if named, ok := tool.(mcpServerNamedTool); ok {
		if serverName := strings.TrimSpace(named.MCPServerName()); serverName != "" {
			return serverName
		}
	}

	rest, ok := strings.CutPrefix(registryName, mcpRegistryToolPrefix)
	if !ok {
		return ""
	}
	tokens := make([]string, 0, len(serverTokens))
	for token := range serverTokens {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(left, right int) bool {
		if len(tokens[left]) != len(tokens[right]) {
			return len(tokens[left]) > len(tokens[right])
		}
		return tokens[left] < tokens[right]
	})
	for _, token := range tokens {
		if strings.HasPrefix(rest, token+"_") {
			return serverTokens[token]
		}
	}
	if server, _, ok := strings.Cut(rest, "_"); ok {
		return server
	}
	return ""
}

func mcpToolName(registryName string, serverName string) string {
	rest, ok := strings.CutPrefix(registryName, mcpRegistryToolPrefix)
	if !ok {
		return registryName
	}
	if serverName != "" {
		prefix := mcpStateSanitizeToolNamePart(serverName) + "_"
		if strings.HasPrefix(rest, prefix) {
			return strings.TrimPrefix(rest, prefix)
		}
	}
	if _, name, ok := strings.Cut(rest, "_"); ok {
		return name
	}
	return rest
}

func mcpStateSanitizeToolNamePart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = mcpStateUnsafeToolNameChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "server"
	}
	return value
}

func mcpPermissionTarget(grant mcp.PermissionGrant) string {
	serverName := strings.TrimSpace(grant.ServerName)
	if grant.Scope == mcp.ScopeTool {
		toolName := strings.TrimSpace(grant.ToolName)
		if serverName == "" {
			return toolName
		}
		if toolName == "" {
			return serverName + "/*"
		}
		return serverName + "/" + toolName
	}
	if serverName == "" {
		return "*"
	}
	return serverName + "/*"
}

// mcpServerSecretValues collects the configured values a failing server could
// echo back at us.
//
// The generic redaction patterns match shapes they recognise, like an
// `Authorization:` header or a key-looking token. A remote MCP server is
// configured with ARBITRARY headers, so a value under a name nobody can predict
// (`X-Workspace-Credential`, say) matches no pattern at all. A server that
// returns the request it failed on then puts that value straight into
// MCPServerView.Error, which this PR newly renders in both /mcp surfaces and the
// session transcript. Passing the values themselves redacts by equality rather
// than by shape, so the name does not have to be guessable.
//
// It also removes the dependence on the syntactic matcher, which terminal
// control bytes can split before the later sanitizer strips them.
//
// Short values are skipped. A configured "1" or "true" is not a credential, and
// redacting it by equality would punch holes through unrelated text, which is
// its own way of making an error message useless.
func mcpServerSecretValues(raw config.MCPServerConfig) []string {
	const shortestSecret = 8
	values := make([]string, 0, len(raw.Headers)+len(raw.Env)+2)
	add := func(value string) {
		if trimmed := strings.TrimSpace(value); len(trimmed) >= shortestSecret {
			values = append(values, trimmed)
		}
	}
	for _, value := range raw.Headers {
		add(value)
	}
	// Env carries the same risk for a stdio server: the child is launched with
	// these, and a startup failure often reports the environment it was given.
	for _, value := range raw.Env {
		add(value)
	}
	add(raw.Auth)
	if raw.OAuth != nil {
		add(raw.OAuth.ClientSecret)
	}
	return values
}
