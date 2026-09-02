package tools

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// newWebFetchToolWithClient is a test seam: it builds the live web_fetch tool
// with an injected HTTP client so tests can stub transport behavior.
func newWebFetchToolWithClient(client *http.Client) Tool {
	return newWebFetchToolWithClientAndResolver(client, nil)
}

// budgetBashOutput truncates stdout and stderr to bashOutputBudgetBytes each,
// keeping the head and tail of anything larger, and records raw/emitted byte
// counts plus a truncated flag in meta (mirroring outputBudgetMeta's shape for
// the read/search tools). Detection that needs the full output (sandbox-denial
// scanning) must run on the raw strings before this is applied.
func budgetBashOutput(stdout string, stderr string, meta map[string]string) (string, string, bool) {
	return budgetBashCapture(stdout, len(stdout), stderr, len(stderr), meta)
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

func isGenericDescriptionHeading(line string) bool {
	return genericHeading.MatchString(line)
}

// normalizeDescriptionLine trims a line and unwraps a leading markdown heading.
func normalizeDescriptionLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if m := headingPrefix.FindStringSubmatch(trimmed); m != nil {
		return strings.TrimSpace(m[1])
	}
	return trimmed
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
