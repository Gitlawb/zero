package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

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
	// SkippedCredentials fingerprints the credential material that existed when
	// Skipped was captured. See mcpCredentialFingerprint: an observation retains
	// a RAW error, and redaction happens at render, so the safety of fixed text
	// would otherwise depend on mutable state read later. Empty means the caller
	// did not record one, and the check is skipped.
	SkippedCredentials string
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
		Servers:     buildMCPServerViews(options.Config, toolCounts, options.Skipped, options.TokenStore, options.SkippedCredentials),
		Tools:       toolViews,
		Permissions: buildMCPPermissionSummary(options),
		OAuth:       buildMCPOAuthSummary(options.Config, options.TokenStore),
	}
}

func buildMCPServerViews(cfg config.MCPConfig, toolCounts map[string]int, skipped []mcp.SkippedServer, tokenStore *mcp.TokenStore, capturedCredentials string) []MCPServerView {
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
	for _, rawName := range names {
		raw := cfg.Servers[rawName]
		// ONE IDENTITY, and it is the registry's. mcp.normalizeServer trims the
		// config-map key before anything downstream sees it, so a server
		// configured as " docs " is registered, recorded in SkippedServer.Name, and
		// counted in toolCounts as "docs". This loop was iterating the RAW map keys
		// and looking failures up with them, so failures[" docs "] missed and the
		// entry rendered as " docs " enabled: a server that never started, shown as
		// running, which is the one thing this panel exists to prevent. The tool
		// count was lost the same way.
		//
		// Trimmed here to match normalizeServer exactly. The canonical spelling is
		// also what gets displayed, because that is the name the server actually
		// has everywhere else in the process.
		name := strings.TrimSpace(rawName)
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
				if staleMCPObservation(capturedCredentials, tokenSecrets) {
					message = mcpStaleObservationReason
				}

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
	// BOUNDED AT THE RAW INGRESS, which is inside ErrorMessage rather than around
	// it. See boundMCPFailureError: wrapping the outside of that call left the
	// full server-controlled value going through every redaction pass first, so
	// the work scaled with the attacker's input instead of with the cap.
	bounded, truncated := boundMCPFailureError(err)
	rendered := redaction.RedactString(stripTerminalRejoiners(redaction.ErrorMessage(bounded, options)), options)
	// AND THE CUT ITSELF CAN MANUFACTURE A LEAK. Everything above matches whole
	// values, so a credential the bound sliced in half matches nothing and its
	// surviving prefix is ordinary text. The fixed overlap makes that need a
	// secret longer than the window, and nothing caps a configured header, URL,
	// environment or stored token value, so "longer than the window" is a
	// configuration away rather than impossible. A server can also spend the raw
	// budget on control sequences that later vanish, putting the start of the
	// credential right at the cut.
	//
	// Only the TAIL can be a partial, because only the final cut splits anything,
	// so this looks at the tail alone and drops any run that is a prefix of a
	// configured secret. It costs one comparison per secret against a bounded
	// window and does not care how long the secret is, which is the property the
	// overlap could not give.
	if truncated {
		rendered = dropTrailingSecretPrefix(rendered, secrets)
	}
	return rendered
}

// dropTrailingSecretPrefix removes a trailing run that is the beginning of a
// configured secret.
//
// Applied to the FINAL text, after both redaction passes and after the terminal
// rejoiners are gone, because that is the string a reader sees and the only one
// whose tail is the real tail.
func dropTrailingSecretPrefix(rendered string, secrets []string) string {
	cut := len(rendered)
	for _, secret := range secrets {
		size := longestPrefixSuffix(secret, rendered)
		if !recoverableSecretPrefix(size, len(secret)) {
			continue
		}
		if start := len(rendered) - size; start < cut {
			cut = start
		}
	}
	if cut == len(rendered) {
		return rendered
	}
	return strings.TrimRight(rendered[:cut], " 	")
}

// maxMCPSecretMatchWindow is the FIXED overlap kept past the display cap so a
// configured credential straddling the cut is still matched whole.
//
// Fixed, deliberately. The previous version sized it to the longest secret,
// which made the real budget "the cap plus whatever the largest configured value
// happens to be". Configured values and the stored token enumeration have no
// size limit, so a two-megabyte credential raised the retained error to 65546
// bytes against a nominal cap of 16384. A bound the other side can widen is not
// a bound.
//
// A credential longer than this window can still straddle the cut and leave a
// prefix. That is a stated limit rather than an oversight: four kilobytes is far
// past any real bearer token, and the alternative is the unbounded margin this
// replaces.
const maxMCPSecretMatchWindow = 4 << 10

// boundMCPFailureError caps the RAW, server-controlled error before redaction or
// terminal normalization touches it.
//
// The bound used to sit outside redaction.ErrorMessage, which is the innermost
// call, so the whole value went through the exact-value replacements and the
// regular-expression passes first and only the leftovers were trimmed. Measured
// on the unfixed path, the work scaled with the input rather than with the cap:
//
//	input 1 KiB   ->    1ms,    0.2 MB allocated
//	input 1 MiB   ->  248ms,     36 MB allocated
//	input 8 MiB   ->  2.21s,    286 MB allocated   (rendered panel: 400 runes)
//
// A remote MCP chooses that input by putting an oversized tool name into a
// conflict error, and every open or refresh of /mcp paid for it again.
//
// A short error is returned untouched, so ErrorMessage keeps its handling of nil
// and wrapped errors for the overwhelmingly common case. Only an oversized one
// is flattened, and flattening an eight-megabyte error is the entire point.
func boundMCPFailureError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	limit := maxMCPReasonRawLen + maxMCPSecretMatchWindow
	message := err.Error()
	if len(message) <= limit {
		return err, false
	}
	return errors.New(boundMCPRawText(message, limit)), true
}

// boundMCPRawText truncates rune-safely, so nothing downstream sees a
// replacement character this function produced.
func boundMCPRawText(message string, limit int) string {
	if len(message) <= limit {
		return message
	}
	message = message[:limit]
	for len(message) > 0 {
		decoded, width := utf8.DecodeLastRuneInString(message)
		if decoded != utf8.RuneError || width > 1 {
			break
		}
		message = message[:len(message)-1]
	}
	return message
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

// redactMCPHeaderValue redacts a "<name>: <value>" header argument for DISPLAY,
// keeping the name. The name is what tells an operator which header was
// rejected; the value is the credential.
func redactMCPHeaderValue(value string) string {
	if name, rest, ok := strings.Cut(value, ":"); ok && strings.TrimSpace(rest) != "" {
		separator := ": "
		if !strings.HasPrefix(rest, " ") {
			separator = ":"
		}
		return name + separator + mcpDisplayRedacted
	}
	return mcpDisplayRedacted
}

func redactedCommandArgs(values []string) []string {
	trimmed := make([]string, 0, len(values))
	redactNext := false
	// THE TARGET ROW SITS UNDER THE ERROR ROW. Redacting the failure reason while
	// printing the same credential verbatim one line lower gives it back with the
	// other hand, and this row is persisted to the transcript too.
	redactNextHeader := false
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			if redactNext {
				wasHeader := redactNextHeader
				redactNext = false
				redactNextHeader = false
				switch {
				case wasHeader:
					trimmed = append(trimmed, redactMCPHeaderValue(value))
				case looksLikeMCPDisplayURLValue(value):
					trimmed = append(trimmed, redactMCPDisplayURL(value))
				default:
					trimmed = append(trimmed, mcpDisplayRedacted)
				}
				continue
			}
			if key, rest, ok := strings.Cut(value, "="); ok {
				switch {
				case isMCPHeaderFlag(key):
					trimmed = append(trimmed, key+"="+redactMCPHeaderValue(rest))
					continue
				case isSensitiveMCPDisplayKey(key):
					trimmed = append(trimmed, key+"="+mcpDisplayRedacted)
					continue
				case looksLikeMCPDisplayURLValue(rest):
					trimmed = append(trimmed, key+"="+redactMCPDisplayURL(rest))
					continue
				}
			}
			if flag, rest, ok := strings.Cut(value, " "); ok && isMCPHeaderFlag(flag) {
				trimmed = append(trimmed, flag+" "+redactMCPHeaderValue(rest))
				continue
			}
			if flag, carried, ok := mcpHeaderArgument(value); ok {
				if carried != "" {
					// Attached "-HName: value": the credential is in THIS
					// argument, so there is no next one to claim.
					trimmed = append(trimmed, flag+redactMCPHeaderValue(carried))
					continue
				}
				trimmed = append(trimmed, value)
				redactNext = true
				redactNextHeader = true
				continue
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
	if path := parsed.EscapedPath(); path != "" {
		// The Target row renders alongside the Error, so a path credential shown
		// here defeats redacting it there.
		parsed.RawPath = redactMCPDisplayPath(path)
		parsed.Path, _ = url.PathUnescape(parsed.RawPath)
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

// opaqueURLPathSegments returns the path segments long enough to be a credential
// rather than a route.
func opaqueURLPathSegments(escapedPath string) []string {
	segments := strings.Split(escapedPath, "/")
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		if len(segment) < shortestMCPSecret {
			continue
		}
		out = append(out, segment)
	}
	return out
}

// redactMCPDisplayPath replaces opaque segments and keeps the route shape, so
// the operator can still tell which endpoint failed.
func redactMCPDisplayPath(escapedPath string) string {
	segments := strings.Split(escapedPath, "/")
	for index, segment := range segments {
		if len(segment) < shortestMCPSecret {
			continue
		}
		segments[index] = mcpDisplayRedacted
	}
	return strings.Join(segments, "/")
}

func redactMCPDisplayRawQuery(rawQuery string) string {
	parts := strings.Split(rawQuery, "&")
	for index, part := range parts {
		if part == "" {
			continue
		}
		key, rawValue, hasValue := strings.Cut(part, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			decodedKey = key
		}
		// THE PARAMETER NAME IS THE OPERATOR'S TO CHOOSE, so a name-based rule only
		// covers the names somebody thought of. `?workspace=<token>` carried a
		// credential straight into this row while `?api_key=` next to it was
		// redacted, and this Target row sits directly under the Error row that IS
		// redacted, on the same panel, so the panel gave the value back with one
		// hand.
		//
		// Length decides instead, on the same floor the candidate collector uses:
		// a value long enough to be a credential is redacted, and `v=1` or
		// `mode=sse` stays readable so the row still describes the endpoint.
		sensitive := isSensitiveMCPDisplayKey(decodedKey)
		if !sensitive && hasValue {
			decodedValue, valueErr := url.QueryUnescape(rawValue)
			if valueErr != nil {
				decodedValue = rawValue
			}
			sensitive = len(strings.TrimSpace(decodedValue)) >= shortestMCPSecret
		}
		if !sensitive {
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
	// AMBIGUOUS values go through credentialCandidates, whose shortestMCPSecret
	// floor keeps ordinary short configuration ("v=1", "mode=sse") out of the
	// redaction set. That floor is a readability trade-off, and it is only
	// defensible while the value might not be a credential at all.
	add := func(value string) {
		values = append(values, credentialCandidates(value)...)
	}
	// KNOWN values skip the floor. Provenance has already settled that these are
	// secret: a field named ClientSecret, or the value of a credential-bearing
	// flag that sensitiveMCPArgValues identified as such. Routing them through
	// the ambiguity heuristic discarded anything under eight bytes, so a
	// six-byte client secret echoed back in an error_description, or a short
	// value passed through --api-key, survived into the panel and the
	// transcript. Stored tokens are already protected at any non-empty length;
	// this makes the configured sources agree with them.
	//
	// The candidate walk still runs, so the "<scheme> <credential>" shape is
	// split as before; what changes is that the whole value is kept regardless
	// of length.
	addKnown := func(value string) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values = append(values, trimmed)
		}
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
		addKnown(value)
	}
	// THE ENDPOINT ITSELF CARRIES CREDENTIALS. HTTP and SSE send the configured
	// URL verbatim, and it accepts both userinfo and arbitrary query keys, so
	// `https://host/mcp?workspace=opaque-token` puts a credential in a parameter
	// whose NAME the operator chose. The generic query redaction downstream only
	// recognises conventional key names, so "workspace" walks straight through it,
	// and equality redaction cannot help because nothing told it the value. A
	// server that echoes its own endpoint in a failure body then reaches this
	// panel and the transcript with the token intact.
	//
	// Collected as exact values here rather than by widening the sensitive-key
	// list, which would still only cover names somebody thought of.
	for _, value := range mcpURLSecretValues(raw.URL) {
		add(value)
	}
	addKnown(raw.Auth)
	if raw.OAuth != nil {
		addKnown(raw.OAuth.ClientSecret)
		// THE OAUTH ENDPOINTS ARE REACHED DURING STARTUP TOO, and they carry
		// credentials in exactly the way the main URL does. With a stored token a
		// 401 from the server triggers a refresh, oauth.PostToken then posts to
		// TokenEndpoint, and a dial or TLS failure comes back wrapped in a
		// *url.Error that retains the path and query. Collecting only from
		// raw.URL left an accepted endpoint such as
		// "https://auth.invalid/token?workspace=<opaque>" outside the candidate
		// set, so the value reached the panel and the transcript intact.
		for _, endpoint := range []string{
			raw.OAuth.TokenEndpoint,
			raw.OAuth.AuthorizationEndpoint,
			raw.OAuth.RegistrationEndpoint,
			raw.OAuth.IssuerURL,
		} {
			for _, value := range mcpURLSecretValues(endpoint) {
				add(value)
			}
		}
	}
	return values
}

// mcpURLSecretValues returns the credential-bearing parts of a configured
// endpoint: the userinfo password, the userinfo username, and every query value.
//
// Every query VALUE, not the sensitively-named ones, because the name is the
// operator's to choose and the point of equality redaction is that it does not
// have to guess. The shortestMCPSecret floor in credentialCandidates is what
// keeps ordinary short parameters (`v=1`, `mode=sse`) out of the set, so this
// does not blank harmless words out of unrelated text.
//
// The path is deliberately NOT collected. It is the part an operator needs to
// see to recognise which endpoint failed, and it is not where a credential is
// configured.
func mcpURLSecretValues(rawURL string) []string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil {
		return nil
	}
	// BOTH REPRESENTATIONS, decoded and raw. url.Parse and url.ParseQuery hand
	// back DECODED values, but the network client starts from the configured URL
	// string, so an MCP can echo the escaped spelling back in its failure body.
	// Exact-value redaction then knows "opaque-workspace-token-9f3c2b7ae1d8" and
	// the body contains "opaque%2Dworkspace%2Dtoken%2D9f3c2b7ae1d8", which matches
	// nothing and prints. %2D decodes to a hyphen, so the credential is fully
	// recoverable from what was displayed and persisted.
	//
	// The generic patterns do not catch it either, because the parameter name is
	// the operator's to choose. Collecting both forms fixes the boundary rather
	// than guessing at more names.
	values := make([]string, 0, 8)
	// PATH SEGMENTS ARE CREDENTIAL MATERIAL TOO.
	//
	// Query values and userinfo were treated as secret-bearing and the path as an
	// identifier, and the configuration contract accepts an arbitrary HTTP or SSE
	// path. Opaque path-segment credentials are an ordinary endpoint convention,
	// and this needs no crafted response body to leak: a failing http.Client.Do
	// returns a *url.Error carrying the request URL, which the failed-server path
	// wraps and renders.
	//
	// Only opaque-looking segments, by the same length floor the rest of this file
	// uses. A route like "/mcp" or "/v1/sse" is structure, and redacting it would
	// eat the diagnostic without protecting anything.
	for _, segment := range opaqueURLPathSegments(parsed.EscapedPath()) {
		values = append(values, segment)
		if decoded, err := url.PathUnescape(segment); err == nil && decoded != segment {
			values = append(values, decoded)
		}
	}
	if parsed.User != nil {
		if password, ok := parsed.User.Password(); ok {
			values = append(values, password)
		}
		// The username too: a token-as-username is a real shape, and the floor
		// discards an ordinary short login.
		values = append(values, parsed.User.Username())
		// The escaped spelling, taken from the ORIGINAL string rather than from
		// parsed.User.String(). Go re-escapes canonically there and leaves
		// unreserved characters alone, so a configured "%2D" comes back as "-" and
		// the raw form the server echoes would still match nothing.
		if rawUserinfo := rawURLUserinfo(trimmed); rawUserinfo != "" {
			values = append(values, rawUserinfo)
			if rawUser, rawPassword, found := strings.Cut(rawUserinfo, ":"); found {
				values = append(values, rawUser, rawPassword)
			}
		}
	}
	// Raw query values, taken from RawQuery before any decoding.
	for _, pair := range strings.Split(parsed.RawQuery, "&") {
		if pair == "" {
			continue
		}
		if _, rawValue, found := strings.Cut(pair, "="); found {
			values = append(values, rawValue)
		}
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return values
	}
	names := make([]string, 0, len(query))
	for name := range query {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values = append(values, query[name]...)
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
// maxMCPCredentialCandidates and maxMCPCredentialInput bound what one configured
// value can expand into.
//
// This walked every suffix after every space or colon and kept them all, so a
// delimiter-heavy value produced O(n) candidates and RedactString then ran a
// replacement pass for each. The expansion happened on the CONFIG side, outside
// the raw-error bound, so opening or refreshing /mcp for that server burned CPU
// and memory however short the server's error was.
//
// The tails exist for one narrow reason: a value configured as "Bearer <token>"
// has to yield <token> as well, because the server may echo only the credential.
// Two or three splits cover every such convention. Beyond that the suffixes stop
// being plausible credentials and start being work.
const (
	maxMCPCredentialCandidates = 8
	maxMCPCredentialInput      = 8 << 10
)

func credentialCandidates(value string) []string {
	candidates := make([]string, 0, 3)
	remainder := strings.TrimSpace(value)
	if len(remainder) > maxMCPCredentialInput {
		// Bounded before the walk, not after. A value this long is still redacted
		// whole, because the untruncated original is added by the caller; what is
		// dropped is only the suffix enumeration, which is what costs.
		return []string{remainder}
	}
	for {
		if len(remainder) >= shortestMCPSecret {
			candidates = append(candidates, remainder)
		}
		if len(candidates) >= maxMCPCredentialCandidates {
			return candidates
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
// mcpHeaderArgument parses ONE argument into the header flag it names and the
// header text it carries, if any.
//
// ONE PARSER FOR BOTH CONSUMERS. The redaction collector and the target row
// each derived the accepted shapes separately and drifted apart: an argument
// the reason redacted printed verbatim in the target one line below. Everything
// that decides "is this a header, and where is its value" now happens here.
//
// carried is the header text when this argument holds it, and empty when the
// value is the NEXT argument. The attached form "-HName: value" is included
// because it is the conventional curl spelling and neither consumer recognised
// it: the flag name parsed as "HName:..." and matched nothing.
//
// The long form folds case; the SHORT form does not, deliberately. "-H" is the
// header flag and "-h" is help, which takes no value, so folding here would
// consume the next argument after "-h" and blank an unrelated word out of every
// message that mentions it.
func mcpHeaderArgument(value string) (flag string, carried string, ok bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "-") {
		return "", "", false
	}
	// Long form first, so "--header..." is never mistaken for the short "-H".
	if rest, matched := cutFoldPrefix(trimmed, "--header"); matched {
		switch {
		case rest == "":
			return trimmed, "", true
		case strings.HasPrefix(rest, "="):
			return trimmed[:len(trimmed)-len(rest)], strings.TrimSpace(rest[1:]), true
		case strings.HasPrefix(rest, " "):
			return trimmed[:len(trimmed)-len(rest)], strings.TrimSpace(rest), true
		}
		return "", "", false
	}
	if !strings.HasPrefix(trimmed, "-H") {
		return "", "", false
	}
	rest := trimmed[len("-H"):]
	switch {
	case rest == "":
		return trimmed, "", true
	case strings.HasPrefix(rest, "="):
		return "-H", strings.TrimSpace(rest[1:]), true
	case strings.HasPrefix(rest, " "):
		return "-H", strings.TrimSpace(rest), true
	case strings.HasPrefix(rest, "-"):
		// "-H-something" is not a header spelling; refuse rather than guess.
		return "", "", false
	}
	// Attached: "-HName: value".
	return "-H", rest, true
}

// cutFoldPrefix reports whether s begins with prefix under case folding, and
// returns what follows it.
func cutFoldPrefix(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}

func isMCPHeaderFlag(value string) bool {
	_, _, ok := mcpHeaderArgument(value)
	return ok
}

func sensitiveMCPArgValues(args []string) []string {
	values := make([]string, 0, len(args))
	// A candidate that is itself a flag is never a value. Reading one would put
	// something like "--verbose" into the redaction set, and every message
	// mentioning it would lose the word.
	isFlag := func(value string) bool { return strings.HasPrefix(value, "-") }
	// HEADER FLAGS ARE THEIR OWN CLASS, because the credential is in the VALUE
	// and the flag name says nothing about it. isSensitiveMCPDisplayKey matches
	// token/secret/auth/credential and friends against the FLAG, so
	// `--header X-Workspace-Credential: opaque-token` was never collected: the
	// flag is "header", which matches none of them, and the credential rides in
	// an argument whose name the operator chose. A stdio child that rejects its
	// invocation echoes it into captured stderr, which this panel renders and the
	// transcript persists.
	//
	// The long form is matched case-insensitively. The SHORT form is not, and
	// that is deliberate: `-H` is the header flag, `-h` is help and takes no
	// value, so folding case here would set pending on `-h` and put the next
	// argument into the redaction set, blanking an unrelated word out of every
	// message that mentions it. Over-collection has already cost this panel a
	// readable docker image name once.
	// A header ARGUMENT carries "<name>: <value>" while a configured header
	// contributes only the value, because a map key is not part of it. Feeding the
	// whole argument in would make the entire line a candidate, so a server that
	// echoes the header back would have its NAME redacted too and the message
	// would no longer say which header was rejected. Only the value is offered;
	// credentialCandidates still splits it further for the "<scheme> <credential>"
	// shape.
	headerValue := func(value string) string {
		if _, rest, ok := strings.Cut(value, ":"); ok {
			if trimmed := strings.TrimSpace(rest); trimmed != "" {
				return trimmed
			}
		}
		return value
	}
	pending := false
	pendingHeader := false
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			// Blanks are skipped rather than consumed, matching the display pass.
			// Consuming one would take the blank as the value and leave the real
			// secret in the next position unredacted.
			continue
		}
		if pending {
			wasHeader := pendingHeader
			pending = false
			pendingHeader = false
			if !isFlag(arg) {
				if wasHeader {
					values = append(values, headerValue(arg))
				} else {
					values = append(values, arg)
				}
				continue
			}
			// Otherwise fall through: this argument is a flag in its own right.
		}
		// Cut at the FIRST "=" and keep the whole tail, so base64 padding and
		// values that themselves contain "=" survive intact.
		if key, rest, ok := strings.Cut(arg, "="); ok && (isSensitiveMCPDisplayKey(key) || isMCPHeaderFlag(key)) {
			collected := strings.TrimSpace(rest)
			if isMCPHeaderFlag(key) {
				collected = headerValue(collected)
			}
			values = append(values, collected)
			continue
		}
		// A flag and its value packed into a single argument. The display pass
		// gets this shape wrong in the other direction (it prints the whole thing
		// verbatim, then redacts the following, unrelated argument), so this
		// cannot be delegated to it.
		if flag, rest, ok := strings.Cut(arg, " "); ok && (isSensitiveMCPDisplayFlag(flag) || isMCPHeaderFlag(flag)) {
			collected := strings.TrimSpace(rest)
			if isMCPHeaderFlag(flag) {
				collected = headerValue(collected)
			}
			values = append(values, collected)
			continue
		}
		// The attached header spelling, "-HName: value", carries its value in
		// this same argument. Neither pass recognised it: the flag name parsed
		// as "HName:..." and matched nothing, so the value was never collected
		// here and printed whole one row below.
		if _, carried, ok := mcpHeaderArgument(arg); ok && carried != "" {
			values = append(values, headerValue(carried))
			continue
		}
		// Only an actual FLAG claims the next argument. isSensitiveMCPDisplayFlag
		// strips leading dashes before matching, so it says yes to a bare
		// positional word too, and the documented GitHub server config
		// (`-e GITHUB_PERSONAL_ACCESS_TOKEN ghcr.io/github/github-mcp-server`)
		// would put the IMAGE NAME into the redaction set: the pull failure would
		// then lose the one string that explains it. A positional argument is not
		// a flag and does not introduce a value.
		if isFlag(arg) && (isSensitiveMCPDisplayFlag(arg) || isMCPHeaderFlag(arg)) {
			pendingHeader = isMCPHeaderFlag(arg)
			// The value is the next argument, if there is one. A sensitive flag in
			// the last position simply has nothing to redact.
			pending = true
		}
	}
	return values
}

// rawURLUserinfo returns the userinfo section exactly as it was configured,
// before any decoding or canonical re-escaping.
//
// parsed.User.String() is not a substitute: it re-escapes by Go rules and leaves
// unreserved characters alone, so a configured "%2D" comes back as "-". The
// server echoes what it was given, so the raw spelling is the one that has to be
// matched.
func rawURLUserinfo(rawURL string) string {
	authority := rawURL
	if _, rest, found := strings.Cut(authority, "//"); found {
		authority = rest
	}
	// The userinfo ends at the first "@", and any "/" before it means there is
	// none at all.
	if slash := strings.IndexByte(authority, '/'); slash >= 0 {
		if at := strings.IndexByte(authority, '@'); at < 0 || at > slash {
			return ""
		}
		authority = authority[:slash]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return ""
	}
	return authority[:at]
}

// recoverableSecretPrefix reports whether a surviving prefix of size bytes
// gives away enough of a total-byte credential to matter.
//
// There has to be SOME floor or the tail of nearly every message disappears:
// with a handful of candidates one of them almost always begins with whatever
// character the text happens to end on, and cutting there would cost a
// character for nothing. The previous floor was a flat eight bytes, which is
// wrong in the direction that counts, because seven bytes of an eight-byte
// credential is the credential. So the rule is proportional as well as
// absolute: eight or more characters is independently useful, and so is half of
// the value however short it is.
func recoverableSecretPrefix(size, total int) bool {
	if size <= 0 || total <= 0 {
		return false
	}
	return size >= shortestMCPSecret || size*2 >= total
}

// longestPrefixSuffix returns the length of the longest prefix of pattern that
// is also a suffix of text.
//
// THE SEARCH IS SIZED TO THE CREDENTIAL, NOT TO A CONSTANT. The previous
// version inspected a fixed 4 KiB window at the end of the text, so a
// credential beginning before that window could never be matched: the inspected
// span started partway through it, and a middle is not a prefix. Measured on
// this code, a 6000-byte value positioned across the cut left 5000 of its bytes
// on the panel.
//
// KMP over "pattern + sentinel + tail" answers it in one pass. The work is
// linear in the CONFIGURED value, which the operator owns and which is already
// bounded, rather than in anything the remote server sent, so a hostile error
// cannot widen it.
func longestPrefixSuffix(pattern, text string) int {
	if pattern == "" || text == "" {
		return 0
	}
	if len(text) > len(pattern) {
		text = text[len(text)-len(pattern):]
	}
	const sentinel = "\x00"
	if strings.Contains(pattern, sentinel) || strings.Contains(text, sentinel) {
		// Unreachable for a rendered failure reason, whose control bytes are
		// already stripped. Degrade to the direct answer rather than trust a
		// sentinel that is not one.
		for size := len(text); size > 0; size-- {
			if strings.HasSuffix(text, pattern[:size]) {
				return size
			}
		}
		return 0
	}
	combined := pattern + sentinel + text
	failure := make([]int, len(combined))
	for i := 1; i < len(combined); i++ {
		length := failure[i-1]
		for length > 0 && combined[i] != combined[length] {
			length = failure[length-1]
		}
		if combined[i] == combined[length] {
			length++
		}
		failure[i] = length
	}
	return failure[len(combined)-1]
}

// mcpStaleObservationReason replaces a startup failure whose redaction context
// no longer exists. The row still reports that the server failed, which is what
// the observation actually established; only the reason is withheld.
const mcpStaleObservationReason = "startup failed; the details were dropped because the stored credentials changed since they were recorded"

// SAFETY MUST BE MONOTONIC OVER AN OBSERVATION.
//
// A retained skipped entry holds the RAW startup error, and every rebuild
// redacts it again from whatever the token store holds at that moment. That
// makes the safety of fixed text depend on mutable external state, and the
// dependency runs the wrong way: `/mcp oauth logout` deletes the stored bearer
// and returns the configuration unchanged, so retainedMCPSkipped keeps the
// observation while the candidate set that was hiding the bearer disappears.
// The next render then writes the credential into the panel and the transcript,
// AFTER the user asked for it to be forgotten. Refresh rotation, deletion by
// another process, and a token-store read failure all do the same thing.
//
// Rather than retain a second copy of the credentials, which would be a second
// long-lived plaintext store, the observation carries a FINGERPRINT of the
// material that made it safe. A change means the guarantee cannot be
// reproduced, so the reason is withheld instead of re-derived.
func staleMCPObservation(captured string, current []string) bool {
	if captured == "" {
		// No fingerprint recorded, so there is nothing to compare and no claim to
		// make. Callers that want the guarantee record one.
		return false
	}
	return captured != mcpCredentialFingerprint(current)
}

// mcpCredentialFingerprint identifies a set of credential values without
// retaining them. Order-independent, because SecretValues enumerates a map.
func mcpCredentialFingerprint(values []string) string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	digest := sha256.New()
	for _, value := range sorted {
		// Length-prefixed so ["ab","c"] and ["a","bc"] cannot collide.
		fmt.Fprintf(digest, "%d:%s", len(value), value)
	}
	return hex.EncodeToString(digest.Sum(nil))
}
