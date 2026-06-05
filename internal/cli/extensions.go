package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/plugins"
	"github.com/Gitlawb/zero/internal/redaction"
)

type pluginListOptions struct {
	json bool
}

type hookListOptions struct {
	json bool
}

type mcpCommandOptions struct {
	json    bool
	confirm bool
}

func runPlugins(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "plugins subcommand required. Use `zero plugins list`.")
	}
	switch args[0] {
	case "-h", "--help", "help":
		if err := writePluginsHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "list":
		options, help, err := parsePluginListArgs(args[1:])
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if help {
			if err := writePluginsListHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		cwd, err := deps.getwd()
		if err != nil {
			return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
		}
		result, err := deps.loadPlugins(plugins.LoadOptions{Cwd: cwd})
		if err != nil {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
		}
		if options.json {
			payload := struct {
				Plugins     []plugins.LoadedPlugin `json:"plugins"`
				Diagnostics []plugins.Diagnostic   `json:"diagnostics"`
			}{Plugins: result.Plugins, Diagnostics: result.Diagnostics}
			if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		output := redaction.RedactString(plugins.FormatList(result.Plugins, result.Diagnostics), redaction.Options{})
		if _, err := fmt.Fprintln(stdout, output); err != nil {
			return exitCrash
		}
		return exitSuccess
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown plugins subcommand %q", args[0]))
	}
}

func runHooks(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "hooks subcommand required. Use `zero hooks list`.")
	}
	switch args[0] {
	case "-h", "--help", "help":
		if err := writeHooksHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "list":
		options, help, err := parseHookListArgs(args[1:])
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if help {
			if err := writeHooksListHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		cwd, err := deps.getwd()
		if err != nil {
			return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
		}
		result, err := deps.loadHooks(hooks.LoadOptions{Cwd: cwd})
		if err != nil {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
		}
		if options.json {
			payload := struct {
				Hooks       hooks.Config       `json:"hooks"`
				Diagnostics []hooks.Diagnostic `json:"diagnostics"`
			}{Hooks: result.Config, Diagnostics: result.Diagnostics}
			if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		output := redaction.RedactString(hooks.FormatList(result.Config, result.Diagnostics), redaction.Options{})
		if _, err := fmt.Fprintln(stdout, output); err != nil {
			return exitCrash
		}
		return exitSuccess
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown hooks subcommand %q", args[0]))
	}
}

func runMCP(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "mcp subcommand required. Use `zero mcp permissions list`.")
	}
	switch args[0] {
	case "-h", "--help", "help":
		if err := writeMCPHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "permissions":
		return runMCPPermissions(args[1:], stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown mcp subcommand %q", args[0]))
	}
}

func runMCPPermissions(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "mcp permissions subcommand required. Use `zero mcp permissions list`.")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		if err := writeMCPPermissionsHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	switch args[0] {
	case "list":
		options, help, err := parseMCPCommandOptions(args[1:])
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if help {
			if err := writeMCPPermissionsHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		store, err := deps.newMCPStore()
		if err != nil {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
		}
		permissions, err := store.List()
		if err != nil {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
		}
		if options.json {
			payload := struct {
				Permissions []mcp.PermissionGrant `json:"permissions"`
			}{Permissions: permissions}
			if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		if _, err := fmt.Fprintln(stdout, mcp.FormatPermissionList(permissions)); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "revoke":
		return runMCPPermissionsRevoke(args[1:], stdout, stderr, deps)
	case "clear":
		return runMCPPermissionsClear(args[1:], stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown mcp permissions subcommand %q", args[0]))
	}
}

func runMCPPermissionsRevoke(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, positional, help, err := parseMCPPositionalCommand(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeMCPPermissionsHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if len(positional) == 0 || len(positional) > 2 {
		return writeExecUsageError(stderr, "usage: zero mcp permissions revoke <server> [<tool>] [--json]")
	}

	store, err := deps.newMCPStore()
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	serverName := positional[0]
	if len(positional) == 2 {
		revoked, err := store.RevokeTool(serverName, positional[1])
		if err != nil {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
		}
		if options.json {
			payload := struct {
				Revoked    int    `json:"revoked"`
				Scope      string `json:"scope"`
				ServerName string `json:"serverName"`
				ToolName   string `json:"toolName"`
			}{Revoked: revoked, Scope: string(mcp.ScopeTool), ServerName: serverName, ToolName: positional[1]}
			if err := writePrettyJSON(stdout, payload); err != nil {
				return exitCrash
			}
		} else if _, err := fmt.Fprintf(stdout, "Revoked %d MCP tool permission grant(s) for %s/%s.\n", revoked, serverName, positional[1]); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	revoked, err := store.RevokeServer(serverName)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		payload := struct {
			Revoked    int    `json:"revoked"`
			Scope      string `json:"scope"`
			ServerName string `json:"serverName"`
		}{Revoked: revoked, Scope: string(mcp.ScopeServer), ServerName: serverName}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
	} else if _, err := fmt.Fprintf(stdout, "Revoked %d MCP server permission grant(s) for %s.\n", revoked, serverName); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runMCPPermissionsClear(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseMCPCommandOptions(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeMCPPermissionsHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if !options.confirm {
		return writeExecUsageError(stderr, "Pass --confirm to clear all MCP permission grants.")
	}
	store, err := deps.newMCPStore()
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	cleared, err := store.Clear()
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		payload := struct {
			Cleared int `json:"cleared"`
		}{Cleared: cleared}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
	} else if _, err := fmt.Fprintf(stdout, "Cleared %d MCP permission grant(s).\n", cleared); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parsePluginListArgs(args []string) (pluginListOptions, bool, error) {
	options := pluginListOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--json":
			options.json = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown plugins list flag %q", arg)}
		}
	}
	return options, false, nil
}

func parseHookListArgs(args []string) (hookListOptions, bool, error) {
	options := hookListOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--json":
			options.json = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown hooks list flag %q", arg)}
		}
	}
	return options, false, nil
}

func parseMCPCommandOptions(args []string) (mcpCommandOptions, bool, error) {
	options := mcpCommandOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--json":
			options.json = true
		case "--confirm":
			options.confirm = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown mcp permissions flag %q", arg)}
		}
	}
	return options, false, nil
}

func parseMCPPositionalCommand(args []string) (mcpCommandOptions, []string, bool, error) {
	options := mcpCommandOptions{}
	positional := []string{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, positional, true, nil
		case "--json":
			options.json = true
		case "--confirm":
			options.confirm = true
		default:
			if strings.HasPrefix(arg, "-") {
				return options, positional, false, execUsageError{fmt.Sprintf("unknown mcp permissions flag %q", arg)}
			}
			positional = append(positional, arg)
		}
	}
	return options, positional, false, nil
}

func writePluginsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins <command>

Commands:
  list    List local Zero plugins
`)
	return err
}

func writePluginsListHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins list [flags]

Flags:
      --json    Print local plugin data as JSON
  -h, --help    Show this help
`)
	return err
}

func writeHooksHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero hooks <command>

Commands:
  list    List configured Zero hooks
`)
	return err
}

func writeHooksListHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero hooks list [flags]

Flags:
      --json    Print hook config as JSON
  -h, --help    Show this help
`)
	return err
}

func writeMCPHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero mcp <command>

Commands:
  permissions    Manage persistent MCP tool permissions
`)
	return err
}

func writeMCPPermissionsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero mcp permissions <command>

Commands:
  list                  List all persistent MCP permissions
  revoke <server> [<tool>] Revoke an MCP server or tool permission
  clear --confirm       Clear all persistent MCP permissions

Flags:
      --json       Print command result as JSON
      --confirm    Confirm destructive clear operation
  -h, --help       Show this help
`)
	return err
}
