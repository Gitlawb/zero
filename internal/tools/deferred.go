package tools

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultShortenMax   = 200
	maxSchemaHintParams = 4
	maxSchemaHintLen    = 360
)

var (
	headingPrefix      = regexp.MustCompile(`^#{1,6}\s+(.+)$`)
	genericHeading     = regexp.MustCompile(`(?i)^(overview|description|summary)$`)
	collapseWhitespace = regexp.MustCompile(`\s+`)
)

// normalizeDescriptionLine trims a line and unwraps a leading markdown heading.
func normalizeDescriptionLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if m := headingPrefix.FindStringSubmatch(trimmed); m != nil {
		return strings.TrimSpace(m[1])
	}
	return trimmed
}

func isGenericDescriptionHeading(line string) bool {
	return genericHeading.MatchString(line)
}

// truncateDescription clips desc to at most max runes, preferring a word
// boundary and appending a single-rune ellipsis when it had to cut.
func truncateDescription(desc string, max int) string {
	runes := []rune(desc)
	if max <= 0 || len(runes) <= max {
		return desc
	}
	cut := string(runes[:max-1])
	if idx := strings.LastIndexByte(cut, ' '); idx > 0 {
		cut = cut[:idx]
	}
	return strings.TrimRight(cut, " ") + "…"
}

// shortenDescription reduces desc to a single meaningful line, collapses
// whitespace, and truncates to at most max runes with an ellipsis.
func shortenDescription(desc string, max int) string {
	if desc == "" {
		return ""
	}
	if max <= 0 {
		max = defaultShortenMax
	}
	var lines []string
	for _, raw := range strings.Split(desc, "\n") {
		if line := normalizeDescriptionLine(raw); line != "" {
			lines = append(lines, collapseWhitespace.ReplaceAllString(line, " "))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	meaningful := lines[0]
	if isGenericDescriptionHeading(meaningful) && len(lines) > 1 {
		meaningful = meaningful + " — " + lines[1]
	}
	return truncateDescription(meaningful, max)
}

// formatInputSchemaHint renders a one-line summary of a tool's input schema,
// e.g. "inputs (* required): a (string)*, b (number); +N more". Property names
// are sorted for deterministic output (Schema.Properties is a map). Returns
// "(none)" when the schema declares no properties. At most maxSchemaHintParams
// params are shown; the rest are summarized as "; +N more".
func formatInputSchemaHint(schema Schema) string {
	if len(schema.Properties) == 0 {
		return "(none)"
	}

	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}

	shown := names
	if len(shown) > maxSchemaHintParams {
		shown = shown[:maxSchemaHintParams]
	}

	parts := make([]string, 0, len(shown))
	for _, name := range shown {
		prop := schema.Properties[name]
		marker := ""
		if required[name] {
			marker = "*"
		}
		typePart := ""
		if t := strings.TrimSpace(prop.Type); t != "" {
			typePart = " (" + t + ")"
		}
		parts = append(parts, name+typePart+marker)
	}

	more := ""
	if len(names) > maxSchemaHintParams {
		more = fmt.Sprintf("; +%d more", len(names)-maxSchemaHintParams)
	}

	hint := "inputs (* required): " + strings.Join(parts, ", ") + more
	return shortenDescription(hint, maxSchemaHintLen)
}

// mcpToolNamePrefix mirrors the prefix used by mcp.registryToolName.
const mcpToolNamePrefix = "mcp_"

// mcpServerFromToolName extracts the server token from a synthesized MCP tool
// name produced by mcp.registryToolName ("mcp_<server>_<tool>"). It returns ""
// for non-MCP names and for names that lack both a server and a tool segment.
func mcpServerFromToolName(name string) string {
	rest, ok := strings.CutPrefix(name, mcpToolNamePrefix)
	if !ok {
		return ""
	}
	sep := strings.IndexByte(rest, '_')
	if sep <= 0 || sep == len(rest)-1 {
		// No server token, or nothing after the server token (no tool part).
		return ""
	}
	return rest[:sep]
}

// formatDeferredToolLine renders a single compact advertisement line for a
// deferred tool: "name: <short-desc> | server: <X> | <input-hint>". The
// "server: <X>" segment is omitted when server is empty (non-MCP tools).
func formatDeferredToolLine(name, description, server string, schema Schema) string {
	desc := shortenDescription(description, defaultShortenMax)
	if desc == "" {
		desc = "No description provided"
	}
	parts := []string{name + ": " + desc}
	if server != "" {
		parts = append(parts, "server: "+server)
	}
	parts = append(parts, formatInputSchemaHint(schema))
	return strings.Join(parts, " | ")
}

// mcpServerNamed is an optional interface a deferred MCP tool implements to
// report its true (un-sanitized-token) server name for discovery labels. When
// a tool provides it, DeferredLine prefers it over the name-derived token, which
// would mislabel a server whose sanitized name itself contains an underscore
// (e.g. "git_hub" → "git"). It affects the cosmetic discovery label only; tool
// resolution never depends on this.
type mcpServerNamed interface {
	MCPServerName() string
}

// DeferredLine renders the compact advertisement line for a single deferred
// tool, deriving the MCP server label from the tool's reported server name when
// available, falling back to the token parsed from the tool's name. It is the
// exported entry point the agent loop uses to build compact deferred metadata,
// so callers in other packages never touch the
// unexported formatters.
func DeferredLine(t Tool) string {
	server := mcpServerFromToolName(t.Name())
	if named, ok := t.(mcpServerNamed); ok {
		if reported := strings.TrimSpace(named.MCPServerName()); reported != "" {
			server = reported
		}
	}
	return formatDeferredToolLine(t.Name(), t.Description(), server, t.Parameters())
}

// DeferredSource reports the compact source label used in tool_search's dynamic
// description. MCP tools use their configured server name; other deferred tools
// fall back to the first name segment so families such as swarm_* are grouped.
func DeferredSource(t Tool) string {
	if t == nil {
		return ""
	}
	if named, ok := t.(mcpServerNamed); ok {
		if reported := strings.TrimSpace(named.MCPServerName()); reported != "" {
			return reported
		}
	}
	if server := mcpServerFromToolName(t.Name()); server != "" {
		return server
	}
	name := strings.TrimSpace(t.Name())
	if name == "" {
		return ""
	}
	if prefix, _, ok := strings.Cut(name, "_"); ok && prefix != "" {
		return prefix
	}
	return name
}

// DeferredToolDiscoveryLine renders the compact name/source entry used in
// tool_search's dynamic description. It intentionally omits descriptions and
// schemas; those are revealed only after tool_search loads a matching tool.
func DeferredToolDiscoveryLine(t Tool) string {
	if t == nil {
		return ""
	}
	name := strings.TrimSpace(t.Name())
	if name == "" {
		return ""
	}
	source := strings.TrimSpace(DeferredSource(t))
	if source == "" || source == name {
		return name
	}
	return name + " — " + source
}

// BuildToolSearchDescription renders the model-facing discovery instructions for
// deferred tools. This belongs on the tool_search tool definition, not as an
// extra user message, so the model can discover tools without treating discovery
// metadata as something to answer or acknowledge.
func BuildToolSearchDescription(deferred []Tool) string {
	seen := make(map[string]bool)
	lines := make([]string, 0, len(deferred))
	for _, tool := range deferred {
		line := DeferredToolDiscoveryLine(tool)
		if line != "" && !seen[line] {
			seen[line] = true
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)

	toolText := "No deferred tools are currently hidden."
	if len(lines) > 0 {
		for i, line := range lines {
			lines[i] = "- " + line
		}
		toolText = strings.Join(lines, "\n")
	}

	var b strings.Builder
	b.WriteString("# Tool discovery\n\n")
	b.WriteString("Searches over deferred tool metadata and exposes matching tools for the next model call.\n\n")
	b.WriteString("Deferred tools available through `tool_search`:\n")
	b.WriteString(toolText)
	b.WriteString("\n")
	b.WriteString("Use query `select:Name1,Name2` for exact names from this list, or use keywords to search tool names and descriptions. Do not call `tool_search` for tools already present in the current tool list.")
	return b.String()
}
