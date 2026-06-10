package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
		"Add API key",
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
