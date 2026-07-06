package cli

import (
	"context"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providers"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// summarizerFactory adapts the resolved profile and the authenticated provider
// builder into agent.Options.Summarizer: a lazy constructor for the cheap
// compaction-summarization provider (see providers.CompactionModelID for the
// selection rules). nil when no dedicated model applies — the agent loop then
// summarizes on the main provider, exactly as before.
func summarizerFactory(resolved config.ResolvedConfig, newProvider func(config.ProviderProfile) (zeroruntime.Provider, error)) func(context.Context) (agent.Provider, error) {
	modelID := providers.CompactionModelID(resolved.Provider, resolved.Preferences.CompactionModel)
	if modelID == "" || newProvider == nil {
		return nil
	}
	profile := resolved.Provider
	profile.Model = modelID
	return func(context.Context) (agent.Provider, error) {
		return newProvider(profile)
	}
}
