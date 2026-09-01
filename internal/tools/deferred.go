package tools

import (
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

// mcpServerNamed is an optional interface a deferred MCP tool implements to
// report its true (un-sanitized-token) server name for discovery labels. When
// a tool provides it, DeferredLine prefers it over the name-derived token, which
// would mislabel a server whose sanitized name itself contains an underscore
// (e.g. "git_hub" → "git"). It affects the cosmetic discovery label only; tool
// resolution never depends on this.
type mcpServerNamed interface {
	MCPServerName() string
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
