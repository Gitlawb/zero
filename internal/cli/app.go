package cli

import (
	"context"
	"errors"
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
	"github.com/Gitlawb/zero/internal/providerhealth"
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
	userConfigPath       func() (string, error)
	resolveConfig        func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error)
	resolveMCPConfig     func(workspaceRoot string) (config.MCPConfig, error)
	newProvider          func(config.ProviderProfile) (zeroruntime.Provider, error)
	probeProviderHealth  func(context.Context, providerhealth.Options) providerhealth.Result
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
		getwd:          os.Getwd,
		stdin:          os.Stdin,
		userConfigPath: config.DefaultUserConfigPath,
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
		probeProviderHealth: providerhealth.Probe,
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

	addDirs, args, err := splitLeadingAddDirFlags(args)
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}

	if len(args) == 0 {
		return runInteractiveTUI(stderr, deps, agent.PermissionModeAsk, addDirs)
	}

	// --add-dir grants an extra write root, and only the interactive TUI and
	// exec dispatch paths consume one. Fail loud everywhere else rather than
	// silently discarding an explicit grant — including help/version, which
	// run no agent and could only ignore it. The allowlist names exactly the
	// cases below that forward addDirs; a future subcommand is rejected by
	// default until it opts in here.
	if len(addDirs) > 0 {
		switch args[0] {
		case "--skip-permissions-unsafe", "-p", "--prompt", "exec":
			// Forwarded by the matching case below.
		default:
			return writeAppError(stderr, "--add-dir is only supported for the interactive TUI and exec", 1)
		}
	}

	switch args[0] {
	case "--skip-permissions-unsafe":
		// Launch the interactive TUI directly in unsafe mode. Without this, the
		// flag fell through to the unknown-command path, so a user could never
		// reach unsafe mode in the shell — and the "!" shell escape (which is
		// gated behind unsafe) was therefore unreachable.
		//
		// --add-dir may legally appear on either side of the flag, so re-split
		// the remaining args and merge with the dirs already collected. Any
		// trailing non-flag args were ignored on this path before --add-dir
		// existed and still are — but an --add-dir hidden BEHIND one would be
		// silently dropped with them, so reject that misplacement loudly.
		moreDirs, rest, err := splitLeadingAddDirFlags(args[1:])
		if err != nil {
			return writeAppError(stderr, err.Error(), 1)
		}
		for _, arg := range rest {
			if arg == "--add-dir" || strings.HasPrefix(arg, "--add-dir=") {
				return writeAppError(stderr, "--add-dir must come before any other arguments (it may precede or follow --skip-permissions-unsafe)", 1)
			}
		}
		return runInteractiveTUI(stderr, deps, agent.PermissionModeUnsafe, append(append([]string{}, addDirs...), moreDirs...))
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
		// Forward leading --add-dir occurrences so exec's own parser collects them.
		execArgs := append(addDirFlagArgs(addDirs), "--prompt", args[1])
		execArgs = append(execArgs, args[2:]...)
		return runExec(execArgs, stdout, stderr, deps)
	case "exec":
		// Forward leading --add-dir occurrences so exec's own parser collects them.
		return runExec(append(addDirFlagArgs(addDirs), args[1:]...), stdout, stderr, deps)
	case "config":
		return runConfig(args[1:], stdout, stderr, deps)
	case "models":
		return runModels(args[1:], stdout, stderr)
	case "providers":
		return runProviders(args[1:], stdout, stderr, deps)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr, deps)
	case "setup":
		return runSetup(args[1:], stdout, stderr, deps)
	case "context":
		return runContext(args[1:], stdout, stderr, deps)
	case "repo-map", "repomap":
		return runRepoMap(args[1:], stdout, stderr, deps)
	case "search", "find":
		return runSearch(args[1:], stdout, stderr, deps)
	case "sessions", "session":
		return runSessions(args[1:], stdout, stderr, deps)
	case "spec":
		return runSpec(args[1:], stdout, stderr, deps)
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
	case "usage":
		return runUsage(args[1:], stdout, stderr, deps)
	case "cron":
		return runCron(args[1:], stdout, stderr, deps)
	case "repo-info", "repoinfo":
		return runRepoInfo(args[1:], stdout, stderr, deps)
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
	if deps.userConfigPath == nil {
		deps.userConfigPath = defaults.userConfigPath
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
	if deps.probeProviderHealth == nil {
		deps.probeProviderHealth = defaults.probeProviderHealth
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

func runInteractiveTUI(stderr io.Writer, deps appDeps, permissionMode agent.PermissionMode, addDirs []string) int {
	return runInteractiveTUIWithSetup(stderr, deps, permissionMode, addDirs, false)
}

func runInteractiveTUIWithSetup(stderr io.Writer, deps appDeps, permissionMode agent.PermissionMode, addDirs []string, forceSetup bool) int {
	workspaceRoot, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), 1)
	}

	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}
	userConfigPath, err := deps.userConfigPath()
	if err != nil {
		return writeAppError(stderr, "failed to resolve user config path: "+err.Error(), 1)
	}

	needsSetup := setupRequired(resolved)
	setupVisible := forceSetup || needsSetup
	configPath := ""
	if setupVisible {
		configPath, err = deps.userConfigPath()
		if err != nil {
			return writeAppError(stderr, err.Error(), 1)
		}
	}

	scope, err := sandbox.NewScope(workspaceRoot, append(append([]string{}, resolved.Sandbox.AdditionalWriteRoots...), addDirs...))
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}

	provider, err := buildProvider(resolved, deps)
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}

	registry := newCoreRegistryScoped(workspaceRoot, scope)
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
	// Ask (not Auto) is the interactive default: in Auto, ToolAdvertised exposes
	// only PermissionAllow tools, so prompt-gated tools (write_file/edit_file/bash/
	// apply_patch) would never be offered to the model — the TUI could neither edit
	// files nor run shell. Ask advertises them and routes each through the existing
	// OnPermissionRequest flow; shift+tab lets the user switch modes live. An
	// explicit --skip-permissions-unsafe launch overrides this to unsafe (the only
	// way to reach unsafe, since shift+tab deliberately cycles auto↔ask only).
	//
	// Resolve the effective mode BEFORE the deferral gate below so the registration
	// count uses the SAME permission mode the agent loop's partition will use; an
	// empty mode here would mis-gate prompt-advertised deferred tools.
	if permissionMode == "" {
		permissionMode = agent.PermissionModeAsk
	}
	// Activate deferred MCP-tool loading for the interactive run only when the
	// VISIBLE deferred-eligible count meets the resolved threshold, matching exec.
	// The registry is complete (core + specialist + MCP) here, so the count is
	// accurate; below threshold this is a no-op and the surface is unchanged. The
	// interactive surface applies no operator tool filters, so enabled/disabled are
	// nil — matching the AgentOptions below.
	registerToolSearchIfEligible(registry, resolved.Tools.DeferThreshold, permissionMode, nil, nil)
	sandboxStore, err := deps.newSandboxStore()
	if err != nil {
		return writeAppError(stderr, "failed to initialize sandbox grants: "+err.Error(), 1)
	}
	sandboxEngine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspaceRoot,
		Policy:        applyConfiguredSandboxPolicy(sandbox.DefaultPolicy(), resolved.Sandbox),
		Store:         sandboxStore,
		Backend:       deps.selectSandboxBackend(sandbox.BackendOptions{}),
		Scope:         scope,
	})
	return deps.runTUI(context.Background(), tui.Options{
		Cwd:             workspaceRoot,
		UserConfigPath:  userConfigPath,
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
			FileTracker:    tools.NewFileTracker(),
			Hooks:          newHookDispatcher(workspaceRoot),
			DeferThreshold: resolved.Tools.DeferThreshold,
		},
		PermissionMode: permissionMode,
		Notify:         resolved.Notify,
		Setup: tui.SetupOptions{
			Visible:    setupVisible,
			Required:   needsSetup,
			ConfigPath: configPath,
			Providers:  setupProviderOptions(),
			Save: func(selection tui.SetupSelection) (tui.SetupResult, error) {
				return saveSetupProvider(deps, selection, setupSaveOptions{})
			},
		},
	})
}

func buildProvider(resolved config.ResolvedConfig, deps appDeps) (zeroruntime.Provider, error) {
	if !config.HasProviderProfile(resolved.Provider) {
		return nil, nil
	}
	return deps.newProvider(resolved.Provider)
}

func newCoreRegistry(workspaceRoot string) *tools.Registry {
	return newCoreRegistryScoped(workspaceRoot, nil)
}

func newCoreRegistryScoped(workspaceRoot string, scope tools.PathScope) *tools.Registry {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreToolsScoped(workspaceRoot, scope) {
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
	if options.useSpec {
		return false
	}
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
  setup      Guide first-run provider setup
  config     Inspect resolved Go configuration without leaking secrets
  models     List Zero model registry entries
  providers  Inspect resolved provider profiles
  doctor     Run backend health checks for config and provider setup
  context    Report workspace context budget usage
  repo-map   Build a deterministic repository map for agent context
  search     Search persisted local Zero session events
  find       Alias for search
  sessions   Inspect local Zero session lineage
  spec       Review and approve saved spec-mode drafts
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
  usage      Summarize token usage and estimated cost
  cron       Schedule agent jobs (foreground, file-backed)
  repo-info  Characterize the current repository (local git only)
  serve      Run Zero protocol servers
  help       Show this help
  version    Print version

Flags:
  -h, --help                     Show this help
  -v, --version                  Print version
  -p, --prompt                   Run a one-shot prompt
      --add-dir <path>           Allow writes in an extra directory (repeatable)
      --skip-permissions-unsafe  Launch the interactive shell in unsafe mode (enables the ! shell escape)
`)
	return err
}

// addDirFlagArgs rebuilds "--add-dir <dir>" flag pairs so dirs collected by
// splitLeadingAddDirFlags can be forwarded into exec's own argument parser
// (which accepts --add-dir anywhere) instead of being silently dropped.
func addDirFlagArgs(addDirs []string) []string {
	flags := make([]string, 0, 2*len(addDirs))
	for _, dir := range addDirs {
		flags = append(flags, "--add-dir", dir)
	}
	return flags
}

// splitLeadingAddDirFlags strips leading --add-dir flags from the root
// argument list (zero --add-dir <path> [--add-dir <path>] [subcommand …]).
// Subcommands like exec parse their own --add-dir occurrences.
func splitLeadingAddDirFlags(args []string) ([]string, []string, error) {
	addDirs := []string{}
	for len(args) > 0 {
		switch {
		case args[0] == "--add-dir":
			if len(args) < 2 {
				return nil, nil, errors.New("--add-dir requires a directory path")
			}
			value := strings.TrimSpace(args[1])
			if value == "" {
				return nil, nil, errors.New("--add-dir requires a directory path")
			}
			if strings.HasPrefix(value, "-") {
				return nil, nil, errors.New("--add-dir requires a directory path")
			}
			addDirs = append(addDirs, value)
			args = args[2:]
		case strings.HasPrefix(args[0], "--add-dir="):
			value := strings.TrimSpace(strings.TrimPrefix(args[0], "--add-dir="))
			if value == "" {
				return nil, nil, errors.New("--add-dir requires a directory path")
			}
			// Match the space form: a flag-like value is almost certainly a
			// mistyped option, not a directory. A directory literally named
			// "-foo" is still reachable as --add-dir=./-foo.
			if strings.HasPrefix(value, "-") {
				return nil, nil, errors.New("--add-dir requires a directory path")
			}
			addDirs = append(addDirs, value)
			args = args[1:]
		default:
			return addDirs, args, nil
		}
	}
	return addDirs, args, nil
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
      --image <path>                 Attach a local image (repeatable; vision models only)
      --add-dir <path>               Allow writes in an extra directory (repeatable)
      --mode <name>                  Apply a preset (smart, deep, fast, large, precise); explicit flags override it
  -m, --model <model>                Select the model for provider setup
      --use-spec                     Draft a spec first and stop for review
      --spec-model <model>           Override the draft model when --use-spec is set
      --spec-reasoning-effort <effort>
                                    Override draft reasoning effort when --use-spec is set
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
      --allow-escalation             Let the agent escalate to a stronger model mid-run via escalate_model
      --notify <off|bell|notify|both>
                                    Override notification mode for this run
      --no-notify                   Disable notifications for this run
`)
	return err
}
