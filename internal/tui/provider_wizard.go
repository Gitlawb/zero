package tui

import (
	"net"
	"net/url"
	"os"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/redaction"
)

const maxProviderWizardProvidersVisible = 8
const maxProviderWizardModelsVisible = 10
const providerWizardMinWidth = 48
const providerWizardProviderWidth = 64
const providerWizardMediumWidth = 86
const providerWizardModelWidth = 92

type providerWizardStep int

const (
	providerWizardStepProvider providerWizardStep = iota
	providerWizardStepEndpoint
	providerWizardStepName
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
	baseURL          string
	profileName      string
	apiKey           string
	err              string
	modelSource      string
	modelLoading     bool
	modelLoadError   string
	discoveryToken   int
}

func (m model) newProviderWizard() *providerWizardState {
	providers := providerWizardProviders()
	wizard := &providerWizardState{
		step:             providerWizardStepProvider,
		providers:        providers,
		selectedProvider: 0,
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
	if providerWizardUsesTypedModel(wizard.currentProvider()) {
		modelID := strings.TrimSpace(wizard.modelSearch)
		if modelID == "" {
			return providerWizardModel{Description: "model name required"}
		}
		return providerWizardModel{ID: modelID, Description: "custom model"}
	}
	wizard.refreshModels()
	models := wizard.filteredModels()
	if len(models) == 0 {
		return providerWizardModel{Description: "no matching models"}
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
		wizard.baseURL = ""
		wizard.profileName = ""
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
		if providerWizardNeedsEndpoint(wizard.currentProvider()) {
			wizard.step = providerWizardStepEndpoint
		} else if providerWizardNeedsCredential(wizard.currentProvider()) {
			wizard.step = providerWizardStepCredential
		} else {
			wizard.step = providerWizardStepModel
		}
	case providerWizardStepEndpoint:
		wizard.err = ""
		if err := providerWizardEndpointError(wizard.baseURL); err != "" {
			wizard.err = err
			return
		}
		wizard.step = providerWizardStepName
	case providerWizardStepName:
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
		if providerWizardUsesTypedModel(wizard.currentProvider()) {
			if strings.TrimSpace(wizard.modelSearch) == "" {
				wizard.err = "enter a model name before continuing"
				return
			}
			wizard.step = providerWizardStepDone
			return
		}
		if wizard.modelLoading {
			wizard.err = "Models are still loading."
			return
		}
		wizard.refreshModels()
		if len(wizard.filteredModels()) == 0 {
			wizard.err = "choose a matching model before continuing"
			return
		}
		wizard.step = providerWizardStepDone
	case providerWizardStepDone:
		wizard.step = providerWizardStepProvider
	}
}

func (wizard *providerWizardState) retreat() {
	if wizard == nil {
		return
	}
	wizard.err = ""
	switch wizard.step {
	case providerWizardStepEndpoint:
		wizard.step = providerWizardStepProvider
	case providerWizardStepName:
		wizard.step = providerWizardStepEndpoint
	case providerWizardStepCredential:
		if providerWizardNeedsProfileName(wizard.currentProvider()) {
			wizard.step = providerWizardStepName
		} else if providerWizardNeedsEndpoint(wizard.currentProvider()) {
			wizard.step = providerWizardStepEndpoint
		} else {
			wizard.step = providerWizardStepProvider
		}
	case providerWizardStepModel:
		if providerWizardNeedsCredential(wizard.currentProvider()) {
			wizard.step = providerWizardStepCredential
		} else if providerWizardNeedsEndpoint(wizard.currentProvider()) {
			wizard.step = providerWizardStepEndpoint
		} else {
			wizard.step = providerWizardStepProvider
		}
	case providerWizardStepDone:
		wizard.step = providerWizardStepModel
	}
}

func (wizard *providerWizardState) refreshModels() {
	if wizard == nil {
		return
	}
	provider := wizard.currentProvider()
	if providerWizardUsesTypedModel(provider) {
		return
	}
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

func providerWizardNeedsEndpoint(provider providercatalog.Descriptor) bool {
	switch provider.ID {
	case "custom-openai-compatible", "custom-anthropic-compatible":
		return true
	default:
		return false
	}
}

func providerWizardUsesTypedModel(provider providercatalog.Descriptor) bool {
	return providerWizardNeedsEndpoint(provider)
}

func providerWizardNeedsProfileName(provider providercatalog.Descriptor) bool {
	return providerWizardNeedsEndpoint(provider)
}

func (m model) handleProviderWizardKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.providerWizard == nil {
		return m, nil
	}
	if m.providerWizard.step == providerWizardStepEndpoint {
		switch msg.Type {
		case tea.KeyRunes:
			m.providerWizard.appendBaseURL(msg.Runes)
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			m.providerWizard.deleteBaseURLRune()
			return m, nil
		case tea.KeyCtrlU:
			m.providerWizard.baseURL = ""
			m.providerWizard.err = ""
			return m, nil
		case tea.KeyLeft:
			m.providerWizard.retreat()
			return m, nil
		case tea.KeyRight:
			if m.providerWizard.canAdvanceWithRight() {
				return m.advanceProviderWizard()
			}
			return m, nil
		case tea.KeyEnter:
			return m.advanceProviderWizard()
		}
	}
	if m.providerWizard.step == providerWizardStepName {
		switch msg.Type {
		case tea.KeyRunes:
			m.providerWizard.appendProfileName(msg.Runes)
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			m.providerWizard.deleteProfileNameRune()
			return m, nil
		case tea.KeyCtrlU:
			m.providerWizard.profileName = ""
			m.providerWizard.err = ""
			return m, nil
		case tea.KeyLeft:
			m.providerWizard.retreat()
			return m, nil
		case tea.KeyRight, tea.KeyEnter:
			return m.advanceProviderWizard()
		}
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
		case tea.KeyLeft:
			m.providerWizard.retreat()
			return m, nil
		case tea.KeyRight:
			if m.providerWizard.canAdvanceWithRight() {
				return m.advanceProviderWizard()
			}
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
	case tea.KeyLeft:
		m.providerWizard.retreat()
	case tea.KeyRight:
		if m.providerWizard.canAdvanceWithRight() {
			return m.advanceProviderWizard()
		}
	case tea.KeyEnter:
		if m.providerWizard.step == providerWizardStepDone {
			return m.applyProviderWizard()
		}
		return m.advanceProviderWizard()
	}
	return m, nil
}

func (wizard *providerWizardState) canAdvanceWithRight() bool {
	if wizard == nil {
		return false
	}
	switch wizard.step {
	case providerWizardStepProvider:
		return strings.TrimSpace(wizard.currentProvider().ID) != ""
	case providerWizardStepEndpoint:
		return providerWizardEndpointError(wizard.baseURL) == ""
	case providerWizardStepName:
		return true
	case providerWizardStepCredential:
		return wizard.credentialReadyForRight()
	case providerWizardStepModel:
		if providerWizardUsesTypedModel(wizard.currentProvider()) {
			return strings.TrimSpace(wizard.modelSearch) != ""
		}
		if wizard.modelLoading {
			return false
		}
		wizard.refreshModels()
		return len(wizard.filteredModels()) > 0
	default:
		return false
	}
}

func (wizard *providerWizardState) credentialReadyForRight() bool {
	if strings.TrimSpace(wizard.apiKey) != "" {
		return true
	}
	provider := wizard.currentProvider()
	if !providerWizardNeedsCredential(provider) {
		return true
	}
	for _, env := range provider.AuthEnvVars {
		if strings.TrimSpace(os.Getenv(strings.TrimSpace(env))) != "" {
			return true
		}
	}
	return false
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

func (wizard *providerWizardState) appendBaseURL(runes []rune) {
	for _, r := range runes {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			continue
		}
		wizard.baseURL += string(r)
	}
	wizard.err = ""
}

func (wizard *providerWizardState) deleteBaseURLRune() {
	if wizard.baseURL == "" {
		return
	}
	runes := []rune(wizard.baseURL)
	wizard.baseURL = string(runes[:len(runes)-1])
	wizard.err = ""
}

func (wizard *providerWizardState) appendProfileName(runes []rune) {
	for _, r := range runes {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			continue
		}
		wizard.profileName += string(r)
	}
	wizard.err = ""
}

func (wizard *providerWizardState) deleteProfileNameRune() {
	if wizard.profileName == "" {
		return
	}
	runes := []rune(wizard.profileName)
	wizard.profileName = string(runes[:len(runes)-1])
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
	profile := providerWizardProfile(provider, modelChoice.ID, wizard.apiKey, wizard.baseURL, wizard.profileName)
	runtimeProfile := providerWizardRuntimeProfile(profile)
	if m.newProvider != nil {
		nextProvider, err := m.newProvider(runtimeProfile)
		if err != nil {
			wizard.err = redaction.RedactString(err.Error(), redaction.Options{ExtraSecretValues: []string{profile.APIKey, runtimeProfile.APIKey}})
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

func providerWizardRuntimeProfile(profile config.ProviderProfile) config.ProviderProfile {
	runtimeProfile := profile
	if strings.TrimSpace(runtimeProfile.APIKey) == "" && strings.TrimSpace(runtimeProfile.APIKeyEnv) != "" {
		runtimeProfile.APIKey = strings.TrimSpace(os.Getenv(runtimeProfile.APIKeyEnv))
	}
	return runtimeProfile
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
	overlayWidth := providerWizardOverlayWidth(width, wizard.step)
	innerWidth := maxInt(20, overlayWidth-4)

	lines := []string{
		zeroTheme.faint.Render(providerWizardStepLine(wizard)),
		zeroTheme.line.Render(strings.Repeat("─", innerWidth)),
	}
	if wizard.err != "" {
		lines = append(lines, zeroTheme.red.Render("error: "+wizard.err), "")
	}
	switch wizard.step {
	case providerWizardStepProvider:
		lines = append(lines, wizard.renderProviderStep(innerWidth)...)
	case providerWizardStepEndpoint:
		lines = append(lines, wizard.renderEndpointStep(innerWidth)...)
	case providerWizardStepName:
		lines = append(lines, wizard.renderNameStep(innerWidth)...)
	case providerWizardStepCredential:
		lines = append(lines, wizard.renderCredentialStep(innerWidth)...)
	case providerWizardStepModel:
		lines = append(lines, wizard.renderModelStep(innerWidth)...)
	case providerWizardStepDone:
		lines = append(lines, wizard.renderDoneStep(innerWidth)...)
	}
	lines = append(lines,
		zeroTheme.line.Render(strings.Repeat("─", innerWidth)),
		zeroTheme.faint.Render(wizard.footer()),
	)

	block := styledBlockFillTitle(overlayWidth, "Provider setup", lines, zeroTheme.lineStrong, lipgloss.NewStyle())
	if width > overlayWidth {
		return indentBlock(block, (width-overlayWidth)/2)
	}
	return block
}

func (wizard *providerWizardState) footer() string {
	canRight := wizard.canAdvanceWithRight()
	switch wizard.step {
	case providerWizardStepProvider:
		if canRight {
			return "↑/↓ move   Enter/→ continue   Esc close"
		}
		return "↑/↓ move   Enter continue   Esc close"
	case providerWizardStepEndpoint:
		if canRight {
			return "Enter/→ continue   ← back   Esc close"
		}
		return "Enter continue   ← back   Esc close"
	case providerWizardStepName:
		return "Enter/→ continue   ← back   Esc close"
	case providerWizardStepModel:
		if canRight {
			return "↑/↓ move   Enter/→ continue   ← back   Esc close"
		}
		return "↑/↓ move   Enter continue   ← back   Esc close"
	case providerWizardStepDone:
		return "Enter save   ← back   Esc close"
	default:
		if canRight {
			return "Enter/→ continue   ← back   Esc close"
		}
		return "Enter continue   ← back   Esc close"
	}
}

func providerWizardOverlayWidth(width int, step providerWizardStep) int {
	if width <= 0 {
		return providerWizardProviderWidth
	}
	target := providerWizardMediumWidth
	switch step {
	case providerWizardStepProvider:
		target = providerWizardProviderWidth
	case providerWizardStepModel:
		target = providerWizardModelWidth
	}
	target = minInt(target, width)
	if target < providerWizardMinWidth {
		return width
	}
	return target
}

func providerWizardStepLine(wizard *providerWizardState) string {
	if wizard == nil {
		return ""
	}
	step := wizard.step
	steps := []struct {
		step  providerWizardStep
		label string
	}{
		{providerWizardStepProvider, "1 provider"},
	}
	if providerWizardNeedsEndpoint(wizard.currentProvider()) {
		steps = append(steps,
			struct {
				step  providerWizardStep
				label string
			}{providerWizardStepEndpoint, "2 endpoint"},
			struct {
				step  providerWizardStep
				label string
			}{providerWizardStepName, "3 name"},
			struct {
				step  providerWizardStep
				label string
			}{providerWizardStepCredential, "4 key"},
			struct {
				step  providerWizardStep
				label string
			}{providerWizardStepModel, "5 model"},
			struct {
				step  providerWizardStep
				label string
			}{providerWizardStepDone, "6 ready"},
		)
	} else {
		steps = append(steps,
			struct {
				step  providerWizardStep
				label string
			}{providerWizardStepCredential, "2 key"},
			struct {
				step  providerWizardStep
				label string
			}{providerWizardStepModel, "3 model"},
			struct {
				step  providerWizardStep
				label string
			}{providerWizardStepDone, "4 ready"},
		)
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
	return lines
}

func (wizard *providerWizardState) renderSelectableProvider(width int, index int, provider providercatalog.Descriptor) string {
	selected := index == wizard.selectedProvider
	surface := transparentSurface
	marker := surface(zeroTheme.faintest).Render("  ")
	if selected {
		surface = zeroTheme.onSel
		marker = surface(zeroTheme.accent).Render("❯ ")
	}
	left := marker + surface(zeroTheme.ink).Render(provider.Name)
	return fitStyledLine(left, width)
}

func (wizard *providerWizardState) renderEndpointStep(width int) []string {
	provider := wizard.currentProvider()
	input := providerWizardInputLine("url > ", wizard.baseURL, providerWizardEndpointPlaceholder(provider), width)
	return []string{
		zeroTheme.accent.Render("Endpoint URL"),
		zeroTheme.ink.Render("Enter the API base URL for " + provider.Name + "."),
		zeroTheme.faint.Render(providerWizardEndpointHint(provider)),
		input,
	}
}

func providerWizardEndpointPlaceholder(provider providercatalog.Descriptor) string {
	if provider.Transport == providercatalog.TransportAnthropicCompatible {
		return "https://api.example.com/anthropic"
	}
	return "https://api.example.com/v1"
}

func providerWizardEndpointHint(provider providercatalog.Descriptor) string {
	if provider.Transport == providercatalog.TransportAnthropicCompatible {
		return "Use the base URL before /v1/messages."
	}
	return "Use the base URL before /chat/completions."
}

func (wizard *providerWizardState) renderNameStep(width int) []string {
	name := providerWizardDisplayName(wizard.currentProvider(), wizard.baseURL, wizard.profileName)
	return []string{
		zeroTheme.accent.Render("Provider name"),
		zeroTheme.ink.Render("Choose the short label shown in the status bar."),
		zeroTheme.faint.Render("Leave blank to use " + name + "."),
		providerWizardInputLine("name > ", strings.TrimSpace(wizard.profileName), name, width),
	}
}

func (wizard *providerWizardState) renderCredentialStep(width int) []string {
	provider := wizard.currentProvider()
	env := firstProviderDisplayValue(provider.AuthEnvVars...)
	value := zeroTheme.accent.Render("▌") + zeroTheme.faint.Render("paste key here")
	if wizard.apiKey != "" {
		value = zeroTheme.ink.Render(maskedProviderWizardKey(wizard.apiKey)) + zeroTheme.accent.Render("▌")
	}
	input := zeroTheme.userPrompt.Render("api key > ") + value
	return []string{
		zeroTheme.accent.Render("Paste API key"),
		zeroTheme.ink.Render(providerWizardCredentialInstruction(env)),
		input,
		zeroTheme.faint.Render("Pasted keys are hidden and saved in your user config."),
	}
}

func providerWizardCredentialInstruction(env string) string {
	if env = strings.TrimSpace(env); env != "" {
		return "Paste a key, or leave blank to use " + env + "."
	}
	return "Paste a key, or leave blank to use your shell env."
}

func (wizard *providerWizardState) renderModelStep(width int) []string {
	if providerWizardUsesTypedModel(wizard.currentProvider()) {
		return wizard.renderTypedModelStep(width)
	}
	if wizard.modelLoading {
		return wizard.renderModelLoadingStep(width)
	}
	lines := []string{zeroTheme.accent.Render("Choose a model")}
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
	if detail := providerWizardModelDetail(wizard.currentModel()); detail != "" {
		lines = append(lines, fitStyledLine(zeroTheme.faint.Render("  "+detail), width))
	}
	return lines
}

func (wizard *providerWizardState) renderModelLoadingStep(width int) []string {
	return []string{
		zeroTheme.accent.Render("Choose a model"),
		"",
		fitStyledLine(zeroTheme.faint.Render("Checking available models..."), width),
		fitStyledLine(zeroTheme.faint.Render("Built-in models will be used if discovery fails."), width),
	}
}

func (wizard *providerWizardState) renderModelSearch(width int) string {
	query := strings.TrimSpace(wizard.modelSearch)
	return providerWizardInputLine("search > ", query, "model name...", width)
}

func providerWizardInputLine(promptText string, value string, placeholder string, width int) string {
	prompt := zeroTheme.userPrompt.Render("search > ")
	cursor := zeroTheme.accent.Render("▌")
	if promptText != "" {
		prompt = zeroTheme.userPrompt.Render(promptText)
	}
	if value == "" {
		return fitStyledLine(prompt+cursor+zeroTheme.faint.Render(placeholder), width)
	}
	return fitStyledLine(prompt+zeroTheme.ink.Render(value)+cursor, width)
}

func (wizard *providerWizardState) renderTypedModelStep(width int) []string {
	provider := wizard.currentProvider()
	return []string{
		zeroTheme.accent.Render("Model name"),
		zeroTheme.ink.Render("Enter the model ID this endpoint expects."),
		zeroTheme.faint.Render("Examples: gpt-4.1, claude-sonnet-4-5, llama-3.3-70b"),
		providerWizardInputLine("model > ", strings.TrimSpace(wizard.modelSearch), provider.DefaultModel, width),
	}
}

func (wizard *providerWizardState) modelStatusText() string {
	if wizard.modelLoadError != "" {
		return "Using built-in model list"
	}
	return ""
}

func (wizard *providerWizardState) renderSelectableModel(width int, index int, model providerWizardModel) string {
	selected := index == wizard.selectedModel
	surface := transparentSurface
	marker := surface(zeroTheme.faintest).Render("  ")
	if selected {
		surface = zeroTheme.onSel
		marker = surface(zeroTheme.accent).Render("❯ ")
	}
	left := marker + surface(zeroTheme.ink).Render(model.displayLabel())
	return fitStyledLine(left, width)
}

func providerWizardModelDetail(model providerWizardModel) string {
	parts := []string{}
	if secondary := strings.TrimSpace(model.secondaryText()); secondary != "" && !providerWizardGenericModelDescription(secondary) {
		parts = append(parts, secondary)
	}
	if meta := strings.TrimSpace(model.Meta); meta != "" {
		parts = append(parts, meta)
	}
	return strings.Join(parts, " · ")
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
	lines := []string{
		zeroTheme.accent.Render("Ready to connect"),
		"",
		zeroTheme.ink.Render("Provider    " + provider.Name),
	}
	if providerWizardNeedsEndpoint(provider) {
		lines = append(lines, zeroTheme.ink.Render("Endpoint    "+strings.TrimSpace(wizard.baseURL)))
	}
	lines = append(lines,
		zeroTheme.ink.Render("Name        "+providerWizardDisplayName(provider, wizard.baseURL, wizard.profileName)),
		zeroTheme.ink.Render("Model       "+model.ID),
		zeroTheme.ink.Render("Credential  "+providerWizardCredentialLabel(provider, wizard.apiKey)),
		"",
		zeroTheme.faint.Render("Press Enter to save and start using this provider."),
	)
	return lines
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

func providerWizardProfile(provider providercatalog.Descriptor, model string, apiKey string, baseURL string, profileName string) config.ProviderProfile {
	profile := config.ProviderProfile{
		Name:         providerWizardDisplayName(provider, baseURL, profileName),
		ProviderKind: providerWizardProviderKind(provider),
		CatalogID:    provider.ID,
		BaseURL:      firstProviderDisplayValue(strings.TrimSpace(baseURL), provider.DefaultBaseURL),
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

func providerWizardEndpointError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "enter an endpoint URL before continuing"
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "enter a valid endpoint URL including https://"
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "endpoint URL must start with http:// or https://"
	}
	if parsed.Scheme == "http" && !providerWizardIsLoopbackHost(parsed.Hostname()) {
		return "endpoint URL must use https:// unless it is local loopback"
	}
	return ""
}

func providerWizardIsLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func providerWizardDisplayName(provider providercatalog.Descriptor, baseURL string, profileName string) string {
	if name := strings.TrimSpace(profileName); name != "" {
		return name
	}
	if providerWizardNeedsProfileName(provider) {
		return providerWizardNameFromBaseURL(baseURL)
	}
	return provider.ID
}

func providerWizardNameFromBaseURL(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return "custom"
	}
	host := strings.ToLower(parsed.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		return providerWizardSanitizedName("ip-" + ip.String())
	}
	host = strings.TrimPrefix(host, "api.")
	host = strings.TrimPrefix(host, "gateway.")
	parts := strings.Split(host, ".")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	if host != "" {
		return strings.ReplaceAll(host, ":", "-")
	}
	return "custom"
}

func providerWizardSanitizedName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
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
