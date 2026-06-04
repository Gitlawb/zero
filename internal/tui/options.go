package tui

import (
	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Options configures the reusable Zero terminal UI shell.
type Options struct {
	Cwd          string
	ProviderName string
	ModelName    string
	Provider     zeroruntime.Provider
	Registry     *tools.Registry

	AgentOptions   agent.Options
	PermissionMode agent.PermissionMode
}
