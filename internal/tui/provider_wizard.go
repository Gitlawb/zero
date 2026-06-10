package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/redaction"
)

const maxProviderWizardProvidersVisible = 10
const maxProviderWizardModelsVisible = 10

type providerWizardStep int

const (
	providerWizardStepProvider providerWizardStep = iota
	providerWizardStepCredential
	providerWizardStepModel
	providerWizardStepDone
)

type providerWizardModel struct {
	ID          string
	Description string
	Meta        string
}

type providerWizardState struct {
	step             providerWizardStep
	providers        []providercatalog.Descriptor
	selectedProvider int
	models           []providerWizardModel
	selectedModel    int
	modelSearch      string
	apiKey           string
	err              string
	modelSource      string
	modelLoading     bool
	modelLoadError   string
}

func (m model) newProviderWizard() *providerWizardState {
	providers := providerWizardProviders()
	selected := 0
	activeID := strings.TrimSpace(m.providerProfile.CatalogID)
	if activeID == "" {
		activeID = strings.TrimSpace(m.providerName)
	}
	for index, provider := range providers {
		if provider.ID == activeID {
			selected = index
			break
		}
	}

	wizard := &providerWizardState{
		step:             providerWizardStepProvider,
		providers:        providers,
		selectedProvider: selected,
	}
	wizard.refreshModels()
	return wizard
}

func providerWizardProviders() []providercatalog.Descriptor {
	providers := []providercatalog.Descriptor{}
	for _, descriptor := range providercatalog.All() {
		if !providercatalog.RuntimeSupported(descriptor) {
			continue
		}
		providers = append(providers, descriptor)
	}
	return providers
}

func (wizard *providerWizardState) currentProvider() providercatalog.Descriptor {
	if wizard == nil || len(wizard.providers) == 0 {
		return providercatalog.Descriptor{}
	}
	wizard.selectedProvider = clampInt(wizard.selectedProvider, 0, len(wizard.providers)-1)
	return wizard.providers[wizard.selectedProvider]
}

func (wizard *providerWizardState) currentModel() providerWizardModel {
	if wizard == nil {
		return providerWizardModel{}
	}
	wizard.refreshModels()
	models := wizard.filteredModels()
	if len(models) == 0 {
		provider := wizard.currentProvider()
		return providerWizardModel{ID: provider.DefaultModel, Description: "catalog default"}
	}
	wizard.selectedModel = clampInt(wizard.selectedModel, 0, len(models)-1)
	return models[wizard.selectedModel]
}

func (wizard *providerWizardState) move(delta int) {
	if wizard == nil {
		return
	}
	switch wizard.step {
	case providerWizardStepProvider:
		if len(wizard.providers) == 0 {
			return
		}
		wizard.selectedProvider = ((wizard.selectedProvider+delta)%len(wizard.providers) + len(wizard.providers)) % len(wizard.providers)
		wizard.selectedModel = 0
		wizard.modelSearch = ""
		wizard.apiKey = ""
		wizard.err = ""
		wizard.modelSource = ""
		wizard.modelLoading = false
		wizard.modelLoadError = ""
		wizard.refreshModels()
	case providerWizardStepModel:
		wizard.refreshModels()
		models := wizard.filteredModels()
		if len(models) == 0 {
			return
		}
		wizard.selectedModel = ((wizard.selectedModel+delta)%len(models) + len(models)) % len(models)
	}
}

func (wizard *providerWizardState) advance() {
	if wizard == nil {
		return
	}
	switch wizard.step {
	case providerWizardStepProvider:
		wizard.refreshModels()
		wizard.err = ""
		if providerWizardNeedsCredential(wizard.currentProvider()) {
			wizard.step = providerWizardStepCredential
		} else {
			wizard.step = providerWizardStepModel
		}
	case providerWizardStepCredential:
		wizard.err = ""
		wizard.step = providerWizardStepModel
	case providerWizardStepModel:
		wizard.err = ""
		wizard.step = providerWizardStepDone
	case providerWizardStepDone:
		wizard.step = providerWizardStepProvider
	}
}

func (wizard *providerWizardState) refreshModels() {
	if wizard == nil {
		return
	}
	provider := wizard.currentProvider()
	if wizard.modelSource != "" && wizard.modelSource != "fallback" {
		wizard.selectedModel = clampInt(wizard.selectedModel, 0, maxInt(0, len(wizard.models)-1))
		return
	}
	models := providerWizardModelOptions(provider)
	if sameProviderWizardModels(wizard.models, models) {
		wizard.selectedModel = clampInt(wizard.selectedModel, 0, maxInt(0, len(models)-1))
		return
	}
	wizard.models = models
	wizard.selectedModel = 0
	wizard.modelSource = "fallback"
}

func sameProviderWizardModels(a, b []providerWizardModel) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index].ID != b[index].ID {
			return false
		}
	}
	return true
}

func providerWizardNeedsCredential(provider providercatalog.Descriptor) bool {
	return provider.RequiresAuth && !provider.Local && len(provider.AuthEnvVars) > 0
}

func (m model) handleProviderWizardKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.providerWizard == nil {
		return m, nil
	}
	if m.providerWizard.step == providerWizardStepCredential {
		switch msg.Type {
		case tea.KeyEsc:
			m.providerWizard = nil
			return m, nil
		case tea.KeyRunes:
			m.providerWizard.appendAPIKey(msg.Runes)
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			m.providerWizard.deleteAPIKeyRune()
			return m, nil
		case tea.KeyCtrlU:
			m.providerWizard.apiKey = ""
			return m, nil
		case tea.KeyEnter:
			return m.advanceProviderWizard()
		}
		return m, nil
	}
	if m.providerWizard.step == providerWizardStepModel {
		switch msg.Type {
		case tea.KeyRunes:
			m.providerWizard.appendModelSearch(msg.Runes)
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			m.providerWizard.deleteModelSearchRune()
			return m, nil
		case tea.KeyCtrlU:
			m.providerWizard.modelSearch = ""
			m.providerWizard.selectedModel = 0
			return m, nil
		}
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.providerWizard = nil
	case tea.KeyUp:
		m.providerWizard.move(-1)
	case tea.KeyDown, tea.KeyTab:
		m.providerWizard.move(1)
	case tea.KeyEnter:
		if m.providerWizard.step == providerWizardStepDone {
			return m.applyProviderWizard()
		}
		return m.advanceProviderWizard()
	}
	return m, nil
}

func (wizard *providerWizardState) appendAPIKey(runes []rune) {
	for _, r := range runes {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			continue
		}
		wizard.apiKey += string(r)
	}
	wizard.err = ""
}

func (wizard *providerWizardState) deleteAPIKeyRune() {
	if wizard.apiKey == "" {
		return
	}
	runes := []rune(wizard.apiKey)
	wizard.apiKey = string(runes[:len(runes)-1])
	wizard.err = ""
}

func (wizard *providerWizardState) appendModelSearch(runes []rune) {
	for _, r := range runes {
		if unicode.IsControl(r) {
			continue
		}
		wizard.modelSearch += string(r)
	}
	wizard.selectedModel = 0
}

func (wizard *providerWizardState) deleteModelSearchRune() {
	if wizard.modelSearch == "" {
		return
	}
	runes := []rune(wizard.modelSearch)
	wizard.modelSearch = string(runes[:len(runes)-1])
	wizard.selectedModel = 0
}

func (m model) applyProviderWizard() (model, tea.Cmd) {
	wizard := m.providerWizard
	if wizard == nil {
		return m, nil
	}
	provider := wizard.currentProvider()
	modelChoice := wizard.currentModel()
	profile := providerWizardProfile(provider, modelChoice.ID, wizard.apiKey)
	if m.newProvider != nil {
		nextProvider, err := m.newProvider(profile)
		if err != nil {
			wizard.err = redaction.RedactString(err.Error(), redaction.Options{ExtraSecretValues: []string{profile.APIKey}})
			return m, nil
		}
		m.provider = nextProvider
	}
	if strings.TrimSpace(m.userConfigPath) != "" {
		if _, err := config.UpsertProvider(m.userConfigPath, profile, true); err != nil {
			wizard.err = redaction.RedactString(err.Error(), redaction.Options{ExtraSecretValues: []string{profile.APIKey}})
			return m, nil
		}
	}
	m.providerProfile = profile
	m.providerName = profile.Name
	m.modelName = profile.Model
	m.providerWizard = nil
	return m, nil
}

func (m model) providerWizardOverlay(width int) string {
	if m.providerWizard == nil {
		return ""
	}
	return m.providerWizard.render(width)
}

func (wizard *providerWizardState) render(width int) string {
	if wizard == nil {
		return ""
	}
	overlayWidth := minInt(maxInt(56, width-10), minInt(width, 92))
	innerWidth := maxInt(20, overlayWidth-4)

	lines := []string{
		zeroTheme.badge.Render(" PROVIDER ") + " " + zeroTheme.ink.Bold(true).Render("Provider setup"),
		zeroTheme.faint.Render(providerWizardStepLine(wizard.step)),
		"",
	}
	if wizard.err != "" {
		lines = append(lines, zeroTheme.red.Render("error: "+wizard.err), "")
	}
	switch wizard.step {
	case providerWizardStepProvider:
		lines = append(lines, wizard.renderProviderStep(innerWidth)...)
	case providerWizardStepCredential:
		lines = append(lines, wizard.renderCredentialStep(innerWidth)...)
	case providerWizardStepModel:
		lines = append(lines, wizard.renderModelStep(innerWidth)...)
	case providerWizardStepDone:
		lines = append(lines, wizard.renderDoneStep(innerWidth)...)
	}
	lines = append(lines, "", zeroTheme.faint.Render("↑/↓ select  ·  Enter continue  ·  Esc close"))

	block := styledBlockFill(overlayWidth, lines, zeroTheme.line, zeroTheme.panel)
	if width > overlayWidth {
		return indentBlock(block, (width-overlayWidth)/2)
	}
	return block
}

func providerWizardStepLine(step providerWizardStep) string {
	steps := []struct {
		step  providerWizardStep
		label string
	}{
		{providerWizardStepProvider, "1 provider"},
		{providerWizardStepCredential, "2 key"},
		{providerWizardStepModel, "3 model"},
		{providerWizardStepDone, "4 ready"},
	}
	parts := make([]string, 0, len(steps))
	for _, item := range steps {
		if item.step == step {
			parts = append(parts, "["+item.label+"]")
		} else {
			parts = append(parts, item.label)
		}
	}
	return strings.Join(parts, "  ")
}

func (wizard *providerWizardState) renderProviderStep(width int) []string {
	lines := []string{zeroTheme.accent.Render("Choose provider")}
	maxVisible := minInt(maxProviderWizardProvidersVisible, len(wizard.providers))
	start := selectableListStart(len(wizard.providers), maxVisible, wizard.selectedProvider)
	for offset, provider := range wizard.providers[start : start+maxVisible] {
		lines = append(lines, wizard.renderSelectableProvider(width, start+offset, provider))
	}
	if hidden := len(wizard.providers) - maxVisible; hidden > 0 {
		lines = append(lines, zeroTheme.faint.Render(fmt.Sprintf("  %d more providers in catalog", hidden)))
	}
	return lines
}

func (wizard *providerWizardState) renderSelectableProvider(width int, index int, provider providercatalog.Descriptor) string {
	selected := index == wizard.selectedProvider
	surface := zeroTheme.onPanel
	marker := surface(zeroTheme.faintest).Render("  ")
	if selected {
		surface = zeroTheme.onSel
		marker = surface(zeroTheme.accent).Render("❯ ")
	}
	auth := "local"
	if provider.RequiresAuth {
		auth = firstProviderDisplayValue(strings.Join(provider.AuthEnvVars, ","), "api key")
	}
	left := marker + surface(zeroTheme.ink).Render(provider.Name)
	right := surface(zeroTheme.faint).Render(provider.ID + " · " + auth)
	gap := width - lipglossWidth(left) - lipglossWidth(right)
	return fitStyledLine(left+surface(zeroTheme.ink).Render(strings.Repeat(" ", maxInt(1, gap)))+right, width)
}

func (wizard *providerWizardState) renderCredentialStep(width int) []string {
	provider := wizard.currentProvider()
	env := firstProviderDisplayValue(provider.AuthEnvVars...)
	command := providerWizardAddCommand(provider, "")
	value := zeroTheme.faint.Render("paste key here")
	if wizard.apiKey != "" {
		value = zeroTheme.ink.Render(maskedProviderWizardKey(wizard.apiKey)) + zeroTheme.faint.Render("  pasted key")
	}
	input := zeroTheme.userPrompt.Render("api key > ") + value + zeroTheme.accent.Render("▌")
	return []string{
		zeroTheme.accent.Render("Paste API key"),
		zeroTheme.ink.Render("Paste your key here, then press Enter."),
		zeroTheme.faint.Render("Leave empty to use " + env + " from your environment."),
		zeroTheme.onPanel2(zeroTheme.ink).Render(input),
		zeroTheme.faint.Render("Pasted keys are hidden. Ready saves the profile to Zero config."),
		zeroTheme.onPanel2(zeroTheme.ink).Render(command),
	}
}

func (wizard *providerWizardState) renderModelStep(width int) []string {
	lines := []string{zeroTheme.accent.Render("Choose model")}
	if status := wizard.modelStatusText(); status != "" {
		lines = append(lines, zeroTheme.faint.Render(status))
	}
	lines = append(lines, wizard.renderModelSearch(width))
	wizard.refreshModels()
	models := wizard.filteredModels()
	if len(models) == 0 {
		lines = append(lines, zeroTheme.faint.Render("  no matching models"))
		return lines
	}
	maxVisible := minInt(maxProviderWizardModelsVisible, len(models))
	wizard.selectedModel = clampInt(wizard.selectedModel, 0, len(models)-1)
	start := selectableListStart(len(models), maxVisible, wizard.selectedModel)
	for offset, model := range models[start : start+maxVisible] {
		lines = append(lines, wizard.renderSelectableModel(width, start+offset, model))
	}
	if hidden := len(models) - maxVisible; hidden > 0 {
		lines = append(lines, zeroTheme.faint.Render(fmt.Sprintf("  %d more models - type to search", hidden)))
	}
	return lines
}

func (wizard *providerWizardState) renderModelSearch(width int) string {
	query := strings.TrimSpace(wizard.modelSearch)
	value := zeroTheme.faint.Render("Search model")
	if query != "" {
		value = zeroTheme.ink.Render(query)
	}
	input := zeroTheme.userPrompt.Render("search > ") + value
	return fitStyledLine(zeroTheme.onPanel2(zeroTheme.ink).Render(input), width)
}

func (wizard *providerWizardState) modelStatusText() string {
	if wizard.modelLoading {
		return "models: refreshing catalog"
	}
	if wizard.modelLoadError != "" {
		return "models: fallback - " + wizard.modelLoadError
	}
	if wizard.modelSource == "fallback" {
		return "models: fallback"
	}
	return ""
}

func (wizard *providerWizardState) renderSelectableModel(width int, index int, model providerWizardModel) string {
	selected := index == wizard.selectedModel
	surface := zeroTheme.onPanel
	marker := surface(zeroTheme.faintest).Render("  ")
	if selected {
		surface = zeroTheme.onSel
		marker = surface(zeroTheme.accent).Render("❯ ")
	}
	left := marker + surface(zeroTheme.ink).Render(model.displayLabel())
	rightText := firstProviderDisplayValue(model.Meta, model.secondaryText())
	right := surface(zeroTheme.faint).Render(rightText)
	gap := width - lipglossWidth(left) - lipglossWidth(right)
	return fitStyledLine(left+surface(zeroTheme.ink).Render(strings.Repeat(" ", maxInt(1, gap)))+right, width)
}

func (wizard *providerWizardState) filteredModels() []providerWizardModel {
	if wizard == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(wizard.modelSearch))
	if query == "" {
		return append([]providerWizardModel{}, wizard.models...)
	}
	models := make([]providerWizardModel, 0, len(wizard.models))
	for _, model := range wizard.models {
		if model.matches(query) {
			models = append(models, model)
		}
	}
	return models
}

func (model providerWizardModel) displayLabel() string {
	description := strings.TrimSpace(model.Description)
	if description != "" && !providerWizardGenericModelDescription(description) {
		return description
	}
	return model.ID
}

func (model providerWizardModel) secondaryText() string {
	if model.displayLabel() != model.ID {
		return model.ID
	}
	return model.Description
}

func (model providerWizardModel) matches(query string) bool {
	if query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{model.ID, model.Description, model.Meta}, " "))
	return strings.Contains(haystack, query)
}

func providerWizardGenericModelDescription(description string) bool {
	switch strings.ToLower(strings.TrimSpace(description)) {
	case "", "catalog default", "catalog model", "custom endpoint model", "live model":
		return true
	default:
		return strings.HasSuffix(strings.ToLower(strings.TrimSpace(description)), " model")
	}
}

func (wizard *providerWizardState) renderDoneStep(width int) []string {
	provider := wizard.currentProvider()
	model := wizard.currentModel()
	checkCommand := "zero providers check " + provider.ID + " --connectivity"
	return []string{
		zeroTheme.accent.Render("Ready to connect"),
		zeroTheme.ink.Render("provider: " + provider.Name),
		zeroTheme.ink.Render("model: " + model.ID),
		zeroTheme.ink.Render("credential: " + providerWizardCredentialLabel(provider, wizard.apiKey)),
		zeroTheme.faint.Render("Press Enter to save this provider and use it now."),
		zeroTheme.faint.Render("Verify later with:"),
		zeroTheme.onPanel2(zeroTheme.ink).Render(checkCommand),
	}
}

func providerWizardCredentialLabel(provider providercatalog.Descriptor, apiKey string) string {
	if strings.TrimSpace(apiKey) != "" {
		return "pasted key"
	}
	if env := firstProviderDisplayValue(provider.AuthEnvVars...); provider.RequiresAuth && env != "" {
		return env + " env var"
	}
	return "not required"
}

func maskedProviderWizardKey(value string) string {
	count := len([]rune(value))
	if count == 0 {
		return ""
	}
	if count > 24 {
		count = 24
	}
	return strings.Repeat("*", count)
}

func providerWizardProfile(provider providercatalog.Descriptor, model string, apiKey string) config.ProviderProfile {
	profile := config.ProviderProfile{
		Name:         provider.ID,
		ProviderKind: providerWizardProviderKind(provider),
		CatalogID:    provider.ID,
		BaseURL:      provider.DefaultBaseURL,
		APIFormat:    providerWizardAPIFormat(provider),
		Model:        firstProviderDisplayValue(model, provider.DefaultModel),
	}
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		profile.APIKey = apiKey
	} else if env := firstProviderDisplayValue(provider.AuthEnvVars...); provider.RequiresAuth && env != "" {
		profile.APIKeyEnv = env
	}
	return profile
}

func providerWizardProviderKind(provider providercatalog.Descriptor) config.ProviderKind {
	switch provider.Transport {
	case providercatalog.TransportOpenAI:
		return config.ProviderKindOpenAI
	case providercatalog.TransportAnthropic:
		return config.ProviderKindAnthropic
	case providercatalog.TransportAnthropicCompatible:
		return config.ProviderKindAnthropicCompat
	case providercatalog.TransportGoogle:
		return config.ProviderKindGoogle
	case providercatalog.TransportOpenAICompatible:
		return config.ProviderKindOpenAICompatible
	default:
		return config.ProviderKind(strings.ToLower(string(provider.Transport)))
	}
}

func providerWizardAPIFormat(provider providercatalog.Descriptor) string {
	if provider.Transport == providercatalog.TransportOpenAI || provider.Transport == providercatalog.TransportOpenAICompatible {
		return string(providercatalog.APIFormatOpenAIChatCompletions)
	}
	if len(provider.SupportedAPIFormats) == 0 {
		return ""
	}
	return string(provider.SupportedAPIFormats[0])
}

func providerWizardAddCommand(provider providercatalog.Descriptor, model string) string {
	parts := []string{"zero", "providers", "add", provider.ID}
	if env := firstProviderDisplayValue(provider.AuthEnvVars...); provider.RequiresAuth && env != "" {
		parts = append(parts, "--api-key-env", env)
	}
	if model = strings.TrimSpace(model); model != "" && model != provider.DefaultModel {
		parts = append(parts, "--model", model)
	}
	parts = append(parts, "--set-active")
	return strings.Join(parts, " ")
}

func lipglossWidth(value string) int {
	return lipgloss.Width(value)
}
