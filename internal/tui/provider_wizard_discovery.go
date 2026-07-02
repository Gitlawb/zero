package tui

import (
	"context"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
	"github.com/Gitlawb/zero/internal/redaction"
)

type providerModelsDiscoveredMsg struct {
	providerID string
	token      int
	models     []providermodeldiscovery.Model
	err        error
	// secrets are redacted from any surfaced error (e.g. a resolved OAuth token
	// used to authenticate discovery, which must never be logged or shown).
	secrets []string
}

func (m model) advanceProviderWizard() (model, tea.Cmd) {
	if m.providerWizard == nil {
		return m, nil
	}
	// OAuth path: advancing from the OAuth provider list starts the browser/device
	// login instead of the key/endpoint flow.
	if m.providerWizard.step == providerWizardStepProvider && m.providerWizard.oauthMode && m.providerWizard.currentProvider().OAuth {
		provider := m.providerWizard.currentProvider()
		// Headless/SSH boxes can't open a browser — use device code there by
		// default (the user can also force it with "d" from the list).
		if provider.OAuthDeviceFlow && oauthPreferDeviceFlow() {
			return m.startProviderDeviceLogin()
		}
		attemptID := m.providerWizard.beginOAuthAttempt(false)
		return m, providerWizardOAuthCmdFor(provider, attemptID)
	}
	// A non-OAuth provider that already has a key in the credential store: offer
	// keep/replace/remove before re-entering credentials.
	if m.providerWizard.step == providerWizardStepProvider && !m.providerWizard.oauthMode {
		if name, ok := m.wizardProviderStoredKey(m.providerWizard.currentProvider()); ok {
			// Generic/custom providers (custom-openai-compatible etc.) all share
			// the same CatalogID — matching on CatalogID would block creating a
			// second instance. Skip ManageKey and fall through to the shared
			// advance() path below; the user can overwrite by re-entering the
			// same name or create a new one with a different name.
			if !providerWizardNeedsEndpoint(m.providerWizard.currentProvider()) {
				m.providerWizard.manageProviderName = name
				m.providerWizard.manageKeyCursor = 0
				m.providerWizard.err = ""
				m.providerWizard.step = providerWizardStepManageKey
				return m, nil
			}
			m.providerWizard.manageProviderName = ""
		}
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
	if providerWizardUsesTypedModel(provider) {
		return nil
	}
	pastedKey := wizard.apiKey
	// A token-login provider (e.g. xAI) stores its bearer in the OAuth store, not
	// as a pasted key; resolve it so /models is authenticated and the live list
	// shows after sign-in. (OpenRouter mints a key into wizard.apiKey already.)
	needOAuthToken := strings.TrimSpace(pastedKey) == "" && provider.OAuth && !provider.OAuthMintsKey
	discover := m.discoverProviderModels
	if discover == nil {
		discover = func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return providermodeldiscovery.DiscoverCatalog(ctx, provider, profile, providermodeldiscovery.Options{})
		}
	}

	wizard.modelLoading = true
	wizard.modelLoadError = ""
	wizard.discoveryToken++
	token := wizard.discoveryToken
	providerID := provider.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		apiKey := pastedKey
		if needOAuthToken {
			if resolved := oauthStoredToken(ctx, providerID); resolved != "" {
				apiKey = resolved
			}
		}
		profile := providerWizardDiscoveryProfile(provider, apiKey)
		models, err := discover(ctx, profile)
		return providerModelsDiscoveredMsg{providerID: providerID, token: token, models: models, err: err, secrets: []string{apiKey, profile.APIKey}}
	}
}

func (m model) applyProviderModelsDiscovered(msg providerModelsDiscoveredMsg) model {
	wizard := m.providerWizard
	if wizard == nil || wizard.step != providerWizardStepModel || wizard.currentProvider().ID != msg.providerID || msg.token != wizard.discoveryToken {
		return m
	}
	wizard.modelLoading = false
	if msg.err != nil {
		wizard.modelLoadError = redaction.RedactString(msg.err.Error(), redaction.Options{ExtraSecretValues: append([]string{wizard.apiKey}, msg.secrets...)})
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
	profile := providerWizardProfile(provider, provider.DefaultModel, apiKey, "", "")
	if strings.TrimSpace(profile.APIKey) == "" && strings.TrimSpace(profile.APIKeyEnv) != "" {
		profile.APIKey = strings.TrimSpace(os.Getenv(profile.APIKeyEnv))
	}
	return profile
}

func providerWizardCatalogDiscoveryAllowed(provider providercatalog.Descriptor) bool {
	return providercatalog.RuntimeSupported(provider)
}
