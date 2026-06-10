package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
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
