package cli

import (
	"fmt"
	"io"

	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/plugins"
	"github.com/Gitlawb/zero/internal/tools"
)

// pluginActivation holds what plugin activation contributed to a bootstrap so the
// later dispatcher + skill wiring can consume it: the plugin hook definitions and
// the plugin skill search roots. The zero value (no plugins) is inert — the
// dispatcher gets no extra hooks and the skill tool keeps only the default dir.
type pluginActivation struct {
	hooks      []hooks.Definition
	skillRoots []string
}

// activatePlugins loads the workspace's plugins and makes their declared
// extensions live: plugin tools are registered into registry here, and the
// returned activation carries the plugin hooks + skill roots for the caller to
// fold into the hook dispatcher and the skill tool.
//
// It fails OPEN: any load error (or a malformed plugin) is surfaced as a warning
// on stderr and otherwise skipped, so a broken plugin can never wedge startup —
// mirroring how newHookDispatcher and skills.Load tolerate bad input.
func activatePlugins(workspaceRoot string, registry *tools.Registry, deps appDeps, stderr io.Writer) pluginActivation {
	loaded, err := deps.loadPlugins(plugins.LoadOptions{Cwd: workspaceRoot})
	if err != nil {
		writePluginActivationWarning(stderr, "failed to load plugins: "+err.Error())
		return pluginActivation{}
	}

	// Load fails OPEN per plugin: a malformed manifest is recorded as a diagnostic
	// and the plugin is dropped rather than aborting the whole load. Surface those
	// diagnostics so a skipped plugin is never silent.
	for _, diagnostic := range loaded.Diagnostics {
		writePluginActivationWarning(stderr, formatLoadDiagnostic(diagnostic))
	}

	result := plugins.Activate(registry, loaded.Plugins, plugins.ActivateOptions{Cwd: workspaceRoot})
	for _, warning := range result.Warnings {
		writePluginActivationWarning(stderr, warning)
	}

	// Re-register the skill tool to also resolve plugin-declared skills. The core
	// skill tool only reads the default skills dir; the plugin-aware replacement
	// merges the default dir with the plugin skill roots (default dir wins a name
	// clash), so plugin skills appear in the agent's skill list. With no plugin
	// skill roots this is byte-equivalent to the default skills surface.
	if len(result.SkillRoots) > 0 {
		registry.Register(plugins.NewSkillTool(deps.skillsDir(), result.SkillRoots))
	}

	return pluginActivation{hooks: result.Hooks, skillRoots: result.SkillRoots}
}

// formatLoadDiagnostic renders a plugin load diagnostic into a single warning
// line, prefixing the diagnostic kind and appending the most specific locator
// available (manifest path, then plugin path, then plugin ID) so an operator can
// locate the offending plugin even when the manifest path is unknown.
func formatLoadDiagnostic(diagnostic plugins.Diagnostic) string {
	message := fmt.Sprintf("[%s] %s", diagnostic.Kind, diagnostic.Message)
	locator := diagnostic.ManifestPath
	if locator == "" {
		locator = diagnostic.PluginPath
	}
	if locator == "" {
		locator = diagnostic.PluginID
	}
	if locator != "" {
		message += " (" + locator + ")"
	}
	return message
}

func writePluginActivationWarning(stderr io.Writer, message string) {
	if stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(stderr, "[zero] WARNING: plugin activation: %s\n", message)
}
