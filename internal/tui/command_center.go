package tui

import (
	"fmt"
	"strconv"
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
		return renderCommandOutput(commandOutput{
			Title:  "Sessions",
			Status: commandStatusInfo,
			Sections: []commandSection{{
				Title: "Resume",
				Lines: []string{"requested session: " + args},
			}},
			Hints: []string{"use /resume " + args + " to hydrate this TUI session"},
		})
	}
	sessions, err := m.sessionStore.List()
	if err != nil {
		return renderCommandOutput(commandOutput{
			Title:  "Sessions",
			Status: commandStatusBlocked,
			Sections: []commandSection{{
				Title: "Store",
				Lines: []string{"error: " + err.Error()},
			}},
		})
	}
	if len(sessions) == 0 {
		return renderCommandOutput(commandOutput{
			Title:  "Sessions",
			Status: commandStatusInfo,
			Sections: []commandSection{{
				Title: "Recent",
				Lines: []string{"none"},
			}},
		})
	}
	limit := len(sessions)
	if limit > 8 {
		limit = 8
	}
	lines := []string{fmt.Sprintf("recent sessions: %d", len(sessions))}
	for index := 0; index < limit; index++ {
		session := sessions[index]
		title := displayValue(session.Title, "untitled")
		lines = append(lines, commandBullet(fmt.Sprintf("%s  %s  model=%s provider=%s events=%d updated=%s", session.SessionID, title, displayValue(session.ModelID, "none"), displayValue(session.Provider, "none"), session.EventCount, session.UpdatedAt)))
	}
	if len(sessions) > limit {
		lines = append(lines, fmt.Sprintf("... %d more", len(sessions)-limit))
	}
	return renderCommandOutput(commandOutput{
		Title:  "Sessions",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "Recent",
			Lines: lines,
		}},
		Hints: []string{"use /resume latest or /resume <id> to load a session"},
	})
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
	entry, notice, ok := registry.ResolveWithFallback(args)
	if !ok {
		return m, "Model\nunknown Zero model " + strconv.Quote(args)
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
	resetEffort := false
	if m.reasoningEffort != "" && !reasoningEffortAllowed(entry.ReasoningEfforts, m.reasoningEffort) {
		// Drop an unsupported carry-over preference and fall back to the
		// model's effective default for the new model.
		m.reasoningEffort = ""
		resetEffort = true
	}
	effortLine := "effort: " + m.effortDisplay()
	if resetEffort {
		// Preference was dropped: show "auto" (model default applies), not a
		// concrete value that would read as an explicit setting.
		effortLine += " (unsupported preference reset)"
	} else if effective := modelregistry.EffectiveReasoningEffort(entry, m.reasoningEffort); effective != modelregistry.ReasoningEffortNone {
		effortLine = "effort: " + string(effective)
	}
	lines := []string{"Model"}
	if notice != "" {
		lines = append(lines, notice)
	}
	lines = append(lines,
		"Switched model for this TUI session.",
		"model: "+entry.ID,
		"provider: "+string(metadata.ProviderKind),
		"api model: "+metadata.APIModel,
		effortLine,
	)
	return m, strings.Join(lines, "\n")
}

// handleModeCommand applies a preset that bundles model, reasoning effort, and
// turn budget. "/mode" with no argument lists the presets; "/mode <name>"
// switches the active model (rebuilding the provider, like /model), the reasoning
// effort (like /effort), and the agent-loop turn budget for this TUI session. It
// mirrors the state mutations in handleModelCommand/handleEffortCommand so a mode
// switch is equivalent to running those commands in sequence.
func (m model) handleModeCommand(args string) (model, string) {
	args = strings.TrimSpace(args)
	switch strings.ToLower(args) {
	case "":
		return m, m.modeListText()
	case "list", "ls":
		return m, m.modeListText()
	}

	mode, ok := modelregistry.LookupMode(args)
	if !ok {
		return m, "Mode\nunknown mode " + strconv.Quote(args) + "\navailable: " + strings.Join(modelregistry.ModeNames(), ", ")
	}
	if m.pending {
		return m, "Mode\nCannot switch modes while a run is active."
	}

	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return m, "Mode\nFailed to load model catalog: " + err.Error()
	}
	entry, notice, ok := registry.ResolveWithFallback(mode.Model)
	if !ok {
		return m, "Mode\nmode " + strconv.Quote(mode.Name) + " references unknown model " + strconv.Quote(mode.Model)
	}
	if m.providerProfile == (config.ProviderProfile{}) {
		return m, "Mode\nNo provider profile is available for TUI mode switching."
	}
	if m.newProvider == nil {
		return m, "Mode\nProvider rebuild is not available for this TUI session."
	}

	nextProfile := m.providerProfile
	nextProfile.Model = entry.ID
	metadata, err := providers.ResolveRuntimeMetadata(nextProfile, providers.Options{})
	if err != nil {
		return m, "Mode\n" + err.Error()
	}
	nextProvider, err := m.newProvider(nextProfile)
	if err != nil {
		return m, "Mode\n" + err.Error()
	}

	m.providerProfile = nextProfile
	m.provider = nextProvider
	m.providerName = displayValue(nextProfile.Name, string(metadata.ProviderKind))
	m.modelName = entry.ID

	// Apply the mode's reasoning effort when the resolved model supports it;
	// otherwise fall back to auto (the model's effective default) so we never
	// store an unsupported preference.
	effortLine := "effort: auto"
	if mode.Effort != "" && reasoningEffortAllowed(entry.ReasoningEfforts, mode.Effort) {
		m.reasoningEffort = mode.Effort
		effortLine = "effort: " + string(mode.Effort)
	} else {
		m.reasoningEffort = ""
		if mode.Effort != "" {
			effortLine = "effort: auto (mode effort unsupported by model)"
		}
	}

	turnsLine := fmt.Sprintf("max turns: %d (unchanged)", m.agentOptions.MaxTurns)
	if mode.MaxTurns > 0 {
		m.agentOptions.MaxTurns = mode.MaxTurns
		turnsLine = fmt.Sprintf("max turns: %d", mode.MaxTurns)
	}

	lines := []string{"Mode"}
	if notice != "" {
		lines = append(lines, notice)
	}
	lines = append(lines,
		"Switched to mode "+mode.Name+" for this TUI session.",
		mode.Description,
		"model: "+entry.ID,
		"provider: "+string(metadata.ProviderKind),
		effortLine,
		turnsLine,
	)
	return m, strings.Join(lines, "\n")
}

func (m model) modeListText() string {
	lines := make([]string, 0, len(modelregistry.Modes()))
	for _, mode := range modelregistry.Modes() {
		detail := fmt.Sprintf("model=%s", mode.Model)
		if mode.Effort != "" {
			detail += " effort=" + string(mode.Effort)
		}
		if mode.MaxTurns > 0 {
			detail += fmt.Sprintf(" turns=%d", mode.MaxTurns)
		}
		lines = append(lines, commandBullet(fmt.Sprintf("%s - %s (%s)", mode.Name, mode.Description, detail)))
	}
	return renderCommandOutput(commandOutput{
		Title:  "Mode",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "Available",
			Lines: lines,
		}},
		Hints: []string{"use /mode <name> to switch model, effort, and turns"},
	})
}

func apiKeyState(set bool) string {
	if set {
		return "set"
	}
	return "not set"
}
