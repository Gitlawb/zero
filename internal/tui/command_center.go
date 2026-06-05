package tui

import (
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/doctor"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/providers"
	zsearch "github.com/Gitlawb/zero/internal/search"
)

func (m model) doctorText() string {
	report := doctor.Run(doctor.Options{
		Now:      m.now,
		Runtime:  "go",
		Provider: m.providerProfile,
	})
	return doctor.Format(report)
}

func (m model) searchText(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Search\nusage: /search <query>"
	}
	result, err := zsearch.Sessions(query, zsearch.Options{
		Store:        m.sessionStore,
		Limit:        5,
		ContextChars: 120,
		Now:          m.now,
	})
	if err != nil {
		return "Search\nerror: " + err.Error()
	}
	return zsearch.FormatResult(zsearch.RedactResult(result))
}

func (m model) resumeText(args string) string {
	args = strings.TrimSpace(args)
	if args != "" {
		return strings.Join([]string{
			"Sessions",
			"TUI resume hydration is not wired yet.",
			"Use zero exec --resume " + args + " for headless resume.",
		}, "\n")
	}
	sessions, err := m.sessionStore.List()
	if err != nil {
		return "Sessions\nerror: " + err.Error()
	}
	lines := []string{"Sessions"}
	if len(sessions) == 0 {
		return "Sessions\n  (none)"
	}
	limit := len(sessions)
	if limit > 8 {
		limit = 8
	}
	for index := 0; index < limit; index++ {
		session := sessions[index]
		title := displayValue(session.Title, "untitled")
		lines = append(lines, fmt.Sprintf("  %s  %s  model=%s provider=%s events=%d updated=%s", session.SessionID, title, displayValue(session.ModelID, "none"), displayValue(session.Provider, "none"), session.EventCount, session.UpdatedAt))
	}
	if len(sessions) > limit {
		lines = append(lines, fmt.Sprintf("  ... %d more", len(sessions)-limit))
	}
	return strings.Join(lines, "\n")
}

func (m model) handleModelCommand(args string) (model, string) {
	args = strings.TrimSpace(args)
	switch strings.ToLower(args) {
	case "":
		return m, m.modelText(args)
	case "list", "ls":
		return m, m.modelListText()
	}
	if m.pending {
		return m, "Model\nCannot switch models while a run is active."
	}

	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return m, "Model\nFailed to load model catalog: " + err.Error()
	}
	entry, err := registry.Require(args)
	if err != nil {
		return m, "Model\n" + err.Error()
	}
	if m.providerProfile == (config.ProviderProfile{}) {
		return m, "Model\nNo provider profile is available for TUI model switching."
	}
	if m.newProvider == nil {
		return m, "Model\nProvider rebuild is not available for this TUI session."
	}

	nextProfile := m.providerProfile
	nextProfile.Model = entry.ID
	metadata, err := providers.ResolveRuntimeMetadata(nextProfile, providers.Options{})
	if err != nil {
		return m, "Model\n" + err.Error()
	}

	nextProvider, err := m.newProvider(nextProfile)
	if err != nil {
		return m, "Model\n" + err.Error()
	}

	m.providerProfile = nextProfile
	m.provider = nextProvider
	m.providerName = displayValue(nextProfile.Name, string(metadata.ProviderKind))
	m.modelName = entry.ID
	return m, strings.Join([]string{
		"Model",
		"Switched model for this TUI session.",
		"model: " + entry.ID,
		"provider: " + string(metadata.ProviderKind),
		"api model: " + metadata.APIModel,
	}, "\n")
}

func apiKeyState(set bool) string {
	if set {
		return "set"
	}
	return "not set"
}
