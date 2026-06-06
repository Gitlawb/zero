package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/usage"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Options configures the reusable Zero terminal UI shell.
type Options struct {
	Cwd                string
	ProviderName       string
	ModelName          string
	ProviderProfile    config.ProviderProfile
	Provider           zeroruntime.Provider
	NewProvider        func(config.ProviderProfile) (zeroruntime.Provider, error)
	RuntimeMessageSink func(tea.Msg)
	Registry           *tools.Registry
	SessionStore       *sessions.Store
	SandboxStore       *sandbox.GrantStore
	UsageTracker       *usage.Tracker

	AgentOptions    agent.Options
	PermissionMode  agent.PermissionMode
	ReasoningEffort modelregistry.ReasoningEffort
	ResponseStyle   string
}
