package cli

import (
	"fmt"
	"io"

	"github.com/Gitlawb/zero/internal/acp"
	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/tools"
)

const acpUsage = `zero acp — serve the Agent Client Protocol (ACP) over stdio

Editors that speak ACP (Zed, JetBrains, Neovim, ...) spawn this command and drive
ZERO as a backend over JSON-RPC 2.0 on stdin/stdout. ZERO keeps your provider,
model, and API keys (BYOK); the editor only hosts the conversation thread.

Usage:
  zero acp

Not meant to be run interactively — point your editor's ACP / external-agent
setting at "zero acp".`

// runACP serves ACP over stdio so an editor can drive ZERO's agent core. It
// speaks JSON-RPC 2.0 (newline-delimited JSON) on stdin/stdout; stderr stays free
// for human-readable diagnostics. The session lifecycle maps onto ZERO's own
// session store, and provider/model/keys remain owned by ZERO.
func runACP(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			fmt.Fprintln(stdout, acpUsage)
			return exitSuccess
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown acp flag %q", arg))
		}
	}

	models, err := modelregistry.DefaultRegistry()
	if err != nil {
		return writeAppError(stderr, "acp: model registry: "+err.Error(), exitCrash)
	}

	conn := acp.NewConn(deps.stdin, stdout)
	acp.NewAgent(conn, acp.Deps{
		ResolveConfig: deps.resolveConfig,
		NewProvider:   deps.newProvider,
		RunAgent:      agent.Run,
		BuildRegistry: func(workspaceRoot string) *tools.Registry { return newCoreRegistry(workspaceRoot) },
		Store:         deps.newSessionStore(),
		Models:        models,
		AgentInfo:     acp.Implementation{Name: "zero", Version: version},
	})

	ctx, stop := signalContext()
	defer stop()
	if err := conn.Serve(ctx); err != nil && ctx.Err() == nil {
		return writeAppError(stderr, "acp: "+err.Error(), exitCrash)
	}
	return exitSuccess
}
