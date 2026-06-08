package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/observability"
	"github.com/Gitlawb/zero/internal/plugins"
	"github.com/Gitlawb/zero/internal/providers"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/selfverify"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/skills"
	"github.com/Gitlawb/zero/internal/specialist"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/tui"
	"github.com/Gitlawb/zero/internal/update"
	"github.com/Gitlawb/zero/internal/verify"
	"github.com/Gitlawb/zero/internal/worktrees"
	"github.com/Gitlawb/zero/internal/zerogit"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

var version = "dev"

type appDeps struct {
	getwd                func() (string, error)
	stdin                io.Reader
	resolveConfig        func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error)
	resolveMCPConfig     func(workspaceRoot string) (config.MCPConfig, error)
	newProvider          func(config.ProviderProfile) (zeroruntime.Provider, error)
	newSessionStore      func() *sessions.Store
	loadPlugins          func(plugins.LoadOptions) (plugins.LoadResult, error)
	loadHooks            func(hooks.LoadOptions) (hooks.LoadResult, error)
	skillsDir            func() string
	newMCPStore          func() (*mcp.PermissionStore, error)
	newSandboxStore      func() (*sandbox.GrantStore, error)
	selectSandboxBackend func(sandbox.BackendOptions) sandbox.Backend
	registerMCPTools     func(context.Context, *tools.Registry, config.MCPConfig, mcp.RegisterOptions) (mcpToolRuntime, error)
	prepareWorktree      func(context.Context, worktrees.Options) (worktrees.Result, error)
	detectVerifyPlan     func(string) (verify.Plan, error)
	runVerify            func(context.Context, verify.Plan, verify.RunOptions) verify.Report
	runSelfVerify        func(context.Context, verify.Plan, selfverify.Options) selfverify.Report
	inspectChanges       func(context.Context, zerogit.InspectOptions) (zerogit.ChangeSummary, error)
	commitChanges        func(context.Context, zerogit.CommitOptions) (zerogit.CommitResult, error)
	runTUI               func(context.Context, tui.Options) int
	runEditor            func(string) error
	checkUpdate          func(context.Context, update.Options) (update.Result, error)
	now                  func() time.Time
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
		skillsDir: func() string {
			return skills.DefaultDir(nil)
		},
		newMCPStore: func() (*mcp.PermissionStore, error) {
			return mcp.NewPermissionStore(mcp.StoreOptions{})
		},
		newSandboxStore: func() (*sandbox.GrantStore, error) {
			return sandbox.NewGrantStore(sandbox.StoreOptions{})
		},
		selectSandboxBackend: sandbox.SelectBackend,
		registerMCPTools: func(ctx context.Context, registry *tools.Registry, cfg config.MCPConfig, options mcp.RegisterOptions) (mcpToolRuntime, error) {
			return mcp.RegisterTools(ctx, registry, cfg, options)
		},
		prepareWorktree:  worktrees.Prepare,
		detectVerifyPlan: verify.DetectPlan,
		runVerify:        verify.Run,
		runSelfVerify:    selfverify.Run,
		inspectChanges:   zerogit.Inspect,
		commitChanges:    zerogit.Commit,
		runTUI:           tui.Run,
		runEditor:        openEditor,
		checkUpdate:      update.Check,
		now:              time.Now,
	}
}

func userAgent() string {
	return "zero/" + version
}

func runWithDeps(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) (exitCode int) {
	// Convert an unexpected panic anywhere in the CLI into a saved crash report and
	// a brief notice, rather than a raw stack trace dumped at the user.
	defer observability.Recover(observability.DefaultCrashDir(), "cli", stderr, &exitCode)
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
	case "sessions", "session":
		return runSessions(args[1:], stdout, stderr, deps)
	case "specialists", "specialist":
		return runSpecialists(args[1:], stdout, stderr, deps)
	case "plugins", "plugin":
		return runPlugins(args[1:], stdout, stderr, deps)
	case "skills", "skill":
		return runSkills(args[1:], stdout, stderr, deps)
	case "hooks":
		return runHooks(args[1:], stdout, stderr, deps)
	case "mcp":
		return runMCP(args[1:], stdout, stderr, deps)
	case "sandbox":
		return runSandbox(args[1:], stdout, stderr, deps)
	case "update":
		return runUpdate(args[1:], stdout, stderr, deps)
	case "worktrees", "worktree":
		return runWorktrees(args[1:], stdout, stderr, deps)
	case "verify":
		return runVerifyCommand(args[1:], stdout, stderr, deps)
	case "changes", "change":
		return runChanges(args[1:], stdout, stderr, deps)
	case "serve":
		return runServe(args[1:], stdout, stderr, deps)
	case "zeroline":
		return runZeroline(args[1:], stdout, stderr, deps)
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
	if deps.skillsDir == nil {
		deps.skillsDir = defaults.skillsDir
	}
	if deps.newMCPStore == nil {
		deps.newMCPStore = defaults.newMCPStore
	}
	if deps.newSandboxStore == nil {
		deps.newSandboxStore = defaults.newSandboxStore
	}
	if deps.selectSandboxBackend == nil {
		deps.selectSandboxBackend = defaults.selectSandboxBackend
	}
	if deps.registerMCPTools == nil {
		deps.registerMCPTools = defaults.registerMCPTools
	}
	if deps.prepareWorktree == nil {
		deps.prepareWorktree = defaults.prepareWorktree
	}
	if deps.detectVerifyPlan == nil {
		deps.detectVerifyPlan = defaults.detectVerifyPlan
	}
	if deps.runVerify == nil {
		deps.runVerify = defaults.runVerify
	}
	if deps.runSelfVerify == nil {
		deps.runSelfVerify = defaults.runSelfVerify
	}
	if deps.inspectChanges == nil {
		deps.inspectChanges = defaults.inspectChanges
	}
	if deps.commitChanges == nil {
		deps.commitChanges = defaults.commitChanges
	}
	if deps.runTUI == nil {
		deps.runTUI = defaults.runTUI
	}
	if deps.runEditor == nil {
		deps.runEditor = defaults.runEditor
	}
	if deps.checkUpdate == nil {
		deps.checkUpdate = defaults.checkUpdate
	}
	if deps.now == nil {
		deps.now = defaults.now
	}
	return deps
}

func runInteractiveTUI(stderr io.Writer, deps appDeps) int {
	return runInteractiveTUIWithSkin(stderr, deps, "")
}

func runInteractiveTUIWithSkin(stderr io.Writer, deps appDeps, skin string) int {
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
	specialistRuntime, err := registerSpecialistTools(registry, workspaceRoot)
	if err != nil {
		return writeAppError(stderr, "failed to initialize specialist tools: "+err.Error(), 1)
	}
	defer closeSpecialistRuntime(stderr, specialistRuntime)
	mcpRuntime, err := registerMCPToolsForWorkspace(context.Background(), workspaceRoot, registry, deps, mcp.AutonomyLow)
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}
	defer closeMCPRuntime(stderr, mcpRuntime)
	sandboxStore, err := deps.newSandboxStore()
	if err != nil {
		return writeAppError(stderr, "failed to initialize sandbox grants: "+err.Error(), 1)
	}
	sandboxEngine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspaceRoot,
		Policy:        sandbox.DefaultPolicy(),
		Store:         sandboxStore,
		Backend:       deps.selectSandboxBackend(sandbox.BackendOptions{}),
	})
	// Ask (not Auto) is the interactive default: in Auto, ToolAdvertised exposes
	// only PermissionAllow tools, so prompt-gated tools (write_file/edit_file/bash/
	// apply_patch) would never be offered to the model — the TUI could neither edit
	// files nor run shell. Ask advertises them and routes each through the existing
	// OnPermissionRequest flow; shift+tab lets the user switch modes live.
	permissionMode := agent.PermissionModeAsk
	return deps.runTUI(context.Background(), tui.Options{
		Cwd:             workspaceRoot,
		ProviderName:    resolved.Provider.Name,
		ModelName:       resolved.Provider.Model,
		ProviderProfile: resolved.Provider,
		Provider:        provider,
		NewProvider:     deps.newProvider,
		Registry:        registry,
		SessionStore:    deps.newSessionStore(),
		SandboxStore:    sandboxStore,
		AgentOptions: agent.Options{
			MaxTurns:       resolved.MaxTurns,
			Registry:       registry,
			PermissionMode: permissionMode,
			Autonomy:       string(sandbox.AutonomyLow),
			Sandbox:        sandboxEngine,
		},
		PermissionMode: permissionMode,
		Skin:           skin,
		ThemeDark:      true,
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

func registerSpecialistTools(registry *tools.Registry, workspaceRoot string) (*specialist.Runtime, error) {
	paths, err := specialist.DefaultPaths(workspaceRoot)
	if err != nil {
		return nil, err
	}
	return specialist.RegisterTools(registry, specialist.Executor{Paths: paths})
}

func shouldRegisterExecSpecialistTools(options execOptions) bool {
	if strings.EqualFold(strings.TrimSpace(options.tag), specialist.SessionTagSpecialist) {
		return false
	}
	return options.skipPermissionsUnsafe || strings.EqualFold(strings.TrimSpace(options.autonomy), "high")
}

func closeMCPRuntime(stderr io.Writer, runtime mcpToolRuntime) {
	if runtime == nil {
		return
	}
	if err := runtime.Close(); err != nil {
		_, _ = fmt.Fprintf(stderr, "[zero] mcp_close_error: %s\n", err)
	}
}

func closeSpecialistRuntime(stderr io.Writer, runtime *specialist.Runtime) {
	if runtime == nil {
		return
	}
	if err := runtime.Close(); err != nil {
		_, _ = fmt.Fprintf(stderr, "[zero] specialist_cleanup_error: %s\n", err)
	}
}

func writeAppError(stderr io.Writer, message string, exitCode int) int {
	if _, err := fmt.Fprintf(stderr, "[zero] %s\n", message); err != nil {
		return 1
	}
	return exitCode
}

func writeUsageError(stderr io.Writer, message string) int {
	return writeExecUsageError(stderr, message)
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
  sessions   Inspect local Zero session lineage
  specialist Manage local Zero specialist profiles
  plugins    Inspect local Zero plugin manifests
  skills     Inspect local Zero skills
  hooks      Inspect Zero hook configuration
  mcp        Manage MCP backend settings
  sandbox    Inspect sandbox policy and persistent grants
  update     Check for Zero CLI updates
  worktrees  Prepare isolated git worktrees
  verify     Detect and run local verification checks
  changes    Inspect and commit local git changes
  serve      Run Zero protocol servers
  zeroline    Launch the interactive TUI with the Zeroline reskin
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
      --mode <name>                  Apply a preset (smart, deep, fast, large, precise); explicit flags override it
  -m, --model <model>                Select the model for provider setup
      --max-turns <number>           Override the maximum agent loop turns
      --auto <low|medium|high>       Set exec autonomy; high enables unsafe tools
      --enabled-tools <tools>        Only expose these comma or space separated tools
      --disabled-tools <tools>       Hide these comma or space separated tools
      --list-tools                   List model-visible tools and exit
      --profile <profile>            Accept legacy model profile selection
  -r, --reasoning-effort <effort>    Accept legacy reasoning effort selection
  -C, --cwd <path>                   Set the workspace directory
  -w, --worktree [name]              Run from an isolated git worktree
      --worktree-dir <path>          Base directory for created worktrees
  -i, --input-format text|stream-json
                                    Select prompt input format
  -o, --output-format text|json|stream-json
                                    Select text, JSON, or schema-versioned JSONL output
                                    ("debug" is accepted as a stream-json alias)
      --prompt <prompt>              Provide prompt text as a flag
      --resume [id]                  Resume a session; omit id to use the latest
      --fork <id>                    Fork an existing session into a new session
      --calling-session-id <id>      Parent session id for specialist child runs
      --calling-tool-use-id <id>     Parent tool-call id for specialist child runs
      --tag <tag>                    Attach runtime tag metadata to the exec run
      --depth <number>               Set specialist nesting depth metadata
      --session-title <text>         Set the created session title
      --init-session-id <id>         Create a new exec session with this id
      --skip-permissions-unsafe      Allow prompt-gated tools without approval
`)
	return err
}
