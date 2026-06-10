package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestProviderCommandOpensOnboardingWizard(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/provider")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /provider to open the onboarding wizard without starting a run")
	}
	if next.providerWizard == nil {
		t.Fatal("expected provider wizard to be open")
	}
	if next.providerWizard.step != providerWizardStepProvider {
		t.Fatalf("wizard step = %v, want provider catalog", next.providerWizard.step)
	}
	if len(next.transcript) != len(m.transcript) {
		t.Fatalf("/provider should not append transcript output when opening wizard")
	}
	view := plainRender(t, next.View())
	for _, want := range []string{
		"Provider setup",
		"Choose provider",
		"OpenAI",
		"Anthropic",
		"Google",
		"Groq",
		"OpenRouter",
		"Ollama",
	} {
		assertContains(t, view, want)
	}
}

func TestProviderWizardUsesRuntimeProviderCatalog(t *testing.T) {
	wizard := newModel(context.Background(), Options{}).newProviderWizard()
	got := map[string]bool{}
	for _, provider := range wizard.providers {
		got[provider.ID] = true
		if !providercatalog.RuntimeSupported(provider) {
			t.Fatalf("wizard included unsupported provider %q", provider.ID)
		}
	}

	for _, provider := range providercatalog.All() {
		if !providercatalog.RuntimeSupported(provider) {
			continue
		}
		if !got[provider.ID] {
			t.Fatalf("wizard omitted runtime catalog provider %q", provider.ID)
		}
	}
	for _, unsupported := range []string{"bedrock", "vertex"} {
		if got[unsupported] {
			t.Fatalf("wizard should not include unsupported provider %q", unsupported)
		}
	}
}

func TestProviderWizardModelsAreProviderScoped(t *testing.T) {
	tests := []struct {
		provider string
		want     []string
		notWant  []string
	}{
		{
			provider: "ollama",
			want:     []string{"llama3.1", "qwen2.5-coder:32b"},
			notWant:  []string{"gpt-4.1", "gpt-5", "openai/gpt-4.1"},
		},
		{
			provider: "groq",
			want:     []string{"llama-3.3-70b-versatile", "openai/gpt-oss-120b"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5"},
		},
		{
			provider: "mistral",
			want:     []string{"mistral-large-latest", "codestral-latest"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			descriptor, ok := providercatalog.Get(tt.provider)
			if !ok {
				t.Fatalf("provider %q missing from catalog", tt.provider)
			}
			models := providerWizardModelOptions(descriptor)
			got := map[string]bool{}
			for _, model := range models {
				got[model.ID] = true
			}
			for _, want := range tt.want {
				if !got[want] {
					t.Fatalf("%s models missing %q; got %#v", tt.provider, want, providerWizardModelIDs(models))
				}
			}
			for _, notWant := range tt.notWant {
				if got[notWant] {
					t.Fatalf("%s models should not include %q; got %#v", tt.provider, notWant, providerWizardModelIDs(models))
				}
			}
		})
	}
}

func TestProviderWizardAdvancesProviderAPIKeyAndModelSteps(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = openProviderWizardForTest(t, m)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(model)
	if got := next.providerWizard.currentProvider().ID; got != "anthropic" {
		t.Fatalf("after down, selected provider = %q, want anthropic", got)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepCredential {
		t.Fatalf("wizard step = %v, want credential", next.providerWizard.step)
	}
	view := plainRender(t, next.View())
	for _, want := range []string{
		"Paste API key",
		"ANTHROPIC_API_KEY",
		"zero providers add anthropic --api-key-env ANTHROPIC_API_KEY --set-active",
	} {
		assertContains(t, view, want)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("wizard step = %v, want model", next.providerWizard.step)
	}
	view = plainRender(t, next.View())
	for _, want := range []string{
		"Choose model",
		"claude-sonnet-4.5",
	} {
		assertContains(t, view, want)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepDone {
		t.Fatalf("wizard step = %v, want done", next.providerWizard.step)
	}
	view = plainRender(t, next.View())
	for _, want := range []string{
		"Ready to connect",
		"provider: Anthropic",
		"model: claude-sonnet-4.5",
		"zero providers check anthropic --connectivity",
	} {
		assertContains(t, view, want)
	}
}

func TestProviderWizardSkipsAPIKeyForLocalProvidersAndEscCloses(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "ollama")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("local provider step = %v, want model", next.providerWizard.step)
	}
	view := plainRender(t, next.View())
	if strings.Contains(view, "Add API key") {
		t.Fatalf("local provider should skip API key step, got view:\n%s", view)
	}
	assertContains(t, view, "llama3.1")

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(model)
	if next.providerWizard != nil {
		t.Fatal("Esc should close provider wizard")
	}
}

func TestProviderWizardAcceptsPastedAPIKeyWithoutRenderingSecret(t *testing.T) {
	const secret = "AIza-secret-123"
	m := newModel(context.Background(), Options{})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "google")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.providerWizard.step != providerWizardStepCredential {
		t.Fatalf("wizard step = %v, want credential", next.providerWizard.step)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(secret)})
	next = updated.(model)
	if next.providerWizard.apiKey != secret {
		t.Fatalf("wizard api key was not captured from paste")
	}
	view := plainRender(t, next.View())
	for _, want := range []string{"Paste API key", "api key >", "pasted key", "session only"} {
		assertContains(t, view, want)
	}
	assertNotContains(t, view, secret)
}

func TestProviderWizardAppliesPastedKeyToCurrentSession(t *testing.T) {
	const secret = "AIza-secret-123"
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			captured = profile
			return &fakeProvider{}, nil
		},
	})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "google")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(secret)})
	next = updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("wizard step = %v, want model", next.providerWizard.step)
	}
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepDone {
		t.Fatalf("wizard step = %v, want done", next.providerWizard.step)
	}
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if next.providerWizard != nil {
		t.Fatal("successful provider apply should close the wizard")
	}
	if captured.CatalogID != "google" || captured.ProviderKind != config.ProviderKindGoogle {
		t.Fatalf("captured profile provider = %#v, want google", captured)
	}
	if captured.APIKey != secret {
		t.Fatalf("captured API key = %q, want pasted secret", captured.APIKey)
	}
	if captured.APIKeyEnv != "" {
		t.Fatalf("captured APIKeyEnv = %q, want empty when using pasted key", captured.APIKeyEnv)
	}
	if next.providerProfile.APIKey != secret || next.providerName != "google" {
		t.Fatalf("model provider state was not updated: provider=%q profile=%#v", next.providerName, next.providerProfile)
	}
}

func TestProviderWizardUsesLiveDiscoveredModels(t *testing.T) {
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		DiscoverProviderModels: func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			captured = profile
			return []providermodeldiscovery.Model{{ID: "live-b"}, {ID: "live-a"}}, nil
		},
	})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "ollama")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("wizard step = %v, want model", next.providerWizard.step)
	}
	if cmd == nil {
		t.Fatal("entering model step should start live model discovery")
	}
	msg := cmd()
	updated, _ = next.Update(msg)
	next = updated.(model)

	if captured.CatalogID != "ollama" {
		t.Fatalf("discovery profile = %#v, want ollama", captured)
	}
	if got := providerWizardModelIDs(next.providerWizard.models); strings.Join(got, ",") != "live-b,live-a" {
		t.Fatalf("wizard models = %#v, want live discovered models", got)
	}
	view := plainRender(t, next.View())
	assertContains(t, view, "models: live")
	assertNotContains(t, view, "gpt-4.1")
}

func TestProviderWizardKeepsFallbackModelsWhenLiveDiscoveryFails(t *testing.T) {
	m := newModel(context.Background(), Options{
		DiscoverProviderModels: func(context.Context, config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return nil, errors.New("offline")
		},
	})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "ollama")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("entering model step should start live model discovery")
	}
	updated, _ = next.Update(cmd())
	next = updated.(model)

	if got := providerWizardModelIDs(next.providerWizard.models); !containsString(got, "llama3.1") {
		t.Fatalf("wizard models = %#v, want fallback model llama3.1", got)
	}
	view := plainRender(t, next.View())
	assertContains(t, view, "models: fallback")
	assertContains(t, view, "offline")
}

func TestProviderWizardRendersDiscoveredModelMetadata(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "openai")
	m.providerWizard.step = providerWizardStepModel

	next := m.applyProviderModelsDiscovered(providerModelsDiscoveredMsg{
		providerID: "openai",
		models: []providermodeldiscovery.Model{{
			ID:            "gpt-4.1",
			Description:   "GPT-4.1",
			ContextWindow: 1048576,
			ToolCall:      true,
			Reasoning:     true,
			InputCost:     2,
			OutputCost:    8,
			Source:        "models.dev",
		}},
	})

	view := plainRender(t, next.View())
	assertContains(t, view, "models: models.dev")
	assertContains(t, view, "GPT-4.1")
	assertContains(t, view, "1M ctx")
	assertContains(t, view, "tools")
	assertContains(t, view, "reasoning")
}

func TestProviderWizardModelStepUsesFriendlyNamesAndStaysCompact(t *testing.T) {
	wizard := &providerWizardState{
		step:        providerWizardStepModel,
		modelSource: "models.dev",
		models:      providerWizardManyModelsForTest(18),
	}
	wizard.models[0] = providerWizardModel{
		ID:          "x-ai/grok-4.3",
		Description: "Grok 4.3",
		Meta:        "1M ctx · tools · reasoning",
	}

	view := plainRender(t, strings.Join(wizard.renderModelStep(84), "\n"))
	assertContains(t, view, "search > Search model")
	assertNotContains(t, view, "SearchÃ")
	assertNotContains(t, view, "Searchâ")
	assertContains(t, view, "Grok 4.3")
	assertNotContains(t, view, "x-ai/grok-4.3")
	assertContains(t, view, "8 more models")
	assertNotContains(t, view, "Model 17")
}

func TestProviderWizardModelSearchFiltersAndAppliesRawModelID(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.providerWizard = &providerWizardState{
		step:        providerWizardStepModel,
		modelSource: "models.dev",
		providers: []providercatalog.Descriptor{{
			ID:                  "openrouter",
			Name:                "OpenRouter",
			Transport:           providercatalog.TransportOpenAICompatible,
			DefaultBaseURL:      "https://openrouter.ai/api/v1",
			DefaultModel:        "openai/gpt-4.1",
			AuthEnvVars:         []string{"OPENROUTER_API_KEY"},
			RequiresAuth:        true,
			SupportedAPIFormats: []providercatalog.APIFormat{providercatalog.APIFormatOpenAIChatCompletions},
		}},
		models: []providerWizardModel{
			{ID: "openai/gpt-4.1", Description: "GPT-4.1", Meta: "1M ctx · tools"},
			{ID: "deepseek/deepseek-chat", Description: "DeepSeek Chat", Meta: "64K ctx · tools"},
			{ID: "deepseek/deepseek-v3.2", Description: "DeepSeek V3.2", Meta: "128K ctx · tools"},
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("deep")})
	next := updated.(model)

	if next.providerWizard.modelSearch != "deep" {
		t.Fatalf("model search = %q, want deep", next.providerWizard.modelSearch)
	}
	if got := next.providerWizard.currentModel().ID; got != "deepseek/deepseek-chat" {
		t.Fatalf("current model ID = %q, want raw OpenRouter ID", got)
	}
	view := plainRender(t, next.View())
	assertContains(t, view, "DeepSeek Chat")
	assertContains(t, view, "DeepSeek V3.2")
	assertNotContains(t, view, "GPT-4.1")
}

func openProviderWizardForTest(t *testing.T, m model) model {
	t.Helper()
	m.input.SetValue("/provider")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.providerWizard == nil {
		t.Fatal("expected provider wizard to be open")
	}
	return next
}

func providerWizardManyModelsForTest(count int) []providerWizardModel {
	models := make([]providerWizardModel, 0, count)
	for index := 0; index < count; index++ {
		models = append(models, providerWizardModel{
			ID:          fmt.Sprintf("provider/model-%02d", index),
			Description: fmt.Sprintf("Model %02d", index),
			Meta:        "tools",
		})
	}
	return models
}

func providerWizardProviderIndex(t *testing.T, wizard *providerWizardState, id string) int {
	t.Helper()
	for index, provider := range wizard.providers {
		if provider.ID == id {
			return index
		}
	}
	t.Fatalf("provider %q not found in wizard providers", id)
	return 0
}

func providerWizardModelIDs(models []providerWizardModel) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
