package tui

import (
	"context"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
	"github.com/Gitlawb/zero/internal/redaction"
)

type providerModelsDiscoveredMsg struct {
	providerID string
	models     []providermodeldiscovery.Model
	err        error
}

func (m model) advanceProviderWizard() (model, tea.Cmd) {
	if m.providerWizard == nil {
		return m, nil
	}
	previous := m.providerWizard.step
	m.providerWizard.advance()
	if m.providerWizard.step == providerWizardStepModel && previous != providerWizardStepModel {
		return m, m.providerModelDiscoveryCmd()
	}
	return m, nil
}

func (m model) providerModelDiscoveryCmd() tea.Cmd {
	wizard := m.providerWizard
	if wizard == nil {
		return nil
	}
	provider := wizard.currentProvider()
	if !providerWizardCatalogDiscoveryAllowed(provider) {
		return nil
	}
	profile := providerWizardDiscoveryProfile(provider, wizard.apiKey)
	discover := m.discoverProviderModels
	if discover == nil {
		discover = func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return providermodeldiscovery.DiscoverCatalog(ctx, provider, profile, providermodeldiscovery.Options{})
		}
	}

	wizard.modelLoading = true
	wizard.modelLoadError = ""
	providerID := provider.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
		defer cancel()
		models, err := discover(ctx, profile)
		return providerModelsDiscoveredMsg{providerID: providerID, models: models, err: err}
	}
}

func (m model) applyProviderModelsDiscovered(msg providerModelsDiscoveredMsg) model {
	wizard := m.providerWizard
	if wizard == nil || wizard.currentProvider().ID != msg.providerID {
		return m
	}
	wizard.modelLoading = false
	if msg.err != nil {
		wizard.modelLoadError = redaction.RedactString(msg.err.Error(), redaction.Options{ExtraSecretValues: []string{wizard.apiKey}})
		wizard.modelSource = "fallback"
		wizard.refreshModels()
		return m
	}
	models := providerWizardModelsFromDiscovery(msg.models)
	if len(models) == 0 {
		wizard.modelLoadError = "models endpoint returned no model ids"
		wizard.modelSource = "fallback"
		wizard.refreshModels()
		return m
	}
	wizard.models = models
	wizard.selectedModel = 0
	wizard.modelSource = providerWizardModelsSource(msg.models)
	if wizard.modelSource == "" {
		wizard.modelSource = "live"
	}
	wizard.modelLoadError = ""
	return m
}

func providerWizardModelsFromDiscovery(models []providermodeldiscovery.Model) []providerWizardModel {
	result := make([]providerWizardModel, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		result = append(result, providerWizardModel{
			ID:          id,
			Description: firstProviderDisplayValue(model.Description, "live model"),
			Meta:        providerWizardModelMeta(model.ContextWindow, model.ToolCall, model.Reasoning, model.InputCost, model.OutputCost, model.Tags),
		})
	}
	return result
}

func providerWizardModelsSource(models []providermodeldiscovery.Model) string {
	for _, model := range models {
		if source := strings.TrimSpace(model.Source); source != "" {
			return source
		}
	}
	return ""
}

func providerWizardDiscoveryProfile(provider providercatalog.Descriptor, apiKey string) config.ProviderProfile {
	profile := providerWizardProfile(provider, provider.DefaultModel, apiKey)
	if strings.TrimSpace(profile.APIKey) == "" && strings.TrimSpace(profile.APIKeyEnv) != "" {
		profile.APIKey = strings.TrimSpace(os.Getenv(profile.APIKeyEnv))
	}
	return profile
}

func providerWizardCatalogDiscoveryAllowed(provider providercatalog.Descriptor) bool {
	return providercatalog.RuntimeSupported(provider)
}
