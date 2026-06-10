package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/config"
)

func TestSetupTakeoverRendersAndCompletes(t *testing.T) {
	var saved SetupSelection
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible:    true,
			Required:   true,
			ConfigPath: "/tmp/zero/config.json",
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
				{ID: "ollama", Name: "Ollama Local", DefaultModel: "llama3.1", Local: true},
			},
			Save: func(selection SetupSelection) (SetupResult, error) {
				saved = selection
				return SetupResult{
					ConfigPath: "/tmp/zero/config.json",
					Provider: config.ProviderProfile{
						Name:      selection.CatalogID,
						CatalogID: selection.CatalogID,
						Model:     selection.Model,
					},
				}, nil
			},
		},
	})
	m.width = 100
	m.height = 30

	if view := m.View(); !strings.Contains(view, "Welcome to Zero") || !strings.Contains(view, "Space to set up Zero") || !strings.Contains(view, "terminal agent for changing real code") {
		t.Fatalf("setup welcome view missing expected text:\n%s", view)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if cmd != nil {
		t.Fatal("setup navigation should not launch a command")
	}
	m = updated.(model)
	if m.setup.stage != setupStageProvider {
		t.Fatalf("stage = %v, want provider", m.setup.stage)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if got := m.setupProvider().ID; got != "ollama" {
		t.Fatalf("selected provider = %q, want ollama", got)
	}

	for m.setup.stage != setupStageReady {
		m = pressSetupContinue(m)
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("setup completion should stay in the fullscreen chat surface")
	}

	if m.setup.visible {
		t.Fatal("setup should hide after save")
	}
	if saved.CatalogID != "ollama" || saved.Model != "llama3.1" {
		t.Fatalf("saved selection = %#v, want ollama llama3.1", saved)
	}
	if m.providerName != "ollama" || m.modelName != "llama3.1" {
		t.Fatalf("provider state = %q/%q, want ollama/llama3.1", m.providerName, m.modelName)
	}
	if !m.transcriptEmpty() {
		t.Fatalf("setup completion should open the normal empty chat surface, transcript: %#v", m.transcript)
	}
}

func TestSetupCompletionResetsChatSurfaceInsideAltScreen(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
			},
			Save: func(selection SetupSelection) (SetupResult, error) {
				return SetupResult{
					ConfigPath: "/tmp/zero/config.json",
					Provider: config.ProviderProfile{
						Name:      selection.CatalogID,
						CatalogID: selection.CatalogID,
						Model:     selection.Model,
					},
				}, nil
			},
		},
	})
	m.width = 100
	m.height = 30
	m.setup.stage = setupStageReady
	m.headerPrinted = true
	m.flushQueue = []string{"stale setup title"}
	m.printInFlight = true

	updated, cmd := m.completeSetup()
	m = updated.(model)
	if cmd != nil {
		t.Fatal("setup completion should not exit the alt-screen")
	}
	if m.setup.visible {
		t.Fatal("setup should be hidden")
	}
	if m.headerPrinted {
		t.Fatal("chat header should be reset so the normal surface can render it")
	}
	if len(m.flushQueue) != 0 {
		t.Fatalf("stale setup flush queue should be cleared, got %#v", m.flushQueue)
	}
	if m.printInFlight {
		t.Fatal("stale setup print state should be cleared")
	}
	if !m.transcriptEmpty() {
		t.Fatalf("setup completion should keep the chat empty state, transcript: %#v", m.transcript)
	}
}

func TestSetupTakeoverBlocksPromptSubmission(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1"},
			},
		},
	})
	m.input.SetValue("run tests")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("setup enter should not launch an agent run")
	}
	if m.pending {
		t.Fatal("setup enter should not start a prompt")
	}
	if m.setup.stage != setupStageWelcome {
		t.Fatalf("stage = %v, want welcome because Enter is not advertised here", m.setup.stage)
	}
}

func TestSetupRightArrowDoesNotAdvance(t *testing.T) {
	saveCalls := 0
	for _, stage := range []setupStage{
		setupStageWelcome,
		setupStageProvider,
		setupStageCredentials,
		setupStageReady,
	} {
		m := newModel(context.Background(), Options{
			Setup: SetupOptions{
				Visible: true,
				Providers: []SetupProviderOption{
					{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
				},
				Save: func(selection SetupSelection) (SetupResult, error) {
					saveCalls++
					return SetupResult{}, nil
				},
			},
		})
		m.setup.stage = stage

		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = updated.(model)
		if cmd != nil {
			t.Fatalf("right arrow at stage %v should not return a command", stage)
		}
		if m.setup.stage != stage {
			t.Fatalf("right arrow advanced stage %v to %v", stage, m.setup.stage)
		}
		if !m.setup.visible {
			t.Fatalf("right arrow at stage %v should not hide setup", stage)
		}
	}
	if saveCalls != 0 {
		t.Fatalf("right arrow should not save setup, got %d save calls", saveCalls)
	}
}

func TestSetupEnterDoesNotAdvanceSpaceOnlyStages(t *testing.T) {
	saveCalls := 0
	for _, stage := range []setupStage{
		setupStageWelcome,
		setupStageCredentials,
		setupStageSafety,
	} {
		m := newModel(context.Background(), Options{
			Setup: SetupOptions{
				Visible: true,
				Providers: []SetupProviderOption{
					{ID: "ollama", Name: "Ollama Local", DefaultModel: "llama3.1", Local: true},
				},
				Save: func(selection SetupSelection) (SetupResult, error) {
					saveCalls++
					return SetupResult{}, nil
				},
			},
		})
		m.setup.stage = stage

		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(model)
		if cmd != nil {
			t.Fatalf("enter at stage %v should not return a command", stage)
		}
		if m.setup.stage != stage {
			t.Fatalf("enter advanced stage %v to %v", stage, m.setup.stage)
		}
		if !m.setup.visible {
			t.Fatalf("enter at stage %v should not hide setup", stage)
		}
	}
	if saveCalls != 0 {
		t.Fatalf("enter on space-only steps should not save setup, got %d save calls", saveCalls)
	}
}

func TestSetupProviderRequiresEnter(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
			},
		},
	})
	m.setup.stage = setupStageProvider

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("space on provider step should not return a command")
	}
	if m.setup.stage != setupStageProvider {
		t.Fatalf("space on provider step advanced to %v", m.setup.stage)
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("enter on provider step should not return a command")
	}
	if m.setup.stage != setupStageCredentials {
		t.Fatalf("enter on provider step should advance to credentials, got %v", m.setup.stage)
	}
}

func TestSetupReadyRequiresEnter(t *testing.T) {
	saveCalls := 0
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
			},
			Save: func(selection SetupSelection) (SetupResult, error) {
				saveCalls++
				return SetupResult{
					Provider: config.ProviderProfile{
						Name:      selection.CatalogID,
						CatalogID: selection.CatalogID,
						Model:     selection.Model,
					},
				}, nil
			},
		},
	})
	m.setup.stage = setupStageReady

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("space on ready step should not return a command")
	}
	if saveCalls != 0 {
		t.Fatalf("space on ready step should not save setup, got %d calls", saveCalls)
	}
	if !m.setup.visible || m.setup.stage != setupStageReady {
		t.Fatalf("space on ready step should keep setup visible at ready, visible=%v stage=%v", m.setup.visible, m.setup.stage)
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("enter on ready step should stay in the fullscreen chat surface")
	}
	if saveCalls != 1 {
		t.Fatalf("enter on ready step should save once, got %d calls", saveCalls)
	}
	if m.setup.visible {
		t.Fatal("enter on ready step should open chat")
	}
}

func TestSetupCredentialsAcceptsPastedAPIKeyWithoutRenderingSecret(t *testing.T) {
	const secret = "sk-pasted-secret"
	var saved SetupSelection
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "ollama-cloud", Name: "Ollama Cloud", DefaultModel: "qwen3-coder:480b", EnvVar: "OLLAMA_API_KEY", RequiresAuth: true},
			},
			Save: func(selection SetupSelection) (SetupResult, error) {
				saved = selection
				return SetupResult{
					ConfigPath: "/tmp/zero/config.json",
					Provider: config.ProviderProfile{
						Name:      selection.CatalogID,
						CatalogID: selection.CatalogID,
						Model:     selection.Model,
						APIKey:    selection.APIKey,
					},
				}, nil
			},
		},
	})
	m.width = 96
	m.height = 30
	m.setup.stage = setupStageCredentials

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(secret)})
	m = updated.(model)
	view := plainRender(t, m.View())
	if strings.Contains(view, secret) {
		t.Fatalf("setup view leaked pasted API key:\n%s", view)
	}
	if !strings.Contains(view, strings.Repeat("*", len(secret))) {
		t.Fatalf("setup view should show masked API key, got:\n%s", view)
	}

	for m.setup.stage != setupStageReady {
		m = pressSetupContinue(m)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if saved.APIKey != secret {
		t.Fatalf("saved APIKey = %q, want pasted secret", saved.APIKey)
	}
	if m.providerProfile.APIKey != secret {
		t.Fatalf("providerProfile APIKey = %q, want pasted secret", m.providerProfile.APIKey)
	}
}

func TestSetupProviderStepKeepsModelInSelectedDetail(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
				{ID: "anthropic", Name: "Anthropic", DefaultModel: "claude-sonnet-4.5", EnvVar: "ANTHROPIC_API_KEY", RequiresAuth: true},
			},
		},
	})
	m.width = 180
	m.height = 30
	m.setup.stage = setupStageProvider

	foundProviderRow := false
	foundDefaultDetail := false
	titleColumn := -1
	providerColumn := -1
	defaultColumn := -1
	for _, line := range strings.Split(plainRender(t, m.View()), "\n") {
		row := strings.TrimSpace(line)
		if strings.Contains(row, "Choose a provider") {
			titleColumn = displayColumn(line, "Choose a provider")
		}
		if strings.Contains(row, "Default model: gpt-4.1") {
			foundDefaultDetail = true
			defaultColumn = displayColumn(line, "Default model")
		}
		if !strings.Contains(row, "OpenAI") {
			continue
		}
		foundProviderRow = true
		providerColumn = displayColumn(line, "OpenAI")
		if strings.Contains(row, "gpt-4.1") {
			t.Fatalf("provider row should not render model as a column: %q", row)
		}
		if got := lipgloss.Width(row); got > 44 {
			t.Fatalf("provider row width = %d, want <= 44: %q", got, row)
		}
	}
	if !foundProviderRow {
		t.Fatal("provider row missing from setup view")
	}
	if !foundDefaultDetail {
		t.Fatal("selected provider default model detail missing from setup view")
	}
	if providerColumn < 0 || defaultColumn < 0 || providerColumn != defaultColumn {
		t.Fatalf("default model detail should align with provider names, provider column %d detail column %d", providerColumn, defaultColumn)
	}
	if titleColumn < 0 || titleColumn != providerColumn {
		t.Fatalf("provider title should align with provider names, title column %d provider column %d", titleColumn, providerColumn)
	}
}

func TestSetupProviderSelectionDoesNotShiftBlock(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
				{ID: "anthropic", Name: "Anthropic", DefaultModel: "claude-sonnet-4.5", EnvVar: "ANTHROPIC_API_KEY", RequiresAuth: true},
				{ID: "ollama", Name: "Ollama Local", DefaultModel: "llama3.1", Local: true},
			},
		},
	})
	m.width = 120
	m.height = 30
	m.setup.stage = setupStageProvider

	openAIColumn := displayColumnForVisibleLine(t, m.View(), "OpenAI")
	titleColumn := displayColumnForVisibleLine(t, m.View(), "Choose a provider")

	m.moveSetupProvider(1)
	if got := displayColumnForVisibleLine(t, m.View(), "OpenAI"); got != openAIColumn {
		t.Fatalf("OpenAI column shifted after selecting Anthropic: got %d want %d", got, openAIColumn)
	}
	if got := displayColumnForVisibleLine(t, m.View(), "Choose a provider"); got != titleColumn {
		t.Fatalf("title column shifted after selecting Anthropic: got %d want %d", got, titleColumn)
	}

	m.moveSetupProvider(1)
	if got := displayColumnForVisibleLine(t, m.View(), "OpenAI"); got != openAIColumn {
		t.Fatalf("OpenAI column shifted after selecting Ollama: got %d want %d", got, openAIColumn)
	}
	if got := displayColumnForVisibleLine(t, m.View(), "Choose a provider"); got != titleColumn {
		t.Fatalf("title column shifted after selecting Ollama: got %d want %d", got, titleColumn)
	}
}

func TestSetupProviderLongCatalogUsesVisibleWindow(t *testing.T) {
	providers := make([]SetupProviderOption, 0, 14)
	for index := 0; index < 14; index++ {
		providers = append(providers, SetupProviderOption{
			ID:           "provider",
			Name:         "Provider " + string(rune('A'+index)),
			DefaultModel: "model",
		})
	}
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible:   true,
			Providers: providers,
		},
	})
	m.width = 96
	m.height = 18
	m.setup.stage = setupStageProvider

	initial := plainRender(t, m.View())
	if !strings.Contains(initial, "Provider A") || strings.Contains(initial, "Provider N") {
		t.Fatalf("initial provider window should show the first rows only:\n%s", initial)
	}

	m.setup.selected = len(providers) - 1
	scrolled := plainRender(t, m.View())
	if !strings.Contains(scrolled, "Provider N") || strings.Contains(scrolled, "Provider A") {
		t.Fatalf("scrolled provider window should follow the selected row:\n%s", scrolled)
	}
}

func TestSetupOllamaCloudCredentialCopyMentionsAPIKey(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "ollama-cloud", Name: "Ollama Cloud", DefaultModel: "qwen3-coder:480b", EnvVar: "OLLAMA_API_KEY", RequiresAuth: true},
			},
		},
	})
	m.setup.stage = setupStageCredentials

	view := plainRender(t, m.View())
	for _, want := range []string{"Paste your Ollama Cloud API key", "leave blank to use OLLAMA_API_KEY from your shell", "Saved keys stay in your user config"} {
		if !strings.Contains(view, want) {
			t.Fatalf("credential copy missing %q:\n%s", want, view)
		}
	}
}

func TestSetupCredentialLinesCenterLikeWelcome(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "ollama-cloud", Name: "Ollama Cloud", DefaultModel: "qwen3-coder:480b", EnvVar: "OLLAMA_API_KEY", RequiresAuth: true},
			},
		},
	})
	m.width = 120
	m.height = 30
	m.setup.stage = setupStageCredentials

	view := m.View()
	assertSetupLineCentered(t, view, "Credentials", m.width)
	assertSetupLineCentered(t, view, "Paste your", m.width)
	assertSetupLineCentered(t, view, "paste key", m.width)
	assertSetupLineCentered(t, view, "Saved keys", m.width)
}

func TestSetupCredentialEmptyInputDoesNotHighlightPlaceholder(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "ollama-cloud", Name: "Ollama Cloud", DefaultModel: "qwen3-coder:480b", EnvVar: "OLLAMA_API_KEY", RequiresAuth: true},
			},
		},
	})
	m.setup.stage = setupStageCredentials

	line := m.setupAPIKeyInputLine(80)
	plain := plainRender(t, line)
	if plain != "paste key or leave blank" {
		t.Fatalf("empty API key input = %q, want placeholder only", plain)
	}
	if strings.Count(plain, "paste") != 1 {
		t.Fatalf("empty API key input should render placeholder once, got %q", plain)
	}
}

func TestSetupProgressRendersAboveFooter(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
			},
		},
	})
	m.width = 96
	m.height = 24
	m.setup.stage = setupStageProvider

	lines := strings.Split(plainRender(t, m.View()), "\n")
	stepIndex := -1
	footerIndex := -1
	for index, line := range lines {
		if strings.Contains(line, "2/5") {
			stepIndex = index
		}
		if strings.Contains(line, "Enter continue") {
			footerIndex = index
		}
		if strings.Contains(line, "Choose a provider") && strings.Contains(line, "2/5") {
			t.Fatalf("progress should not render in setup body: %q", line)
		}
	}
	if stepIndex < 0 || footerIndex < 0 {
		t.Fatalf("missing setup progress/footer, step=%d footer=%d view:\n%s", stepIndex, footerIndex, strings.Join(lines, "\n"))
	}
	if stepIndex != footerIndex-1 {
		t.Fatalf("progress should render immediately above footer, step line %d footer line %d", stepIndex, footerIndex)
	}
}

func TestSetupReadyFooterUsesEnter(t *testing.T) {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
			},
		},
	})
	m.width = 96
	m.height = 24
	m.setup.stage = setupStageReady

	view := plainRender(t, m.View())
	if !strings.Contains(view, "Enter to save and start chat") {
		t.Fatalf("ready footer should use Enter, got:\n%s", view)
	}
	if strings.Contains(view, "Space to save and start chat") {
		t.Fatalf("ready footer should not advertise Space, got:\n%s", view)
	}
}

func pressSetupContinue(m model) model {
	if m.setup.stage == setupStageProvider || m.setupCredentialInputActive() || m.setup.stage == setupStageReady {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return updated.(model)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	return updated.(model)
}

func displayColumnForVisibleLine(t *testing.T, view string, marker string) int {
	t.Helper()
	for _, line := range strings.Split(plainRender(t, view), "\n") {
		if strings.Contains(line, marker) {
			return displayColumn(line, marker)
		}
	}
	t.Fatalf("marker %q missing from view:\n%s", marker, view)
	return -1
}

func displayColumn(line string, marker string) int {
	index := strings.Index(line, marker)
	if index < 0 {
		return -1
	}
	return lipgloss.Width(line[:index])
}

func assertSetupLineCentered(t *testing.T, view string, marker string, width int) {
	t.Helper()
	line := visibleLineForMarker(t, view, marker)
	trimmed := strings.TrimSpace(line)
	start := lipgloss.Width(line[:strings.Index(line, strings.TrimLeft(line, " "))])
	midpoint := start + lipgloss.Width(trimmed)/2
	want := width / 2
	if delta := absInt(midpoint - want); delta > 2 {
		t.Fatalf("line %q midpoint = %d, want near %d (delta %d)", trimmed, midpoint, want, delta)
	}
}

func visibleLineForMarker(t *testing.T, view string, marker string) string {
	t.Helper()
	for _, line := range strings.Split(plainRender(t, view), "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("marker %q missing from view:\n%s", marker, view)
	return ""
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
