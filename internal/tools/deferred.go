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
// report its true (un-sanitized-token) server name for the reminder label. When
// a tool provides it, DeferredLine prefers it over the name-derived token, which
// would mislabel a server whose sanitized name itself contains an underscore
// (e.g. "git_hub" → "git"). It affects the cosmetic reminder label only; tool
// resolution never depends on this.
type mcpServerNamed interface {
	MCPServerName() string
}

// DeferredLine renders the compact advertisement line for a single deferred
// tool, deriving the MCP server label from the tool's reported server name when
// available, falling back to the token parsed from the tool's name. It is the
// exported entry point the agent loop uses to build each line of the
// deferred-tools reminder, so callers in other packages never touch the
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

const (
	systemReminderStart = "<system-reminder>"
	systemReminderEnd   = "</system-reminder>"
)

// BuildDeferredReminder wraps the given deferred-tool advertisement lines in a
// <system-reminder> block instructing the model to load a tool's full schema
// via tool_search (using a `select:Name1,Name2` exact query or keywords)
// before calling it. Returns "" when there are no lines.
func BuildDeferredReminder(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(systemReminderStart)
	b.WriteString("\n")
	b.WriteString("The tools listed below are available in this environment, but their full schemas are omitted from the current tool list to save context.\n")
	b.WriteString(`Call tool_search with query "select:Name1,Name2" (the exact names before each colon) or with keywords to load a tool's full schema before calling it. Calling an omitted tool directly will fail with an input validation error.` + "\n")
	b.WriteString("Do not call tool_search for tools already present in the current tool list, and do not guess or invent tool names.\n")
	b.WriteString("\n")
	b.WriteString("Deferred tools:\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
	b.WriteString(systemReminderEnd)
	return b.String()
}
