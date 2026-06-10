package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type setupStage int

const (
	setupStageWelcome setupStage = iota
	setupStageProvider
	setupStageCredentials
	setupStageSafety
	setupStageReady
)

const setupStageCount = int(setupStageReady) + 1

type setupState struct {
	visible    bool
	required   bool
	configPath string
	providers  []SetupProviderOption
	selected   int
	stage      setupStage
	err        string
	apiKey     textinput.Model
}

func newSetupState(options SetupOptions) setupState {
	providers := append([]SetupProviderOption{}, options.Providers...)
	if len(providers) == 0 {
		providers = []SetupProviderOption{{
			ID:           "openai",
			Name:         "OpenAI",
			DefaultModel: "gpt-4.1",
			EnvVar:       "OPENAI_API_KEY",
			RequiresAuth: true,
		}}
	}
	apiKey := textinput.New()
	apiKey.Prompt = ""
	apiKey.PromptStyle = zeroTheme.faint
	apiKey.TextStyle = zeroTheme.ink
	apiKey.PlaceholderStyle = zeroTheme.faint
	apiKey.Placeholder = "paste key or leave blank"
	apiKey.EchoMode = textinput.EchoPassword
	apiKey.EchoCharacter = '*'
	apiKey.Focus()
	return setupState{
		visible:    options.Visible,
		required:   options.Required,
		configPath: strings.TrimSpace(options.ConfigPath),
		providers:  providers,
		apiKey:     apiKey,
	}
}

func (m model) handleSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.setupCredentialInputActive() {
		return m.handleSetupCredentialKey(msg)
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		if m.setup.stage > setupStageWelcome {
			m.setup.stage--
			m.setup.err = ""
			return m, nil
		}
		if m.setup.required {
			return m, tea.Quit
		}
		return m.exitSetupToChat()
	case tea.KeyLeft:
		if m.setup.stage > setupStageWelcome {
			m.setup.stage--
			m.setup.err = ""
		}
		return m, nil
	case tea.KeyEnter:
		if m.setup.stage == setupStageProvider || m.setup.stage == setupStageReady {
			return m.advanceSetup()
		}
		return m, nil
	case tea.KeySpace:
		if m.setup.stage < setupStageReady && m.setup.stage != setupStageProvider {
			return m.advanceSetup()
		}
		return m, nil
	case tea.KeyUp:
		if m.setup.stage == setupStageProvider {
			m.moveSetupProvider(-1)
		}
		return m, nil
	case tea.KeyDown:
		if m.setup.stage == setupStageProvider {
			m.moveSetupProvider(1)
		}
		return m, nil
	}

	switch msg.String() {
	case " ":
		if m.setup.stage < setupStageReady && m.setup.stage != setupStageProvider {
			return m.advanceSetup()
		}
	case "q":
		return m, tea.Quit
	case "k":
		if m.setup.stage == setupStageProvider {
			m.moveSetupProvider(-1)
		}
	case "j":
		if m.setup.stage == setupStageProvider {
			m.moveSetupProvider(1)
		}
	}
	return m, nil
}

func (m model) advanceSetup() (tea.Model, tea.Cmd) {
	if m.setup.stage < setupStageReady {
		if m.setup.stage == setupStageProvider {
			m.setup.apiKey.SetValue("")
		}
		m.setup.stage++
		m.setup.err = ""
		return m, nil
	}
	return m.completeSetup()
}

func (m *model) moveSetupProvider(delta int) {
	if len(m.setup.providers) == 0 {
		return
	}
	m.setup.selected = ((m.setup.selected+delta)%len(m.setup.providers) + len(m.setup.providers)) % len(m.setup.providers)
	m.setup.apiKey.SetValue("")
}

func (m model) completeSetup() (tea.Model, tea.Cmd) {
	option := m.setupProvider()
	if option.ID == "" {
		m.setup.err = "No provider option is available."
		return m, nil
	}
	if m.setupSave == nil {
		return m.exitSetupToChat()
	}

	result, err := m.setupSave(SetupSelection{
		CatalogID: option.ID,
		Model:     option.DefaultModel,
		APIKey:    m.setupCredentialAPIKey(option),
	})
	if err != nil {
		m.setup.err = err.Error()
		return m, nil
	}

	if result.ConfigPath != "" {
		m.setup.configPath = result.ConfigPath
	}
	if result.Provider.Name != "" {
		m.providerProfile = result.Provider
		m.providerName = result.Provider.Name
		m.modelName = result.Provider.Model
		if m.newProvider != nil {
			if provider, providerErr := m.newProvider(result.Provider); providerErr == nil {
				m.provider = provider
			}
		}
	}

	return m.exitSetupToChat()
}

func (m model) exitSetupToChat() (tea.Model, tea.Cmd) {
	m.setup.visible = false
	m.headerPrinted = false
	m.flushQueue = nil
	m.printInFlight = false
	return m, nil
}

func (m model) setupCredentialInputActive() bool {
	return m.setup.stage == setupStageCredentials && setupProviderAcceptsAPIKey(m.setupProvider())
}

func (m model) handleSetupCredentialKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc, tea.KeyLeft:
		m.setup.stage--
		m.setup.err = ""
		return m, nil
	case tea.KeyEnter:
		return m.advanceSetup()
	case tea.KeyUp, tea.KeyDown:
		return m, nil
	}
	var cmd tea.Cmd
	m.setup.apiKey, cmd = m.setup.apiKey.Update(msg)
	m.setup.err = ""
	return m, cmd
}

func setupProviderAcceptsAPIKey(option SetupProviderOption) bool {
	return option.RequiresAuth && !option.Local
}

func (m model) setupCredentialAPIKey(option SetupProviderOption) string {
	if !setupProviderAcceptsAPIKey(option) {
		return ""
	}
	return strings.TrimSpace(m.setup.apiKey.Value())
}

func (m model) setupProvider() SetupProviderOption {
	if len(m.setup.providers) == 0 {
		return SetupProviderOption{}
	}
	index := clamp(m.setup.selected, 0, len(m.setup.providers)-1)
	return m.setup.providers[index]
}

func (m model) setupView(width int) string {
	if width <= 0 {
		width = defaultStartupWidth
	}
	height := normalizedStartupHeight(m.height)
	content := m.setupStageLines(width, height)
	if m.setup.err != "" {
		content = append(content, "", zeroTheme.red.Render("error: "+m.setup.err))
	}
	progress := setupProgressText(m.setup.stage)
	footer := m.setupFooter()

	topGap := maxInt(0, (height-len(content)-3)/2)
	bottomGap := maxInt(0, height-topGap-len(content)-2)
	lines := make([]string, 0, height)
	for i := 0; i < topGap; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, centerSetupLines(content, width)...)
	for i := 0; i < bottomGap; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, centerLine(fitStyledLine(progress, width), width))
	lines = append(lines, centerLine(fitStyledLine(footer, width), width))
	return strings.Join(lines, "\n")
}

func (m model) setupStageLines(width int, height int) []string {
	switch m.setup.stage {
	case setupStageProvider:
		return m.setupProviderLines(width, height)
	case setupStageCredentials:
		return m.setupCredentialLines(width)
	case setupStageSafety:
		return []string{
			zeroTheme.ink.Bold(true).Render("Safety"),
			"",
			"Zero asks before running shell commands or changing files.",
			"Unsafe mode stays off unless you explicitly enable it.",
			"",
			zeroTheme.faint.Render("Default: ask before risky work."),
		}
	case setupStageReady:
		option := m.setupProvider()
		return []string{
			zeroTheme.ink.Bold(true).Render("Ready"),
			"",
			"Zero will save this setup and open chat.",
			"provider: " + displayValue(option.Name, option.ID),
			"model: " + displayValue(option.DefaultModel, "default"),
			"credentials: " + m.setupCredentialSummary(option),
			"config: " + displayValue(m.setup.configPath, "user config"),
			"",
			zeroTheme.faint.Render("Later, use /provider, /doctor, or /help anytime."),
		}
	default:
		return []string{
			zeroTheme.accent.Render("Welcome to Zero"),
			"",
			zeroTheme.ink.Render("A terminal agent for changing real code."),
			zeroTheme.faint.Render("Plan changes, edit with approval, run checks, and resume sessions."),
		}
	}
}

func (m model) setupProviderLines(width int, height int) []string {
	option := m.setupProvider()
	rowWidth := setupProviderBlockWidth(width, m.setup.providers)
	maxVisible := setupProviderMaxVisible(height, len(m.setup.providers))
	start := selectableListStart(len(m.setup.providers), maxVisible, m.setup.selected)
	visibleProviders := m.setup.providers[start : start+maxVisible]
	lines := []string{
		padSetupLine("  "+zeroTheme.ink.Bold(true).Render("Choose a provider"), rowWidth),
		blankSetupBlockLine(rowWidth),
	}
	for index, option := range visibleProviders {
		absoluteIndex := start + index
		marker := "  "
		style := zeroTheme.ink
		if absoluteIndex == m.setup.selected {
			marker = "❯ "
			style = zeroTheme.accent.Bold(true)
		}
		line := marker + style.Render(displayValue(option.Name, option.ID))
		lines = append(lines, padSetupLine(line, rowWidth))
	}
	lines = append(lines,
		blankSetupBlockLine(rowWidth),
		padSetupLine("  "+zeroTheme.faint.Render(setupProviderDetail(option)), rowWidth),
	)
	return lines
}

func setupProviderMaxVisible(height int, total int) int {
	if total <= 0 {
		return 0
	}
	maxVisible := height - 10
	if maxVisible < 6 {
		maxVisible = 6
	}
	if maxVisible > total {
		return total
	}
	return maxVisible
}

func setupProviderBlockWidth(terminalWidth int, providers []SetupProviderOption) int {
	available := maxInt(24, minInt(terminalWidth-8, 44))
	target := maxInt(lipgloss.Width("  step 2/5"), lipgloss.Width("  Choose a provider"))
	for _, provider := range providers {
		target = maxInt(target, 2+lipgloss.Width(displayValue(provider.Name, provider.ID)))
		target = maxInt(target, lipgloss.Width("  "+setupProviderDetail(provider)))
	}
	target = maxInt(target, 32)
	return minInt(target, available)
}

func blankSetupBlockLine(width int) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat(" ", width)
}

func setupProviderDetail(option SetupProviderOption) string {
	if option.Local {
		return "Local provider. Default model: " + displayValue(option.DefaultModel, "local-model")
	}
	return "Default model: " + displayValue(option.DefaultModel, "provider default")
}

func (m model) setupCredentialLines(width int) []string {
	option := m.setupProvider()
	lines := []string{
		zeroTheme.ink.Bold(true).Render("Credentials"),
		"",
	}
	if option.Local || !option.RequiresAuth {
		lines = append(lines,
			displayValue(option.Name, option.ID)+" does not need an API key.",
			"Start the local server before sending a prompt.",
		)
		return lines
	}
	envVar := displayValue(option.EnvVar, "the provider API key env var")
	lines = append(lines,
		"Paste your "+displayValue(option.Name, option.ID)+" API key",
		"or leave blank to use "+envVar+" from your shell.",
		"",
		m.setupAPIKeyInputLine(width),
		"",
		zeroTheme.faint.Render("Saved keys stay in your user config."),
		zeroTheme.faint.Render("Blank uses "+envVar+" from your shell."),
	)
	return lines
}

func (m model) setupAPIKeyInputLine(width int) string {
	input := m.setup.apiKey
	if strings.TrimSpace(input.Value()) == "" {
		return input.PlaceholderStyle.Render(input.Placeholder)
	}
	contentWidth := lipgloss.Width(input.Value())
	if contentWidth == 0 {
		contentWidth = lipgloss.Width(input.Placeholder)
	}
	input.Width = minInt(maxInt(contentWidth, 1), maxInt(1, width-lipgloss.Width(input.Prompt)))
	return input.View()
}

func (m model) setupCredentialSummary(option SetupProviderOption) string {
	if !setupProviderAcceptsAPIKey(option) {
		return "not required"
	}
	if m.setupCredentialAPIKey(option) != "" {
		return "saved API key"
	}
	return "env var " + displayValue(option.EnvVar, "provider API key")
}

func (m model) setupFooter() string {
	switch m.setup.stage {
	case setupStageReady:
		return zeroTheme.accent.Render("Enter") + zeroTheme.faint.Render(" to save and start chat")
	case setupStageCredentials:
		if m.setupCredentialInputActive() {
			return zeroTheme.faint.Render("paste key optional   ") + zeroTheme.accent.Render("Enter") + zeroTheme.faint.Render(" continue   left back")
		}
		return zeroTheme.accent.Render("Space") + zeroTheme.faint.Render(" to continue")
	case setupStageProvider:
		return zeroTheme.faint.Render("↑/↓ choose   ") + zeroTheme.accent.Render("Enter") + zeroTheme.faint.Render(" continue   q quit")
	case setupStageWelcome:
		return zeroTheme.accent.Render("Space") + zeroTheme.faint.Render(" to set up Zero")
	default:
		return zeroTheme.accent.Render("Space") + zeroTheme.faint.Render(" to continue")
	}
}

func centerSetupLines(lines []string, width int) []string {
	fitted := make([]string, 0, len(lines))
	for _, line := range lines {
		fitted = append(fitted, centerLine(fitStyledLine(line, width), width))
	}
	return fitted
}

func setupProgressText(stage setupStage) string {
	return zeroTheme.faint.Render(fmt.Sprintf("%d/%d", int(stage)+1, setupStageCount))
}

func padSetupLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	if pad := width - lipgloss.Width(line); pad > 0 {
		return line + strings.Repeat(" ", pad)
	}
	return fitStyledLine(line, width)
}
