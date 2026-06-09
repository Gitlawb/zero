package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// ToolSearchToolName is the canonical registry name of the deferred-tool loader.
// It is exported so the agent loop, CLI filter validation, and the partition can
// reference the loader without re-stating the literal (keeping all the gates that
// must treat it specially in agreement).
const ToolSearchToolName = "tool_search"

// toolSearchMaxKeywordMatches caps how many deferred tools a bare-keyword query
// loads in one call, keeping the returned schemas bounded.
const toolSearchMaxKeywordMatches = 10

// toolSearchTool lets the model pull a deferred tool's full schema on demand.
// It holds the live registry (like escalate_model holds the model registry) so
// it can resolve names against the currently registered deferred-eligible tools.
type toolSearchTool struct {
	baseTool
	registry *Registry
}

// NewToolSearchTool builds the tool_search tool over the given registry. The
// tool is informational/no-side-effect and is advertised even in auto mode so
// the model can always discover withheld tools.
func NewToolSearchTool(registry *Registry) Tool {
	return toolSearchTool{
		baseTool: baseTool{
			name:        ToolSearchToolName,
			description: "Search for and load deferred tools by exact name or keyword. Use query \"select:Name1,Name2\" to load specific tools, or keywords to find matching tools. Loading a tool returns its full schema so you can call it on the next turn.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {
						Type:        "string",
						Description: "Either \"select:Name1,Name2\" for exact tool names, or space-separated keywords to match tool names and descriptions.",
					},
				},
				Required:             []string{"query"},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect:      SideEffectNone,
				Permission:      PermissionAllow,
				Reason:          "Lists and loads already-registered tool schemas; performs no side effects.",
				AdvertiseInAuto: true,
			},
		},
		registry: registry,
	}
}

// Run satisfies the Tool interface; actual dispatch goes through RunWithOptions.
func (tool toolSearchTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool toolSearchTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	query, err := stringArg(args, "query", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for tool_search: " + err.Error())
	}
	query = strings.TrimSpace(query)

	// Honor the run's operator filters so an operator-hidden deferred tool is
	// invisible to tool_search: it never resolves via select:, never ranks for a
	// keyword query, and is omitted from the no-match listing.
	deferred := tool.visibleDeferredTools(options.EnabledTools, options.DisabledTools)

	var matches []Tool
	if rest, ok := strings.CutPrefix(query, "select:"); ok {
		matches = tool.resolveExact(rest, deferred)
	} else {
		matches = tool.rankByKeyword(query, deferred)
	}

	if len(matches) == 0 {
		return okResult(tool.noMatchMessage(query, deferred))
	}

	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.Name())
	}
	return Result{
		Status: StatusOK,
		Output: renderLoadedTools(matches),
		Meta:   map[string]string{"load_tools": strings.Join(names, ",")},
	}
}

// visibleDeferredTools returns the registry's deferred-eligible tools that pass
// the operator allow/deny filters, sorted by name so keyword ranking and
// listings stay deterministic. A nil/empty filter pair admits every deferred
// tool (the pre-filter behavior). The allow/deny semantics mirror the agent's
// ToolAllowedByFilters: denied if in disabled; if enabled is non-empty, the tool
// must be listed in it.
func (tool toolSearchTool) visibleDeferredTools(enabled []string, disabled []string) []Tool {
	var deferred []Tool
	if tool.registry != nil {
		for _, candidate := range tool.registry.All() {
			if !IsDeferred(candidate) {
				continue
			}
			if !toolAllowedByFilters(candidate.Name(), enabled, disabled) {
				continue
			}
			deferred = append(deferred, candidate)
		}
	}
	sort.Slice(deferred, func(left, right int) bool {
		return deferred[left].Name() < deferred[right].Name()
	})
	return deferred
}

// toolAllowedByFilters mirrors agent.ToolAllowedByFilters (kept here to avoid an
// import cycle: the agent package imports tools, not the other way around). A
// name is denied when it appears in disabled; when enabled is non-empty, the
// name must appear in it to be allowed.
func toolAllowedByFilters(name string, enabled []string, disabled []string) bool {
	if len(enabled) > 0 && !containsName(enabled, name) {
		return false
	}
	return !containsName(disabled, name)
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// resolveExact maps a comma-separated name list (the part after "select:") to
// the matching deferred tools, preserving the model's order and skipping blanks
// and unknown names.
func (tool toolSearchTool) resolveExact(list string, deferred []Tool) []Tool {
	byName := make(map[string]Tool, len(deferred))
	for _, candidate := range deferred {
		byName[candidate.Name()] = candidate
	}
	var matches []Tool
	seen := make(map[string]bool)
	for _, raw := range strings.Split(list, ",") {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		if candidate, ok := byName[name]; ok {
			seen[name] = true
			matches = append(matches, candidate)
		}
	}
	return matches
}

// rankByKeyword scores deferred tools by case-insensitive substring match on the
// name (weighted higher) then the description, and returns the top matches.
func (tool toolSearchTool) rankByKeyword(query string, deferred []Tool) []Tool {
	keywords := strings.Fields(strings.ToLower(query))
	if len(keywords) == 0 {
		return nil
	}
	type scored struct {
		tool  Tool
		score int
		order int
	}
	var ranked []scored
	for index, candidate := range deferred {
		name := strings.ToLower(candidate.Name())
		desc := strings.ToLower(candidate.Description())
		score := 0
		for _, keyword := range keywords {
			if strings.Contains(name, keyword) {
				score += 2
			}
			if strings.Contains(desc, keyword) {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{tool: candidate, score: score, order: index})
		}
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].score != ranked[right].score {
			return ranked[left].score > ranked[right].score
		}
		return ranked[left].order < ranked[right].order
	})
	if len(ranked) > toolSearchMaxKeywordMatches {
		ranked = ranked[:toolSearchMaxKeywordMatches]
	}
	matches := make([]Tool, 0, len(ranked))
	for _, entry := range ranked {
		matches = append(matches, entry.tool)
	}
	return matches
}

// noMatchMessage reports that nothing loaded and names the available deferred
// tools so the model can retry with a valid select: query.
func (tool toolSearchTool) noMatchMessage(query string, deferred []Tool) string {
	if len(deferred) == 0 {
		return "No deferred tools are available to load."
	}
	names := make([]string, 0, len(deferred))
	for _, candidate := range deferred {
		names = append(names, candidate.Name())
	}
	return "No tools matched \"" + query + "\". Available tools: " + strings.Join(names, ", ") +
		`. Retry with query "select:Name1,Name2" using exact names.`
}

// renderLoadedTools lists each loaded tool's name, full description, and full
// input schema (pretty-printed JSON) so the model has the complete spec inline.
func renderLoadedTools(matches []Tool) string {
	var builder strings.Builder
	builder.WriteString("Loaded ")
	if len(matches) == 1 {
		builder.WriteString("1 tool")
	} else {
		builder.WriteString(strconv.Itoa(len(matches)))
		builder.WriteString(" tools")
	}
	builder.WriteString(". Full schemas follow; call them on the next turn.\n")
	for _, match := range matches {
		builder.WriteString("\n## ")
		builder.WriteString(match.Name())
		builder.WriteString("\n")
		builder.WriteString(match.Description())
		builder.WriteString("\n")
		schemaJSON, err := json.MarshalIndent(match.Parameters(), "", "  ")
		if err != nil {
			builder.WriteString("(schema unavailable)\n")
			continue
		}
		builder.WriteString("input schema:\n")
		builder.Write(schemaJSON)
		builder.WriteString("\n")
	}
	return builder.String()
}
