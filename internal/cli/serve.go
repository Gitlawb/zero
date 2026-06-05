package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/tools"
)

type serveOptions struct {
	mcp              bool
	cwd              string
	allowUnsafeTools bool
}

func runServe(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseServeArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeServeHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if !options.mcp {
		return writeExecUsageError(stderr, "serve requires --mcp. Use `zero serve --mcp`.")
	}

	workspaceRoot, err := resolveWorkspaceRoot(options.cwd, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	registry := newServeRegistry(workspaceRoot, options.allowUnsafeTools)
	if options.allowUnsafeTools {
		if _, err := fmt.Fprintln(stderr, "[zero] Unsafe MCP server tools enabled because --allow-unsafe-tools was passed."); err != nil {
			return exitCrash
		}
	}

	err = mcp.Serve(context.Background(), deps.stdin, stdout, registry, mcp.ServeOptions{
		Name:              "zero",
		Version:           version,
		PermissionGranted: options.allowUnsafeTools,
	})
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	return exitSuccess
}

func newServeRegistry(workspaceRoot string, allowUnsafeTools bool) *tools.Registry {
	registry := tools.NewRegistry()
	toolset := tools.CoreReadOnlyTools(workspaceRoot)
	if allowUnsafeTools {
		toolset = tools.CoreTools(workspaceRoot)
	}
	for _, tool := range toolset {
		registry.Register(tool)
	}
	return registry
}

func parseServeArgs(args []string) (serveOptions, bool, error) {
	options := serveOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--mcp":
			options.mcp = true
		case arg == "--allow-unsafe-tools":
			options.allowUnsafeTools = true
		case arg == "-C" || arg == "--cwd":
			index++
			if index >= len(args) {
				return options, false, execUsageError{arg + " requires a path"}
			}
			options.cwd = args[index]
		case strings.HasPrefix(arg, "--cwd="):
			options.cwd = strings.TrimPrefix(arg, "--cwd=")
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown serve flag %q", arg)}
		}
	}
	return options, false, nil
}

func writeServeHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero serve --mcp [flags]

Starts Zero as an MCP stdio server.

Flags:
      --mcp                   Run the MCP stdio server
  -C, --cwd <path>            Set the workspace directory
      --allow-unsafe-tools    Expose write and shell tools to the MCP host
  -h, --help                  Show this help
`)
	return err
}
