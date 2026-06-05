package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/plugins"
	"github.com/Gitlawb/zero/internal/providers"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/tui"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

var version = "dev"

type appDeps struct {
	getwd            func() (string, error)
	stdin            io.Reader
	resolveConfig    func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error)
	resolveMCPConfig func(workspaceRoot string) (config.MCPConfig, error)
	newProvider      func(config.ProviderProfile) (zeroruntime.Provider, error)
	newSessionStore  func() *sessions.Store
	loadPlugins      func(plugins.LoadOptions) (plugins.LoadResult, error)
	loadHooks        func(hooks.LoadOptions) (hooks.LoadResult, error)
	newMCPStore      func() (*mcp.PermissionStore, error)
	registerMCPTools func(context.Context, *tools.Registry, config.MCPConfig, mcp.RegisterOptions) (mcpToolRuntime, error)
	runTUI           func(context.Context, tui.Options) int
	now              func() time.Time
}

type mcpToolRuntime interface {
	Close() error
}

type noopMCPRuntime struct{}

func (noopMCPRuntime) Close() error {
	return nil
}

// Run executes the minimal Go CLI surface. It returns an exit code so tests can
// exercise command behavior without terminating the test process.
func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	return runWithDeps(args, stdout, stderr, defaultAppDeps())
}

func defaultAppDeps() appDeps {
	return appDeps{
		getwd: os.Getwd,
		stdin: os.Stdin,
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			options, err := config.DefaultResolveOptions(workspaceRoot)
			if err != nil {
				return config.ResolvedConfig{}, err
			}
			options.Overrides = overrides
			return config.Resolve(options)
		},
		resolveMCPConfig: func(workspaceRoot string) (config.MCPConfig, error) {
			options, err := config.DefaultResolveOptions(workspaceRoot)
			if err != nil {
				return config.MCPConfig{}, err
			}
			return config.ResolveMCP(options)
		},
		newProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return providers.New(profile, providers.Options{UserAgent: userAgent()})
		},
		newSessionStore: func() *sessions.Store {
			return sessions.NewStore(sessions.StoreOptions{})
		},
		loadPlugins: plugins.Load,
		loadHooks:   hooks.LoadConfig,
		newMCPStore: func() (*mcp.PermissionStore, error) {
			return mcp.NewPermissionStore(mcp.StoreOptions{})
		},
		registerMCPTools: func(ctx context.Context, registry *tools.Registry, cfg config.MCPConfig, options mcp.RegisterOptions) (mcpToolRuntime, error) {
			return mcp.RegisterTools(ctx, registry, cfg, options)
		},
		runTUI: tui.Run,
		now:    time.Now,
	}
}

func userAgent() string {
	return "zero/" + version
}

func runWithDeps(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	deps = fillAppDeps(deps)

	if len(args) == 0 {
		return runInteractiveTUI(stderr, deps)
	}

	switch args[0] {
	case "-h", "--help", "help":
		if err := writeHelp(stdout); err != nil {
			return 1
		}
		return 0
	case "-v", "--version", "version":
		if _, err := fmt.Fprintf(stdout, "zero %s\n", version); err != nil {
			return 1
		}
		return 0
	case "-p", "--prompt":
		if len(args) < 2 {
			return writePromptRequired(stderr)
		}
		execArgs := append([]string{"--prompt", args[1]}, args[2:]...)
		return runExec(execArgs, stdout, stderr, deps)
	case "exec":
		return runExec(args[1:], stdout, stderr, deps)
	case "config":
		return runConfig(args[1:], stdout, stderr, deps)
	case "models":
		return runModels(args[1:], stdout, stderr)
	case "providers":
		return runProviders(args[1:], stdout, stderr, deps)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr, deps)
	case "search", "find":
		return runSearch(args[1:], stdout, stderr, deps)
	case "plugins", "plugin":
		return runPlugins(args[1:], stdout, stderr, deps)
	case "hooks":
		return runHooks(args[1:], stdout, stderr, deps)
	case "mcp":
		return runMCP(args[1:], stdout, stderr, deps)
	case "serve":
		return runServe(args[1:], stdout, stderr, deps)
	default:
		if _, err := fmt.Fprintf(stderr, "unknown command %q\n", args[0]); err != nil {
			return 1
		}
		if _, err := fmt.Fprintln(stderr, "Run zero --help for usage."); err != nil {
			return 1
		}
		return 2
	}
}

func fillAppDeps(deps appDeps) appDeps {
	defaults := defaultAppDeps()
	if deps.getwd == nil {
		deps.getwd = defaults.getwd
	}
	if deps.stdin == nil {
		deps.stdin = defaults.stdin
	}
	if deps.resolveConfig == nil {
		deps.resolveConfig = defaults.resolveConfig
	}
	if deps.resolveMCPConfig == nil {
		deps.resolveMCPConfig = defaults.resolveMCPConfig
	}
	if deps.newProvider == nil {
		deps.newProvider = defaults.newProvider
	}
	if deps.newSessionStore == nil {
		deps.newSessionStore = defaults.newSessionStore
	}
	if deps.loadPlugins == nil {
		deps.loadPlugins = defaults.loadPlugins
	}
	if deps.loadHooks == nil {
		deps.loadHooks = defaults.loadHooks
	}
	if deps.newMCPStore == nil {
		deps.newMCPStore = defaults.newMCPStore
	}
	if deps.registerMCPTools == nil {
		deps.registerMCPTools = defaults.registerMCPTools
	}
	if deps.runTUI == nil {
		deps.runTUI = defaults.runTUI
	}
	if deps.now == nil {
		deps.now = defaults.now
	}
	return deps
}

func runInteractiveTUI(stderr io.Writer, deps appDeps) int {
	workspaceRoot, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), 1)
	}

	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}

	provider, err := buildProvider(resolved, deps)
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}

	registry := newCoreRegistry(workspaceRoot)
	mcpRuntime, err := registerMCPToolsForWorkspace(context.Background(), workspaceRoot, registry, deps, mcp.AutonomyLow)
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}
	defer closeMCPRuntime(stderr, mcpRuntime)
	permissionMode := agent.PermissionModeAuto
	return deps.runTUI(context.Background(), tui.Options{
		Cwd:             workspaceRoot,
		ProviderName:    resolved.Provider.Name,
		ModelName:       resolved.Provider.Model,
		ProviderProfile: resolved.Provider,
		Provider:        provider,
		NewProvider:     deps.newProvider,
		Registry:        registry,
		SessionStore:    deps.newSessionStore(),
		AgentOptions: agent.Options{
			MaxTurns:       resolved.MaxTurns,
			Registry:       registry,
			PermissionMode: permissionMode,
		},
		PermissionMode: permissionMode,
	})
}

func buildProvider(resolved config.ResolvedConfig, deps appDeps) (zeroruntime.Provider, error) {
	if resolved.Provider == (config.ProviderProfile{}) {
		return nil, nil
	}
	return deps.newProvider(resolved.Provider)
}

func newCoreRegistry(workspaceRoot string) *tools.Registry {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(workspaceRoot) {
		registry.Register(tool)
	}
	return registry
}

func closeMCPRuntime(stderr io.Writer, runtime mcpToolRuntime) {
	if runtime == nil {
		return
	}
	if err := runtime.Close(); err != nil {
		_, _ = fmt.Fprintf(stderr, "[zero] mcp_close_error: %s\n", err)
	}
}

func writeAppError(stderr io.Writer, message string, exitCode int) int {
	if _, err := fmt.Fprintf(stderr, "[zero] %s\n", message); err != nil {
		return 1
	}
	return exitCode
}

func writeHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `ZERO terminal coding agent

Usage:
  zero [command]

Commands:
  exec       Run a one-shot prompt through the Go agent runtime
  config     Inspect resolved Go configuration without leaking secrets
  models     List Zero model registry entries
  providers  Inspect resolved provider profiles
  doctor     Run backend health checks for config and provider setup
  search     Search persisted local Zero session events
  find       Alias for search
  plugins    Inspect local Zero plugin manifests
  hooks      Inspect Zero hook configuration
  mcp        Manage MCP backend settings
  serve      Run Zero protocol servers
  help       Show this help
  version    Print version

Flags:
  -h, --help       Show this help
  -v, --version    Print version
  -p, --prompt     Run a one-shot prompt
`)
	return err
}

func writePromptRequired(stderr io.Writer) int {
	if _, err := fmt.Fprintln(stderr, "[zero] Prompt required. Use `zero exec \"prompt\"` or `zero exec --file prompt.txt`."); err != nil {
		return 1
	}
	return 2
}

func writeExecHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero exec [flags] [prompt]

Runs a one-shot prompt through the Go agent runtime.

Flags:
  -f, --file <path>                  Read prompt text from a file
  -m, --model <model>                Select the model for provider setup
      --max-turns <number>           Override the maximum agent loop turns
      --auto <low|medium|high>       Set exec autonomy; high enables unsafe tools
      --enabled-tools <tools>        Only expose these comma or space separated tools
      --disabled-tools <tools>       Hide these comma or space separated tools
      --list-tools                   List model-visible tools and exit
  -C, --cwd <path>                   Set the workspace directory
  -i, --input-format text|stream-json
                                    Select prompt input format
  -o, --output-format text|json|stream-json
                                    Select text, JSON, or schema-versioned JSONL output
      --prompt <prompt>              Provide prompt text as a flag
      --resume [id]                  Resume a session; omit id to use the latest
      --fork <id>                    Fork an existing session into a new session
      --skip-permissions-unsafe      Allow prompt-gated tools without approval
`)
	return err
}
