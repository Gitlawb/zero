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

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestModelPickerDetectsOllamaCloudFromBaseURL(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "custom-openai-compatible",
		ModelName:    "minimax-m3",
		ProviderProfile: config.ProviderProfile{
			Name:         "custom-openai-compatible",
			CatalogID:    "custom-openai-compatible",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKeyEnv:    "OLLAMA_API_KEY",
			Model:        "minimax-m3",
		},
	})

	picker := m.newModelPicker()
	if picker == nil {
		t.Fatal("expected model picker")
	}
	groups := pickerGroups(picker.items)
	if !contains(groups, "Ollama Cloud") {
		t.Fatalf("picker groups = %#v, want Ollama Cloud", groups)
	}
	got := pickerValues(picker.items)
	if !contains(got, "qwen3-coder:480b") {
		t.Fatalf("picker values = %#v, want Ollama Cloud models", got)
	}
	if contains(got, "custom-model") {
		t.Fatalf("picker should not show custom-openai-compatible fallback when URL is Ollama Cloud: %#v", got)
	}
}

func TestModelPickerRefreshesLiveModelsForActiveProvider(t *testing.T) {
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		ProviderName: "ollama-cloud",
		ModelName:    "minimax-m3",
		ProviderProfile: config.ProviderProfile{
			Name:         "ollama-cloud",
			CatalogID:    "ollama-cloud",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKey:       "ollama-key",
			Model:        "minimax-m3",
		},
		DiscoverProviderModels: func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			captured = profile
			return []providermodeldiscovery.Model{
				{ID: "live-cloud-a", Description: "Live Cloud A"},
				{ID: "live-cloud-b", Description: "Live Cloud B"},
			}, nil
		},
	})
	m.input.SetValue("/model")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.picker == nil {
		t.Fatal("expected model picker to open")
	}
	if cmd == nil {
		t.Fatal("opening /model for an active provider should start model discovery")
	}
	updated, _ = next.Update(cmd())
	next = updated.(model)

	if captured.CatalogID != "ollama-cloud" {
		t.Fatalf("discovery profile catalog = %q, want ollama-cloud", captured.CatalogID)
	}
	got := pickerValues(next.picker.items)
	if !contains(got, "live-cloud-a") || !contains(got, "live-cloud-b") {
		t.Fatalf("picker values = %#v, want live cloud models", got)
	}
}

func TestModelPickerTreatsCustomOpenAIEndpointAsCustomProvider(t *testing.T) {
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "custom-coder",
		ProviderProfile: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://proxy.example.test/v1",
			APIKey:       "proxy-key",
			Model:        "custom-coder",
		},
		DiscoverProviderModels: func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			captured = profile
			return []providermodeldiscovery.Model{
				{ID: "custom-coder-plus", Description: "Custom Coder Plus"},
			}, nil
		},
	})
	m.input.SetValue("/model")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.picker == nil {
		t.Fatal("expected model picker to open")
	}
	if cmd == nil {
		t.Fatal("opening /model for a custom endpoint should start model discovery")
	}
	updated, _ = next.Update(cmd())
	next = updated.(model)

	if captured.CatalogID != "custom-openai-compatible" || captured.ProviderKind != config.ProviderKindOpenAICompatible {
		t.Fatalf("discovery profile = %#v, want custom OpenAI-compatible identity", captured)
	}
	groups := pickerGroups(next.picker.items)
	if !contains(groups, "Custom OpenAI-compatible") {
		t.Fatalf("picker groups = %#v, want custom provider group", groups)
	}
	got := pickerValues(next.picker.items)
	if !contains(got, "custom-coder-plus") {
		t.Fatalf("picker values = %#v, want discovered custom model", got)
	}
	if contains(got, "gpt-4.1") {
		t.Fatalf("custom endpoint picker should not fall back to official OpenAI models: %#v", got)
	}
}

func TestModelPickerShowsLoadingUntilDiscoveryCompletes(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "ollama-cloud",
		ModelName:    "minimax-m3",
		ProviderProfile: config.ProviderProfile{
			Name:         "ollama-cloud",
			CatalogID:    "ollama-cloud",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKey:       "ollama-key",
			Model:        "minimax-m3",
		},
		DiscoverProviderModels: func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return []providermodeldiscovery.Model{
				{ID: "live-cloud-a", Description: "Live Cloud A"},
				{ID: "live-cloud-b", Description: "Live Cloud B"},
			}, nil
		},
	})
	m.input.SetValue("/model")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("expected opening the model picker to start background discovery")
	}
	// The list shows immediately (no blocking overlay) before discovery returns.
	immediate := plainRender(t, m.pickerOverlay(100))
	assertNotContains(t, immediate, "Checking available models...")
	assertNotContains(t, immediate, "Live Cloud A")
	if m.picker == nil {
		t.Fatal("picker should be open immediately")
	}

	// When the provider's discovery returns, its section shows the live models.
	updated, _ = m.Update(modelPickerModelsDiscoveredMsg{
		providerID: "ollama-cloud",
		models:     []providermodeldiscovery.Model{{ID: "live-cloud-a", Description: "Live Cloud A"}},
	})
	m = updated.(model)
	loaded := plainRender(t, m.pickerOverlay(100))
	assertContains(t, loaded, "Live Cloud A")
}

func TestModelPickerMetadataOmitsCredentialEnv(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "ollama-cloud",
		ModelName:    "minimax-m3",
		ProviderProfile: config.ProviderProfile{
			Name:         "ollama-cloud",
			CatalogID:    "ollama-cloud",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKeyEnv:    "OLLAMA_API_KEY",
			Model:        "minimax-m3",
		},
	})
	m.modelPickerLiveByProvider = map[string][]providermodeldiscovery.Model{
		"ollama-cloud": {
			{
				ID:            "cogito-2.1:671b",
				ContextWindow: 163840,
				ToolCall:      true,
				Reasoning:     true,
			},
		},
	}
	m.picker = m.newModelPicker()
	if m.picker == nil {
		t.Fatal("expected model picker")
	}
	target := pickerIndex(m.picker.items, "cogito-2.1:671b")
	if target < 0 {
		t.Fatalf("expected cogito model in picker, got %#v", pickerValues(m.picker.items))
	}
	m.picker.selected = target

	view := plainRender(t, m.pickerOverlay(100))
	assertContains(t, view, "163K ctx")
	assertContains(t, view, "tools")
	assertContains(t, view, "reasoning")
	assertNotContains(t, view, "OLLAMA_API_KEY")
	for _, item := range m.picker.items {
		assertNotContains(t, item.Meta, "API_KEY")
	}
}

func TestModelPickerFallsBackWhenDiscoveryFails(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "ollama-cloud",
		ModelName:    "minimax-m3",
		ProviderProfile: config.ProviderProfile{
			Name:         "ollama-cloud",
			CatalogID:    "ollama-cloud",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKey:       "ollama-key",
			Model:        "minimax-m3",
		},
		DiscoverProviderModels: func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return nil, errors.New("offline")
		},
	})
	m.input.SetValue("/model")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("expected opening the model picker to start discovery")
	}
	// A failed discovery leaves the static catalog list in place — no crash, no block.
	updated, _ = m.Update(modelPickerModelsDiscoveredMsg{providerID: "ollama-cloud", models: nil, err: errors.New("offline")})
	m = updated.(model)
	view := plainRender(t, m.pickerOverlay(100))
	assertNotContains(t, view, "Checking available models...")
	if m.picker == nil || len(m.picker.items) == 0 {
		t.Fatal("static catalog list should remain after a failed discovery")
	}
}

func TestModelPickerAppliesLiveDiscoveredModelID(t *testing.T) {
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		ProviderName: "ollama-cloud",
		ModelName:    "minimax-m3",
		Provider:     &fakeProvider{},
		ProviderProfile: config.ProviderProfile{
			Name:         "ollama-cloud",
			CatalogID:    "ollama-cloud",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKey:       "ollama-key",
			Model:        "minimax-m3",
		},
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			captured = profile
			return &fakeProvider{}, nil
		},
	})
	m.modelPickerLiveByProvider = map[string][]providermodeldiscovery.Model{"ollama-cloud": {{ID: "glm-5.1", Description: "GLM 5.1"}}}
	m.picker = m.newModelPicker()
	m.picker.selected = pickerIndex(m.picker.items, "glm-5.1")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if captured.Model != "glm-5.1" {
		t.Fatalf("captured model = %q, want glm-5.1", captured.Model)
	}
	if next.modelName != "glm-5.1" {
		t.Fatalf("active model = %q, want glm-5.1", next.modelName)
	}
}

func TestModelSwitchNormalizesDetectedOllamaCloudProfile(t *testing.T) {
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		ProviderName: "custom-openai-compatible",
		ModelName:    "minimax-m3",
		Provider:     &fakeProvider{},
		ProviderProfile: config.ProviderProfile{
			Name:         "custom-openai-compatible",
			CatalogID:    "custom-openai-compatible",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKeyEnv:    "OPENAI_API_KEY",
			Model:        "minimax-m3",
		},
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			captured = profile
			return &fakeProvider{}, nil
		},
	})
	m.modelPickerLiveByProvider = map[string][]providermodeldiscovery.Model{"ollama-cloud": {{ID: "glm-5.1", Description: "GLM 5.1"}}}
	m.input.SetValue("/model glm-5.1")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if captured.Name != "ollama-cloud" || captured.CatalogID != "ollama-cloud" {
		t.Fatalf("captured provider identity = %#v, want ollama-cloud", captured)
	}
	if captured.APIKeyEnv != "OLLAMA_API_KEY" {
		t.Fatalf("captured APIKeyEnv = %q, want OLLAMA_API_KEY", captured.APIKeyEnv)
	}
	if captured.Model != "glm-5.1" {
		t.Fatalf("captured model = %q, want glm-5.1", captured.Model)
	}
	if next.providerName != "ollama-cloud" {
		t.Fatalf("providerName = %q, want ollama-cloud", next.providerName)
	}
}

func TestModelPickerSearchFiltersModels(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "ollama-cloud",
		ModelName:    "minimax-m3",
		ProviderProfile: config.ProviderProfile{
			Name:         "ollama-cloud",
			CatalogID:    "ollama-cloud",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKeyEnv:    "OLLAMA_API_KEY",
			Model:        "minimax-m3",
		},
	})
	m.picker = m.newModelPicker()

	updated, _ := m.Update(testKeyText("qwen"))
	next := updated.(model)
	if next.picker.query != "qwen" {
		t.Fatalf("picker query = %q, want qwen", next.picker.query)
	}
	view := plainRender(t, next.pickerOverlay(100))
	assertContains(t, view, "search > qwen")
	assertContains(t, view, "Qwen")
	assertNotContains(t, view, "Minimax M3")
}

func TestModelPickerFavoriteShortcutTogglesSelectedModel(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero", "config.json")
	m := newModel(context.Background(), Options{
		UserConfigPath: configPath,
		ProviderName:   "ollama-cloud",
		ModelName:      "minimax-m3",
		ProviderProfile: config.ProviderProfile{
			Name:         "ollama-cloud",
			CatalogID:    "ollama-cloud",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://ollama.com/v1",
			APIKeyEnv:    "OLLAMA_API_KEY",
			Model:        "minimax-m3",
		},
	})
	m.picker = m.newModelPicker()
	if m.picker == nil {
		t.Fatal("expected model picker")
	}
	target := pickerIndex(m.picker.items, "qwen3-coder:480b")
	if target < 0 {
		t.Fatalf("expected qwen3-coder:480b in picker, got %#v", pickerValues(m.picker.items))
	}
	m.picker.selected = target

	updated, _ := m.Update(testKeyCtrl('f'))
	next := updated.(model)
	if !next.favoriteModels["qwen3-coder:480b"] {
		t.Fatalf("favorite map = %#v, want qwen3-coder:480b favorited", next.favoriteModels)
	}
	if next.picker.items[0].Group != "Favorites" || next.picker.items[0].Value != "qwen3-coder:480b" {
		t.Fatalf("first picker item = %#v, want favorite group row", next.picker.items[0])
	}
	persisted := readTUIConfigFixture(t, configPath)
	if len(persisted.Preferences.FavoriteModels) != 1 || persisted.Preferences.FavoriteModels[0] != "qwen3-coder:480b" {
		t.Fatalf("persisted FavoriteModels = %#v, want qwen3-coder:480b", persisted.Preferences.FavoriteModels)
	}

	updated, _ = next.Update(testKeyCtrl('f'))
	next = updated.(model)
	if next.favoriteModels["qwen3-coder:480b"] {
		t.Fatalf("favorite map = %#v, want qwen3-coder:480b unfavorited", next.favoriteModels)
	}
	if len(next.picker.items) > 0 && next.picker.items[0].Group == "Favorites" {
		t.Fatalf("favorites group should be gone after unfavorite, got first item %#v", next.picker.items[0])
	}
	persisted = readTUIConfigFixture(t, configPath)
	if len(persisted.Preferences.FavoriteModels) != 0 {
		t.Fatalf("persisted FavoriteModels = %#v, want empty after unfavorite", persisted.Preferences.FavoriteModels)
	}
}

func TestModelPickerLoadsFavoriteModelsFromOptions(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName:    "ollama-cloud",
		ModelName:       "minimax-m3",
		FavoriteModels:  []string{"qwen3-coder:480b"},
		ProviderProfile: config.ProviderProfile{Name: "ollama-cloud", CatalogID: "ollama-cloud", Model: "minimax-m3"},
	})
	picker := m.newModelPicker()
	if picker == nil {
		t.Fatal("expected model picker")
	}
	if picker.items[0].Group != "Favorites" || picker.items[0].Value != "qwen3-coder:480b" {
		t.Fatalf("first picker item = %#v, want persisted favorite first", picker.items[0])
	}
}

func TestModelPickerShowsRecentThenActiveProviderCatalog(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openrouter",
		ModelName:    "google/gemini-2.5-pro",
		ProviderProfile: config.ProviderProfile{
			Name:      "openrouter",
			CatalogID: "openrouter",
			Model:     "google/gemini-2.5-pro",
			APIKeyEnv: "OPENROUTER_API_KEY",
			Provider:  string(config.ProviderKindOpenAICompatible),
			BaseURL:   "https://openrouter.ai/api/v1",
			APIFormat: "chat-completions",
		},
	})

	picker := m.newModelPicker()
	if picker == nil {
		t.Fatal("expected a model picker")
	}
	if picker.items[0].Group != "Recent" {
		t.Fatalf("first picker group = %q, want Recent", picker.items[0].Group)
	}
	if picker.items[0].Value != "google/gemini-2.5-pro" {
		t.Fatalf("first picker value = %q, want active recent model", picker.items[0].Value)
	}
	if picker.items[1].Group != "OpenRouter" {
		t.Fatalf("second picker group = %q, want OpenRouter", picker.items[1].Group)
	}
	got := pickerValues(picker.items)
	if !contains(got, "anthropic/claude-sonnet-4.5") || !contains(got, "minimax/minimax-m2.1") {
		t.Fatalf("active provider catalog missing expected OpenRouter models: %#v", got)
	}
	if contains(got, "claude-haiku-4.5") {
		t.Fatalf("picker should not include unrelated global Anthropic registry model under OpenRouter: %#v", got)
	}
}

func TestModelPickerAppliesActiveProviderCatalogModelID(t *testing.T) {
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		ProviderName: "openrouter",
		ModelName:    "google/gemini-2.5-pro",
		Provider:     &fakeProvider{},
		ProviderProfile: config.ProviderProfile{
			Name:         "openrouter",
			CatalogID:    "openrouter",
			ProviderKind: config.ProviderKindOpenAICompatible,
			Model:        "google/gemini-2.5-pro",
			APIKeyEnv:    "OPENROUTER_API_KEY",
			BaseURL:      "https://openrouter.ai/api/v1",
			APIFormat:    "chat-completions",
		},
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			captured = profile
			return &fakeProvider{}, nil
		},
	})
	m.input.SetValue("/model openai/gpt-4.1")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /model to be handled without starting a run")
	}
	if captured.Model != "openai/gpt-4.1" {
		t.Fatalf("captured model = %q, want raw OpenRouter model ID", captured.Model)
	}
	if next.modelName != "openai/gpt-4.1" {
		t.Fatalf("active model = %q, want raw OpenRouter model ID", next.modelName)
	}
	if !transcriptContains(next.transcript, "openai/gpt-4.1 ·") {
		t.Fatalf("expected model switch status, got %#v", next.transcript)
	}
}

func TestModelCommandAcceptsManualModelForCustomProvider(t *testing.T) {
	var captured config.ProviderProfile
	m := newModel(context.Background(), Options{
		ProviderName: "custom-openai-compatible",
		ModelName:    "custom-model",
		Provider:     &fakeProvider{},
		ProviderProfile: config.ProviderProfile{
			Name:         "custom-openai-compatible",
			CatalogID:    "custom-openai-compatible",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://proxy.example.test/v1",
			APIKey:       "proxy-key",
			Model:        "custom-model",
		},
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			captured = profile
			return &fakeProvider{}, nil
		},
	})
	m.input.SetValue("/model qwen-custom-latest")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected /model to be handled without starting a run")
	}
	if captured.Model != "qwen-custom-latest" {
		t.Fatalf("captured model = %q, want manual custom model", captured.Model)
	}
	if next.modelName != "qwen-custom-latest" {
		t.Fatalf("active model = %q, want manual custom model", next.modelName)
	}
	if transcriptContains(next.transcript, "unknown Zero model") {
		t.Fatalf("manual custom model should not be rejected, got %#v", next.transcript)
	}
}

func TestModelPickerOpensAndCancels(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "claude-sonnet-4.5"})
	m.input.SetValue("/model")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd != nil {
		t.Fatal("opening the model picker should not start a run")
	}
	if m.picker == nil || m.picker.kind != pickerModel {
		t.Fatalf("expected an open model picker, got %#v", m.picker)
	}

	// Esc cancels the picker without touching the run or transcript.
	updated, _ = m.Update(testKey(tea.KeyEsc))
	m = updated.(model)
	if m.picker != nil {
		t.Fatal("Esc should close the picker")
	}
}

func TestModelPickerNavigatesAndChoosesAppliesHandler(t *testing.T) {
	next := &fakeProvider{}
	m := newModel(context.Background(), Options{
		ProviderName:    "anthropic",
		ModelName:       "claude-sonnet-4.5",
		Provider:        &fakeProvider{},
		ProviderProfile: anthropicTestProfile("claude-sonnet-4.5"),
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return next, nil
		},
		DiscoverProviderModels: func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return []providermodeldiscovery.Model{
				{ID: "claude-haiku-4.5", Description: "Claude Haiku 4.5"},
			}, nil
		},
	})
	m.input.SetValue("/model")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker == nil {
		t.Fatal("expected model picker open")
	}
	if cmd == nil {
		t.Fatal("expected opening the model picker to start discovery")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)

	// Point the picker at a concrete, different model in the same provider family
	// and choose it (cross-provider switches require a matching profile).
	target := -1
	for i, item := range m.picker.items {
		if item.Value == "claude-haiku-4.5" {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatal("expected claude-haiku-4.5 in the model picker")
	}
	m.picker.selected = target

	updated, _ = m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker != nil {
		t.Fatal("choosing should close the picker")
	}
	if m.modelName != "claude-haiku-4.5" {
		t.Fatalf("expected model switched to claude-haiku-4.5 via handler, got %q", m.modelName)
	}
	if !transcriptContains(m.transcript, "Model") {
		t.Fatal("choosing should append the model handler's status text")
	}
}

func TestEffortPickerOpensForSupportedModel(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "claude-sonnet-4.5"})
	m.input.SetValue("/effort")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker == nil || m.picker.kind != pickerEffort {
		t.Fatalf("expected an open effort picker, got %#v", m.picker)
	}
	// "auto" is always offered as the first option.
	if len(m.picker.items) == 0 || m.picker.items[0].Value != "auto" {
		t.Fatalf("expected auto as the first effort option, got %#v", m.picker.items)
	}

	// Choose the highlighted effort; the handler stores the preference.
	for i, item := range m.picker.items {
		if item.Value == "high" {
			m.picker.selected = i
		}
	}
	updated, _ = m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.reasoningEffort != "high" {
		t.Fatalf("expected effort applied via handler, got %q", m.reasoningEffort)
	}
}

func TestThemeCommandOpensNoPicker(t *testing.T) {
	// /theme keeps the existing shell-only message; no picker opens.
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/theme")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker != nil {
		t.Fatal("/theme should not open a picker")
	}
}

func TestPickersRefuseToOpenWhileRunPending(t *testing.T) {
	// A picker opened while a run is in flight would have its selection refused
	// after the run, so opening it at all is misleading. Each no-arg picker command
	// must no-op into a brief "while a run is in progress" message instead.
	cases := []struct {
		name    string
		command string
	}{
		{name: "model", command: "/model"},
		{name: "mode", command: "/mode"},
		{name: "effort", command: "/effort"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(context.Background(), Options{
				ModelName: "claude-sonnet-4.5",
			})
			m.pending = true
			m.input.SetValue(tc.command)

			updated, cmd := m.Update(testKey(tea.KeyEnter))
			next := updated.(model)
			if cmd != nil {
				t.Fatalf("%s while pending should not start a run", tc.command)
			}
			if next.picker != nil {
				t.Fatalf("%s should not open a picker while a run is in progress, got %#v", tc.command, next.picker)
			}
			if !transcriptContains(next.transcript, "while a run is in progress") {
				t.Fatalf("%s should explain it can't change settings while a run is in progress, got %q", tc.command, transcriptText(next.transcript))
			}
			if !next.pending {
				t.Fatalf("%s must not clear the in-flight run", tc.command)
			}
		})
	}
}

func TestPickerRenders(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "claude-sonnet-4.5"})
	m.width, m.height = 96, 30
	m.input.SetValue("/model")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if !strings.Contains(viewString(m.View()), "Choose a model") {
		t.Fatal("view should render the picker title")
	}
}

func TestPickerOverlayCapsVisibleRows(t *testing.T) {
	items := make([]pickerItem, 0, 20)
	for i := range 20 {
		items = append(items, pickerItem{Label: fmt.Sprintf("model-%02d", i), Value: fmt.Sprintf("model-%02d", i)})
	}
	m := newModel(context.Background(), Options{})
	m.picker = &commandPicker{
		kind:     pickerModel,
		title:    "Choose a model",
		items:    items,
		allItems: append([]pickerItem{}, items...),
		selected: 15,
	}

	got := plainRender(t, m.pickerOverlay(120))
	if !strings.Contains(got, "Choose a model") || !strings.Contains(got, "model-15") {
		t.Fatalf("picker overlay should render selected window, got %q", got)
	}
	if strings.Contains(got, "model-00") || strings.Contains(got, "model-09") {
		t.Fatalf("picker overlay should cap visible rows around selection, got %q", got)
	}
}

func pickerValues(items []pickerItem) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, item.Value)
	}
	return values
}

func TestModelPickerListsAllSavedProviders(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-5.1",
		ProviderProfile: config.ProviderProfile{Name: "openai", CatalogID: "openai", Model: "gpt-5.1"},
		SavedProviders: []config.ProviderProfile{
			{Name: "openai", CatalogID: "openai", Model: "gpt-5.1"},
			{Name: "xai", CatalogID: "xai", Model: "grok-4"},
		},
	})
	picker := m.newModelPicker()
	if picker == nil {
		t.Fatal("expected model picker")
	}
	// More than one provider section is listed (not just the active provider).
	if groups := pickerGroups(picker.items); len(groups) < 2 {
		t.Fatalf("expected multiple provider groups, got %#v", groups)
	}
	// Models from both saved providers are present and tagged with their owner so
	// selection can switch providers.
	owners := map[string]bool{}
	for _, item := range picker.items {
		if item.OwnerProvider != "" {
			owners[strings.ToLower(item.OwnerProvider)] = true
		}
	}
	for _, want := range []string{"openai", "xai"} {
		if !owners[want] {
			t.Fatalf("expected models owned by %q; owners=%v", want, owners)
		}
	}
}

func pickerGroups(items []pickerItem) []string {
	groups := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		if item.Group == "" || seen[item.Group] {
			continue
		}
		seen[item.Group] = true
		groups = append(groups, item.Group)
	}
	return groups
}

func pickerIndex(items []pickerItem, value string) int {
	for index, item := range items {
		if item.Value == value {
			return index
		}
	}
	return -1
}

func readTUIConfigFixture(t *testing.T, path string) config.FileConfig {
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

func TestEffortPickerOpensForModelWithoutEffortControls(t *testing.T) {
	// glm-5.1 is not in the hard-coded registry, so availableReasoningEfforts is
	// empty. /effort should still open a picker (offering auto only) instead of
	// rendering a static "Effort / available: none for active model" status card.
	m := newModel(context.Background(), Options{ModelName: "glm-5.1"})
	m.input.SetValue("/effort")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker == nil || m.picker.kind != pickerEffort {
		t.Fatalf("expected an open effort picker, got %#v", m.picker)
	}
	if len(m.picker.items) != 1 || m.picker.items[0].Value != "auto" {
		t.Fatalf("expected [auto] as the only effort option on an unsupported model, got %#v", m.picker.items)
	}
	if m.picker.title != "select reasoning effort" {
		t.Fatalf("picker title = %q, want %q", m.picker.title, "select reasoning effort")
	}
}

func TestEffortPickerAutoSelectionKeepsEffortUnset(t *testing.T) {
	// Picking "auto" on a model without effort controls clears any stale
	// preference and emits the success status text (handleEffortCommand("auto")).
	m := newModel(context.Background(), Options{ModelName: "glm-5.1"})
	m.reasoningEffort = modelregistry.ReasoningEffortHigh
	m.input.SetValue("/effort")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker == nil {
		t.Fatal("expected the effort picker to open")
	}

	updated, _ = m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker != nil {
		t.Fatal("enter should close the picker")
	}
	if m.reasoningEffort != "" {
		t.Fatalf("auto selection should clear reasoning effort, got %q", m.reasoningEffort)
	}
}
