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

// shortestMCPSecret is the floor for treating a configured value as a credential
// to strike out of an error message. A configured "1" or "true" is not a secret,
// and redacting it by equality would punch holes through unrelated text, which is
// its own way of making an error useless.
const shortestMCPSecret = 8

func BuildMCPViewState(options MCPStateOptions) MCPViewState {
	toolViews := buildMCPToolViews(options.Config, options.Registry)
	toolCounts := make(map[string]int, len(toolViews))
	for _, tool := range toolViews {
		toolCounts[tool.ServerName]++
	}

	return MCPViewState{
		Servers:     buildMCPServerViews(options.Config, toolCounts, options.Skipped, options.TokenStore),
		Tools:       toolViews,
		Permissions: buildMCPPermissionSummary(options),
		OAuth:       buildMCPOAuthSummary(options.Config, options.TokenStore),
	}
}

func buildMCPServerViews(cfg config.MCPConfig, toolCounts map[string]int, skipped []mcp.SkippedServer, tokenStore *mcp.TokenStore) []MCPServerView {
	failures := make(map[string]error, len(skipped))
	for _, entry := range skipped {
		failures[entry.Name] = entry.Err
	}
	// Read the stored bearers ONCE. Every load re-reads and re-parses the whole
	// store file, and the material is the same for every row anyway. nil is the
	// normal case rather than a test-only one: startup soft-fails the token store
	// to nil with a warning, and SecretValues is nil-safe for exactly that.
	tokenSecrets := tokenStore.SecretValues()
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
				message = redactMCPFailureReason(err, raw, tokenSecrets)
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

// redactMCPFailureReason turns a failed server's error into text safe to render
// AND to persist, since the panel's output goes into the transcript.
//
// The second pass is the point. Redaction matches a configured value literally,
// and the reason is written by the server, which is free to insert a byte into
// the middle of a credential it echoes back: `wk-live-\x1b[31m4f9c2b7ae1d8` is
// not equal to the configured `wk-live-4f9c2b7ae1d8`, so the first pass sees
// nothing to redact. The display sanitizer downstream then removes the escape
// WITHOUT leaving a gap, reassembling the intact credential on screen. Every
// control byte it drops rejoins the same way, so this is not specific to ANSI.
//
// So the text is normalized to what the reader will actually see, and redaction
// is run again against that. Normalizing before the first pass instead would
// work equally well; running it after keeps redaction.ErrorMessage's handling of
// a nil or wrapped error exactly as it was.
//
// The stored bearer goes in alongside the configured values because a failed
// OAuth server can echo the token in its error body, and no pattern can
// recognize an opaque token by shape.
func redactMCPFailureReason(err error, raw config.MCPServerConfig, tokenSecrets []string) string {
	secrets := mcpServerSecretValues(raw)
	// NO READABILITY FLOOR ON KNOWN CREDENTIAL MATERIAL. The floor exists for
	// ambiguous configuration strings, where a short value is more likely to be a
	// hostname fragment than a secret and blanket redaction would eat the
	// diagnostic. A stored access token, refresh token or client secret is not
	// ambiguous: it is a credential by provenance, whatever its length. OAuth
	// bearer syntax is opaque, the store accepts any non-empty value, and a failed
	// server can echo a six-character token under an arbitrary field name where no
	// pattern will recognise it.
	for _, value := range tokenSecrets {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			secrets = append(secrets, trimmed)
		}
	}
	options := redaction.Options{ExtraSecretValues: secrets}
	message := redaction.ErrorMessage(err, options)
	return redaction.RedactString(stripTerminalRejoiners(message), options)
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
	values := make([]string, 0, len(raw.Headers)+len(raw.Env)+len(raw.Args)+2)
	add := func(value string) {
		values = append(values, credentialCandidates(value)...)
	}
	for _, value := range raw.Headers {
		add(value)
	}
	// Env carries the same risk for a stdio server: the child is launched with
	// these, and a startup failure often reports the environment it was given.
	for _, value := range raw.Env {
		add(value)
	}
	// Args carry it too, and more visibly: a stdio child that rejects its own
	// invocation usually prints that invocation back, and connectStdio appends
	// the captured stderr to the initialization error this panel renders.
	for _, value := range sensitiveMCPArgValues(raw.Args) {
		add(value)
	}
	add(raw.Auth)
	if raw.OAuth != nil {
		add(raw.OAuth.ClientSecret)
	}
	return values
}

// credentialCandidates returns the configured value plus the shorter strings a
// server might echo INSTEAD of it.
//
// Redaction is equality on the whole configured value, and the credential is
// usually not the whole value. Zero's own documented config spells an
// authenticated server as `"Authorization": "Bearer sk-live-..."`, so a server
// that quotes back only the credential, without the scheme word, matches
// nothing and prints. The same shape arrives through a composite argument such
// as `--header "Authorization: Bearer sk-live-..."`, where the configured
// string carries a header name as well.
//
// So each tail after a space or a colon is offered as its own candidate, which
// covers "<scheme> <credential>" and "<header>: <scheme> <credential>" without
// needing to know the scheme vocabulary. The floor discards the fragments too
// short to be a credential, which is what stops "Bearer" or a header name from
// entering the set and blanking those words out of unrelated text.
//
// A value with no separator yields itself and nothing else, so the common case
// costs one entry, as before.
func credentialCandidates(value string) []string {
	candidates := make([]string, 0, 3)
	remainder := strings.TrimSpace(value)
	for {
		if len(remainder) >= shortestMCPSecret {
			candidates = append(candidates, remainder)
		}
		index := strings.IndexAny(remainder, " :")
		if index < 0 {
			return candidates
		}
		next := strings.TrimSpace(remainder[index+1:])
		if next == remainder {
			// Defensive: without this a value of only separators could not shrink
			// and the loop would not terminate.
			return candidates
		}
		remainder = next
	}
}

// sensitiveMCPArgValues returns the VALUES a stdio server is launched with
// behind a sensitive flag, for redaction. redactedCommandArgs answers a
// different question a few lines up: it returns display strings, so the secret
// is already gone by the time it returns and nothing there can be reused here.
// Only the predicates are shared, deliberately, so the two cannot drift apart
// about which flags are sensitive.
//
// Values are trimmed because the child is launched with trimmed args
// (internal/mcp/config.go), and it can only echo back what it was given. An
// untrimmed candidate would be compared against a string the child never saw.
//
// The predicate matches on substrings, so a value behind a flag like
// --auth-type is collected as well. Redacting an enum out of a message costs
// some readability; not redacting a credential costs the credential, so the
// collection is deliberately the wider of the two.
func sensitiveMCPArgValues(args []string) []string {
	values := make([]string, 0, len(args))
	// A candidate that is itself a flag is never a value. Reading one would put
	// something like "--verbose" into the redaction set, and every message
	// mentioning it would lose the word.
	isFlag := func(value string) bool { return strings.HasPrefix(value, "-") }
	pending := false
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			// Blanks are skipped rather than consumed, matching the display pass.
			// Consuming one would take the blank as the value and leave the real
			// secret in the next position unredacted.
			continue
		}
		if pending {
			pending = false
			if !isFlag(arg) {
				values = append(values, arg)
				continue
			}
			// Otherwise fall through: this argument is a flag in its own right.
		}
		// Cut at the FIRST "=" and keep the whole tail, so base64 padding and
		// values that themselves contain "=" survive intact.
		if key, rest, ok := strings.Cut(arg, "="); ok && isSensitiveMCPDisplayKey(key) {
			values = append(values, strings.TrimSpace(rest))
			continue
		}
		// A flag and its value packed into a single argument. The display pass
		// gets this shape wrong in the other direction (it prints the whole thing
		// verbatim, then redacts the following, unrelated argument), so this
		// cannot be delegated to it.
		if flag, rest, ok := strings.Cut(arg, " "); ok && isSensitiveMCPDisplayFlag(flag) {
			values = append(values, strings.TrimSpace(rest))
			continue
		}
		// Only an actual FLAG claims the next argument. isSensitiveMCPDisplayFlag
		// strips leading dashes before matching, so it says yes to a bare
		// positional word too, and the documented GitHub server config
		// (`-e GITHUB_PERSONAL_ACCESS_TOKEN ghcr.io/github/github-mcp-server`)
		// would put the IMAGE NAME into the redaction set: the pull failure would
		// then lose the one string that explains it. A positional argument is not
		// a flag and does not introduce a value.
		if isFlag(arg) && isSensitiveMCPDisplayFlag(arg) {
			// The value is the next argument, if there is one. A sensitive flag in
			// the last position simply has nothing to redact.
			pending = true
		}
	}
	return values
}
