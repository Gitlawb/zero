package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/providerhealth"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/usage"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Options configures the reusable Zero terminal UI shell.
type Options struct {
	Cwd                    string
	UserConfigPath         string
	DoctorUserConfigPath   string
	ProjectConfigPath      string
	ProviderName           string
	ModelName              string
	ProviderProfile        config.ProviderProfile
	SavedProviders         []config.ProviderProfile // all configured providers, for the /model multi-provider list
	FavoriteModels         []string
	RecapsEnabled          bool
	Provider               zeroruntime.Provider
	NewProvider            func(config.ProviderProfile) (zeroruntime.Provider, error)
	ProbeProviderHealth    func(context.Context, providerhealth.Options) providerhealth.Result
	DiscoverProviderModels func(context.Context, config.ProviderProfile) ([]providermodeldiscovery.Model, error)
	RuntimeMessageSink     func(tea.Msg)
	Registry               *tools.Registry
	SessionStore           *sessions.Store
	SandboxStore           *sandbox.GrantStore
	MCPConfig              config.MCPConfig
	MCPPermissionStore     *mcp.PermissionStore
	MCPTokenStore          *mcp.TokenStore
	MCPCommand             func(context.Context, []string) MCPCommandResult
	SandboxSetupCommand    func(context.Context) SandboxSetupCommandResult
	UsageTracker           *usage.Tracker
	SessionCompactor       SessionCompactor
	PrService              *PrService

	AgentOptions    agent.Options
	PermissionMode  agent.PermissionMode
	ReasoningEffort modelregistry.ReasoningEffort
	ResponseStyle   string
	// Theme is the operator's palette preference: "auto" (default), "dark", or
	// "light". Set from the --theme flag; falls back to ZERO_THEME then auto.
	Theme     string
	UserAgent string

	// Notify configures completion / awaiting-input notifications.
	Notify config.NotifyConfig

	// AltScreen tells the model it is running inside Bubble Tea's alternate
	// screen. Run sets this for the interactive app; tests can leave it false
	// to exercise the native scrollback renderer.
	AltScreen bool

	// Setup configures the first-run/setup takeover. It is shown before the
	// normal chat surface when Visible is true.
	Setup SetupOptions
}

type MCPCommandResult struct {
	Config   config.MCPConfig
	Output   string
	Error    string
	ExitCode int
}

type SandboxSetupCommandResult struct {
	Output   string
	Error    string
	ExitCode int
}

// SetupOptions configures the guided first-run provider setup takeover.
type SetupOptions struct {
	Visible    bool
	Required   bool
	ConfigPath string
	Providers  []SetupProviderOption
	Save       func(SetupSelection) (SetupResult, error)
}

// SetupProviderOption is one provider choice offered by the setup takeover.
type SetupProviderOption struct {
	ID           string
	Name         string
	DefaultModel string
	EnvVar       string
	RequiresAuth bool
	Local        bool
}

// SetupSelection is the user's setup choice.
type SetupSelection struct {
	CatalogID string
	Name      string
	BaseURL   string
	Model     string
	APIKey    string
}

// SetupResult describes a completed setup write.
type SetupResult struct {
	ConfigPath string
	Provider   config.ProviderProfile
}
