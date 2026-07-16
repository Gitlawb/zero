package tools

import "github.com/Gitlawb/zero/internal/localcontrol"

// BuiltinCatalog returns every built-in tool that Zero can expose to the model
// from this package (core + control + optional local-control helpers).
//
// It is the source of truth for metadata coverage tests: every tool returned
// here must declare an explicit Effect other than EffectUnknown.
//
// Not included (fail-closed EffectUnknown by design):
//   - MCP tools (internal/mcp registryTool)
//   - Plugin tools (internal/plugins pluginTool)
//   - Specialist tools registered outside this package (Task, Generate, …)
//
// workspaceRoot is used only to construct path-scoped tools; it need not exist.
func BuiltinCatalog(workspaceRoot string) []Tool {
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	var all []Tool
	all = append(all, CoreReadOnlyTools(workspaceRoot)...)
	all = append(all, CoreWriteTools(workspaceRoot)...)
	all = append(all, CoreShellTools(workspaceRoot)...)
	// Network tools: always include web_fetch; always include web_search for
	// classification even when no backend is configured at runtime (registration
	// remains conditional in CoreNetworkTools).
	all = append(all, NewWebFetchTool(), NewWebSearchTool())
	all = append(all, NewEscalateModelTool())

	// tool_search needs a live registry of the tools above.
	reg := NewRegistry()
	for _, tool := range all {
		reg.Register(tool)
	}
	search := NewToolSearchTool(reg)
	reg.Register(search)
	all = append(all, search)

	// Local-control tools are feature-gated at registration time but always
	// exist as built-in implementations with fixed classifications.
	all = append(all, NewLocalBrowserTools(localcontrol.BrowserOptions{})...)
	all = append(all, NewLocalDesktopTools(localcontrol.DesktopOptions{})...)
	all = append(all, NewLocalTerminalTools(localcontrol.TerminalOptions{})...)
	all = append(all, NewLocalControlArtifactTools(LocalControlArtifactOptions{})...)
	return all
}

// ValidateBuiltinCatalog returns diagnostics for every catalog tool that is
// missing or inconsistently classified. Empty means full coverage.
func ValidateBuiltinCatalog(workspaceRoot string) []string {
	var problems []string
	seen := map[string]ToolCapabilities{}
	for _, tool := range BuiltinCatalog(workspaceRoot) {
		name := tool.Name()
		caps := CapabilitiesOf(tool)
		if prev, ok := seen[name]; ok {
			if prev.Effect != caps.Effect || prev.ThreadSafe != caps.ThreadSafe {
				problems = append(problems, name+": duplicate registration with inconsistent metadata")
			}
			continue
		}
		seen[name] = caps
		if caps.Effect == EffectUnknown {
			problems = append(problems, name+": built-in tool remains EffectUnknown (metadata required)")
		}
		problems = append(problems, ValidateCapabilities(name, caps)...)
	}
	return problems
}
