package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

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
	if picker.items[1].Group != "OpenRouter catalog" {
		t.Fatalf("second picker group = %q, want OpenRouter catalog", picker.items[1].Group)
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	if !transcriptContains(next.transcript, "model: openai/gpt-4.1") {
		t.Fatalf("expected model switch status, got %#v", next.transcript)
	}
}

func TestModelPickerOpensAndCancels(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "claude-sonnet-4.5"})
	m.input.SetValue("/model")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("opening the model picker should not start a run")
	}
	if m.picker == nil || m.picker.kind != pickerModel {
		t.Fatalf("expected an open model picker, got %#v", m.picker)
	}

	// Esc cancels the picker without touching the run or transcript.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
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
	})
	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.picker == nil {
		t.Fatal("expected model picker open")
	}

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

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.reasoningEffort != "high" {
		t.Fatalf("expected effort applied via handler, got %q", m.reasoningEffort)
	}
}

func TestThemeCommandOpensNoPicker(t *testing.T) {
	// /theme keeps the existing shell-only message; no picker opens.
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/theme")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if !strings.Contains(m.View(), "select model") {
		t.Fatal("view should render the picker title")
	}
}

func pickerValues(items []pickerItem) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, item.Value)
	}
	return values
}
