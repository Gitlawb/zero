package tui

import (
	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/usage"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Options configures the reusable Zero terminal UI shell.
type Options struct {
	Cwd             string
	ProviderName    string
	ModelName       string
	ProviderProfile config.ProviderProfile
	Provider        zeroruntime.Provider
	NewProvider     func(config.ProviderProfile) (zeroruntime.Provider, error)
	Registry        *tools.Registry
	SessionStore    *sessions.Store
	UsageTracker    *usage.Tracker

	AgentOptions    agent.Options
	PermissionMode  agent.PermissionMode
	ReasoningEffort modelregistry.ReasoningEffort
	ResponseStyle   string
}
