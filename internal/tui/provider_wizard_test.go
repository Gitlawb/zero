package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func TestProviderWizardStartsAtFirstProvider(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "ollama-cloud",
		ModelName:    "cogito-2.1:671b",
	})
	m.providerProfile = config.ProviderProfile{
		Name:      "ollama-cloud",
		CatalogID: "ollama-cloud",
		Model:     "cogito-2.1:671b",
	}

	next := openProviderWizardForTest(t, m)
	if next.providerWizard.selectedProvider != 0 {
		t.Fatalf("selected provider = %d, want first provider", next.providerWizard.selectedProvider)
	}
	if got, want := next.providerWizard.currentProvider().ID, next.providerWizard.providers[0].ID; got != want {
		t.Fatalf("current provider = %q, want first provider %q", got, want)
	}
}

func TestProviderWizardReplacesEmptyStateWordmark(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 120
	m.height = 34
	m.input.SetValue("/provider")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	next.width = m.width
	next.height = m.height

	view := plainRender(t, next.View())
	assertContains(t, view, "Provider setup")
	assertNotContains(t, view, emptyStateTagline)
	assertNotContains(t, view, "███████")
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
		"Enter continue",
	} {
		assertContains(t, view, want)
	}
	assertNotContains(t, view, "zero providers add anthropic")

	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("wizard step = %v, want model", next.providerWizard.step)
	}
	if cmd == nil {
		t.Fatal("entering model step should start live model discovery")
	}
	view = plainRender(t, next.View())
	assertContains(t, view, "Checking available models")
	assertNotContains(t, view, "claude-sonnet-4.5")

	updated, _ = next.Update(providerModelsDiscoveredMsg{
		providerID: "anthropic",
		token:      next.providerWizard.discoveryToken,
		models: []providermodeldiscovery.Model{{
			ID:          "claude-sonnet-4.5",
			Description: "claude-sonnet-4.5",
		}},
	})
	next = updated.(model)
	view = plainRender(t, next.View())
	for _, want := range []string{
		"Choose a model",
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
		"Provider    Anthropic",
		"Model       claude-sonnet-4.5",
		"Credential  ANTHROPIC_API_KEY env var",
		"Press Enter to save and start using this provider.",
	} {
		assertContains(t, view, want)
	}
	assertNotContains(t, view, "zero providers check")
}

func TestProviderWizardSupportsLeftAndGuardedRightNavigation(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = openProviderWizardForTest(t, m)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	next := updated.(model)
	if next.providerWizard.step != providerWizardStepCredential {
		t.Fatalf("right from provider step = %v, want credential", next.providerWizard.step)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyLeft})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepProvider {
		t.Fatalf("left from credential step = %v, want provider", next.providerWizard.step)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	clearProviderAuthEnvForTest(t, next.providerWizard.currentProvider())
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRight})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepCredential {
		t.Fatalf("right from empty credential step = %v, want credential", next.providerWizard.step)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-test")})
	next = updated.(model)
	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyRight})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("right from entered credential step = %v, want model", next.providerWizard.step)
	}
	if cmd == nil {
		t.Fatal("right from entered credential should start live model discovery")
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRight})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("right while loading step = %v, want model", next.providerWizard.step)
	}

	updated, _ = next.Update(providerModelsDiscoveredMsg{
		providerID: next.providerWizard.currentProvider().ID,
		token:      next.providerWizard.discoveryToken,
		models: []providermodeldiscovery.Model{{
			ID:          next.providerWizard.currentProvider().DefaultModel,
			Description: next.providerWizard.currentProvider().DefaultModel,
		}},
	})
	next = updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRight})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepDone {
		t.Fatalf("right from model step = %v, want ready", next.providerWizard.step)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyLeft})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("left from ready step = %v, want model", next.providerWizard.step)
	}
}

func TestProviderWizardRightAllowsExistingCredentialEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	m := newModel(context.Background(), Options{})
	m = openProviderWizardForTest(t, m)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.providerWizard.step != providerWizardStepCredential {
		t.Fatalf("enter from provider step = %v, want credential", next.providerWizard.step)
	}

	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyRight})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("right with env credential step = %v, want model", next.providerWizard.step)
	}
	if cmd == nil {
		t.Fatal("right with env credential should start live model discovery")
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
	if strings.Contains(view, "Paste API key") {
		t.Fatalf("local provider should skip API key step, got view:\n%s", view)
	}
	assertContains(t, view, "Checking available models")
	assertNotContains(t, view, "llama3.1")

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
	for _, want := range []string{"Paste API key", "api key >", "saved in your user config"} {
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
	next = finishProviderWizardModelDiscoveryForTest(t, next)
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

func TestProviderWizardPersistsPastedKeyToUserConfig(t *testing.T) {
	const secret = "ollama-secret-123"
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		UserConfigPath: configPath,
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			captured = profile
			return &fakeProvider{}, nil
		},
	})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "ollama-cloud")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(secret)})
	next = updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	next = finishProviderWizardModelDiscoveryForTest(t, next)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if captured.APIKey != secret {
		t.Fatalf("captured APIKey = %q, want pasted secret", captured.APIKey)
	}
	persisted := readProviderWizardConfigFixture(t, configPath)
	if persisted.ActiveProvider != "ollama-cloud" {
		t.Fatalf("active provider = %q, want ollama-cloud", persisted.ActiveProvider)
	}
	if len(persisted.Providers) != 1 {
		t.Fatalf("providers length = %d, want 1", len(persisted.Providers))
	}
	profile := persisted.Providers[0]
	if profile.Name != "ollama-cloud" || profile.CatalogID != "ollama-cloud" {
		t.Fatalf("persisted provider identity = %#v, want ollama-cloud", profile)
	}
	if profile.APIKey != secret {
		t.Fatalf("persisted APIKey = %q, want pasted secret", profile.APIKey)
	}
	if profile.APIKeyEnv != "" {
		t.Fatalf("persisted APIKeyEnv = %q, want empty for pasted key", profile.APIKeyEnv)
	}
}

func TestProviderWizardUsesAPIKeyEnvForCurrentSessionWithoutPersistingSecret(t *testing.T) {
	const secret = "ollama-env-secret"
	t.Setenv("OLLAMA_API_KEY", secret)
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		UserConfigPath: configPath,
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			captured = profile
			return &fakeProvider{}, nil
		},
	})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "ollama-cloud")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	next = finishProviderWizardModelDiscoveryForTest(t, next)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if captured.APIKey != secret {
		t.Fatalf("runtime APIKey = %q, want env secret", captured.APIKey)
	}
	if captured.APIKeyEnv != "OLLAMA_API_KEY" {
		t.Fatalf("runtime APIKeyEnv = %q, want OLLAMA_API_KEY", captured.APIKeyEnv)
	}
	if next.providerProfile.APIKey != "" {
		t.Fatalf("session providerProfile persisted APIKey = %q, want empty", next.providerProfile.APIKey)
	}
	if next.providerProfile.APIKeyEnv != "OLLAMA_API_KEY" {
		t.Fatalf("session providerProfile APIKeyEnv = %q, want OLLAMA_API_KEY", next.providerProfile.APIKeyEnv)
	}
	persisted := readProviderWizardConfigFixture(t, configPath)
	if len(persisted.Providers) != 1 {
		t.Fatalf("providers length = %d, want 1", len(persisted.Providers))
	}
	profile := persisted.Providers[0]
	if profile.APIKey != "" {
		t.Fatalf("persisted APIKey = %q, want empty", profile.APIKey)
	}
	if profile.APIKeyEnv != "OLLAMA_API_KEY" {
		t.Fatalf("persisted APIKeyEnv = %q, want OLLAMA_API_KEY", profile.APIKeyEnv)
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
	view := plainRender(t, next.View())
	assertContains(t, view, "Checking available models")
	assertNotContains(t, view, "llama3.1")

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	waiting := updated.(model)
	if waiting.providerWizard.step != providerWizardStepModel {
		t.Fatalf("enter while loading step = %v, want model", waiting.providerWizard.step)
	}
	assertContains(t, plainRender(t, waiting.View()), "Models are still loading.")

	msg := cmd()
	updated, _ = next.Update(msg)
	next = updated.(model)

	if captured.CatalogID != "ollama" {
		t.Fatalf("discovery profile = %#v, want ollama", captured)
	}
	if got := providerWizardModelIDs(next.providerWizard.models); strings.Join(got, ",") != "live-b,live-a" {
		t.Fatalf("wizard models = %#v, want live discovered models", got)
	}
	view = plainRender(t, next.View())
	assertNotContains(t, view, "models: live")
	assertContains(t, view, "search > \u258cmodel name...")
	assertNotContains(t, view, "gpt-4.1")
}

func TestProviderWizardIgnoresStaleDiscoveryForSameProvider(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = openProviderWizardForTest(t, m)
	m.providerWizard.selectedProvider = providerWizardProviderIndex(t, m.providerWizard, "ollama")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("first model entry should start discovery")
	}
	staleToken := next.providerWizard.discoveryToken

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyLeft})
	next = updated.(model)
	if next.providerWizard.step != providerWizardStepProvider {
		t.Fatalf("left from local model step = %v, want provider", next.providerWizard.step)
	}

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if cmd == nil {
		t.Fatal("second model entry should start discovery")
	}
	currentToken := next.providerWizard.discoveryToken
	if currentToken == staleToken {
		t.Fatalf("discovery token did not advance: %d", currentToken)
	}

	updated, _ = next.Update(providerModelsDiscoveredMsg{
		providerID: "ollama",
		token:      staleToken,
		models: []providermodeldiscovery.Model{{
			ID: "stale-model",
		}},
	})
	next = updated.(model)
	if got := providerWizardModelIDs(next.providerWizard.models); containsString(got, "stale-model") {
		t.Fatalf("stale discovery response applied: %#v", got)
	}
	if !next.providerWizard.modelLoading {
		t.Fatal("stale discovery response should not clear the active loading state")
	}

	updated, _ = next.Update(providerModelsDiscoveredMsg{
		providerID: "ollama",
		token:      currentToken,
		models: []providermodeldiscovery.Model{{
			ID: "fresh-model",
		}},
	})
	next = updated.(model)
	if got := providerWizardModelIDs(next.providerWizard.models); !containsString(got, "fresh-model") {
		t.Fatalf("fresh discovery response did not apply: %#v", got)
	}
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
	view := plainRender(t, next.View())
	assertContains(t, view, "Checking available models")
	assertNotContains(t, view, "llama3.1")

	updated, _ = next.Update(cmd())
	next = updated.(model)

	if got := providerWizardModelIDs(next.providerWizard.models); !containsString(got, "llama3.1") {
		t.Fatalf("wizard models = %#v, want fallback model llama3.1", got)
	}
	view = plainRender(t, next.View())
	assertContains(t, view, "Using built-in model list")
	assertNotContains(t, view, "offline")
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
	assertNotContains(t, view, "models: models.dev")
	assertNotContains(t, view, "models: OpenGateway")
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
	assertContains(t, view, "search > \u258cmodel name...")
	assertContains(t, view, "\u258c")
	assertNotContains(t, view, "SearchÃ")
	assertNotContains(t, view, "Searchâ")
	assertContains(t, view, "Grok 4.3")
	assertContains(t, view, "x-ai/grok-4.3 · 1M ctx · tools · reasoning")
	assertNotContains(t, view, "❯ x-ai/grok-4.3")
	assertNotContains(t, view, "more models")
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

func TestProviderWizardBlocksAdvanceWhenModelSearchHasNoMatches(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.providerWizard = &providerWizardState{
		step: providerWizardStepModel,
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
			{ID: "openai/gpt-4.1", Description: "GPT-4.1"},
			{ID: "deepseek/deepseek-chat", Description: "DeepSeek Chat"},
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("nomatch")})
	next := updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("wizard advanced to %v, want model step", next.providerWizard.step)
	}
	if next.providerWizard.err == "" {
		t.Fatal("expected model search error")
	}
	if got := next.providerWizard.currentModel().ID; got != "" {
		t.Fatalf("current model ID = %q, want empty when search has no matches", got)
	}
	view := plainRender(t, next.View())
	assertContains(t, view, "no matching models")
	assertContains(t, view, "choose a matching model")
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

func finishProviderWizardModelDiscoveryForTest(t *testing.T, m model) model {
	t.Helper()
	if m.providerWizard == nil {
		t.Fatal("expected provider wizard to be open")
	}
	if m.providerWizard.step != providerWizardStepModel {
		t.Fatalf("wizard step = %v, want model", m.providerWizard.step)
	}
	provider := m.providerWizard.currentProvider()
	modelID := firstProviderDisplayValue(provider.DefaultModel, "test-model")
	updated, _ := m.Update(providerModelsDiscoveredMsg{
		providerID: provider.ID,
		token:      m.providerWizard.discoveryToken,
		models: []providermodeldiscovery.Model{{
			ID:          modelID,
			Description: modelID,
		}},
	})
	return updated.(model)
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

func clearProviderAuthEnvForTest(t *testing.T, provider providercatalog.Descriptor) {
	t.Helper()
	for _, env := range provider.AuthEnvVars {
		env = strings.TrimSpace(env)
		if env != "" {
			t.Setenv(env, "")
		}
	}
}

func providerWizardModelIDs(models []providerWizardModel) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func readProviderWizardConfigFixture(t *testing.T, path string) config.FileConfig {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
