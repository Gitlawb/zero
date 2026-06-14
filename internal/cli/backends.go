package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/plugins"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/zerocommands"
)

type backendStatusOptions struct {
	json bool
}

func runBackends(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseBackendsArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeBackendsHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	snapshot, err := backendLifecycleSnapshot(deps)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, snapshot); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, redaction.RedactString(formatBackendLifecycleSnapshot(snapshot), redaction.Options{})); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parseBackendsArgs(args []string) (backendStatusOptions, bool, error) {
	options := backendStatusOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--json":
			options.json = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown backends flag %q", arg)}
		}
	}
	return options, false, nil
}

func backendLifecycleSnapshot(deps appDeps) (zerocommands.BackendLifecycleSnapshot, error) {
	cwd, err := deps.getwd()
	if err != nil {
		return zerocommands.BackendLifecycleSnapshot{}, fmt.Errorf("failed to resolve workspace: %w", err)
	}

	cfg, err := deps.resolveMCPConfig(cwd)
	if err != nil {
		return zerocommands.BackendLifecycleSnapshot{}, err
	}
	servers, err := mcp.NormalizeConfig(cfg)
	if err != nil {
		return zerocommands.BackendLifecycleSnapshot{}, err
	}

	hookResult, err := deps.loadHooks(hooks.LoadOptions{Cwd: cwd})
	if err != nil {
		return zerocommands.BackendLifecycleSnapshot{}, err
	}
	pluginResult, err := deps.loadPlugins(plugins.LoadOptions{Cwd: cwd})
	if err != nil {
		return zerocommands.BackendLifecycleSnapshot{}, err
	}

	return zerocommands.NewBackendLifecycleSnapshot(servers, hookResult.Config.Hooks, pluginResult.Plugins), nil
}

func formatBackendLifecycleSnapshot(snapshot zerocommands.BackendLifecycleSnapshot) string {
	lines := []string{"Zero Backends:"}
	lines = append(lines, fmt.Sprintf("  MCP servers: %d", len(snapshot.MCPServers)))
	for _, server := range snapshot.MCPServers {
		detail := server.Command
		if detail == "" {
			detail = server.URL
		}
		counts := []string{}
		if server.ArgCount > 0 {
			counts = append(counts, fmt.Sprintf("%d args", server.ArgCount))
		}
		if server.EnvKeyCount > 0 {
			counts = append(counts, fmt.Sprintf("%d env", server.EnvKeyCount))
		}
		if server.HeaderCount > 0 {
			counts = append(counts, fmt.Sprintf("%d headers", server.HeaderCount))
		}
		suffix := ""
		if len(counts) > 0 {
			suffix = " (" + strings.Join(counts, ", ") + ")"
		}
		lines = append(lines, fmt.Sprintf("    - %s [%s] %s%s", server.Name, server.Type, detail, suffix))
	}

	lines = append(lines, fmt.Sprintf("  Hooks: %d", len(snapshot.Hooks)))
	for _, hook := range snapshot.Hooks {
		label := hook.Name
		if label == "" {
			label = hook.ID
		}
		state := "disabled"
		if hook.Enabled {
			state = "enabled"
		}
		lines = append(lines, fmt.Sprintf("    - %s [%s] %s %s", label, hook.Event, state, hook.Command))
	}

	lines = append(lines, fmt.Sprintf("  Plugins: %d", len(snapshot.Plugins)))
	for _, plugin := range snapshot.Plugins {
		state := "disabled"
		if plugin.Enabled {
			state = "enabled"
		}
		lines = append(lines, fmt.Sprintf("    - %s [%s] %s tools=%d prompts=%d skills=%d hooks=%d", plugin.ID, plugin.Source, state, plugin.ToolCount, plugin.PromptCount, plugin.SkillCount, plugin.HookCount))
	}
	return strings.Join(lines, "\n")
}

func writeBackendsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero backends [flags]

Inspect MCP, hook, and plugin backend lifecycle state without connecting to
external MCP servers.

Flags:
      --json    Print backend lifecycle data as JSON
  -h, --help    Show this help
`)
	return err
}
