package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agentcli"
	"github.com/Gitlawb/zero/internal/config"
)

// setupModelWithProviders builds a first-run setup model, injects the given
// agent-CLI detections directly (so the test never touches a real PATH or
// keychain), and advances Welcome -> Method.
func setupModelWithProviders(t *testing.T, providers []SetupProviderOption, detections []agentcli.Detection) model {
	t.Helper()
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible:   true,
			Providers: providers,
		},
		DetectAgentCLIs: func(agentcli.Deps) []agentcli.Detection { return detections },
	})
	m.width = 100
	m.height = 30
	return pressSetupContinueOnce(m) // Welcome -> Method
}

// TestSetupMethodChooserCLIPathSkipsProviderStep drives first-run setup
// through the Method chooser exactly as a user would: land on Method, select
// a logged-in CLI row, press Enter. It must skip the provider chooser AND the
// endpoint/name/credentials stages and land on Model with cliHarness set.
func TestSetupMethodChooserCLIPathSkipsProviderStep(t *testing.T) {
	claudeHarness, ok := agentcli.Lookup("claude")
	if !ok {
		t.Fatal("test assumption broken: claude missing from the agentcli catalog")
	}
	m := setupModelWithProviders(t,
		[]SetupProviderOption{
			{ID: "anthropic", Name: "Anthropic", DefaultModel: "claude-sonnet-4.5", EnvVar: "ANTHROPIC_API_KEY", RequiresAuth: true},
			{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
		},
		[]agentcli.Detection{{Harness: claudeHarness, Path: "/usr/local/bin/claude", Login: agentcli.LoggedIn}},
	)
	if m.setup.stage != setupStageMethod {
		t.Fatalf("stage = %v, want method chooser", m.setup.stage)
	}

	options := m.setupMethodOptions()
	index := -1
	for i, option := range options {
		if option.kind == providerWizardMethodCLI && option.harness.ID == "claude" {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("expected a claude CLI row in %#v", options)
	}
	m.setup.selectedMethod = index

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.setup.stage != setupStageModel {
		t.Fatalf("stage = %v, want Model (CLI method skips provider/endpoint/name/credentials)", next.setup.stage)
	}
	if next.setup.cliHarness == nil || next.setup.cliHarness.ID != "claude" {
		t.Fatalf("cliHarness = %#v, want claude", next.setup.cliHarness)
	}
	if len(next.setup.providers) != 1 || next.setup.providers[0].ID != "anthropic" {
		t.Fatalf("providers = %#v, want exactly the anthropic option", next.setup.providers)
	}

	// Retreating from Model must go straight back to Method and clear cliHarness.
	back, _ := next.Update(testKey(tea.KeyLeft))
	nextBack := back.(model)
	if nextBack.setup.stage != setupStageMethod {
		t.Fatalf("retreat stage = %v, want Method", nextBack.setup.stage)
	}
	if nextBack.setup.cliHarness != nil {
		t.Fatal("expected cliHarness to be cleared after retreating to Method")
	}
}

// TestSetupMethodChooserLoggedOutCLIShowsHint mirrors the equivalent
// /provider-wizard test: choosing a not-logged-in CLI row must not advance
// the stage, and must surface the harness's login hint.
func TestSetupMethodChooserLoggedOutCLIShowsHint(t *testing.T) {
	codexHarness, ok := agentcli.Lookup("codex")
	if !ok {
		t.Fatal("test assumption broken: codex missing from the agentcli catalog")
	}
	m := setupModelWithProviders(t,
		[]SetupProviderOption{{ID: "chatgpt", Name: "ChatGPT", DefaultModel: "gpt-5.5"}},
		[]agentcli.Detection{{Harness: codexHarness, Path: "/usr/local/bin/codex", Login: agentcli.LoggedOut}},
	)
	options := m.setupMethodOptions()
	index := -1
	for i, option := range options {
		if option.kind == providerWizardMethodCLI && option.harness.ID == "codex" {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("expected a codex CLI row in %#v", options)
	}
	m.setup.selectedMethod = index

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.setup.stage != setupStageMethod {
		t.Fatalf("stage = %v, want to stay on Method", next.setup.stage)
	}
	if !strings.Contains(next.setup.err, "codex login") {
		t.Fatalf("err = %q, want it to mention the `codex login` hint", next.setup.err)
	}
}

// TestSetupMethodOptionsFiltersCLIRowsNotInProviderList mirrors the existing
// OAuth-filtering behavior (TestSetupMethodOptionsDropsOAuthWithoutOAuthProviders):
// a selectable CLI row for a harness whose catalog provider isn't offered by
// this setup run must be dropped, since selecting it would commit to a
// provider outside the allowed list.
func TestSetupMethodOptionsFiltersCLIRowsNotInProviderList(t *testing.T) {
	claudeHarness, ok := agentcli.Lookup("claude")
	if !ok {
		t.Fatal("test assumption broken: claude missing from the agentcli catalog")
	}
	detections := []agentcli.Detection{
		{Harness: claudeHarness, Path: "/usr/local/bin/claude", Login: agentcli.LoggedIn},
	}
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			// No "anthropic" entry offered by this setup run.
			Providers: []SetupProviderOption{{ID: "openai", Name: "OpenAI", EnvVar: "OPENAI_API_KEY", RequiresAuth: true}},
		},
		DetectAgentCLIs: func(agentcli.Deps) []agentcli.Detection { return detections },
	})
	for _, option := range m.setupMethodOptions() {
		if option.kind == providerWizardMethodCLI && option.harness.ID == "claude" {
			t.Fatalf("claude CLI row must be filtered out when anthropic isn't in this setup's provider list: %#v", option)
		}
	}
}

// TestCompleteSetupCLIModePassesAuthCLI drives completeSetup for a CLI-mode
// setup and checks the SetupSelection handed to Save.
func TestCompleteSetupCLIModePassesAuthCLI(t *testing.T) {
	claudeHarness, ok := agentcli.Lookup("claude")
	if !ok {
		t.Fatal("test assumption broken: claude missing from the agentcli catalog")
	}
	var gotSelection SetupSelection
	m := newModel(context.Background(), Options{Setup: SetupOptions{
		Visible: true,
		Providers: []SetupProviderOption{
			{ID: "anthropic", Name: "Anthropic", DefaultModel: "claude-sonnet-4.5", EnvVar: "ANTHROPIC_API_KEY", RequiresAuth: true},
		},
		Save: func(selection SetupSelection) (SetupResult, error) {
			gotSelection = selection
			return SetupResult{
				Provider: config.ProviderProfile{
					Name:      selection.Name,
					CatalogID: selection.CatalogID,
					Model:     selection.Model,
					AuthCLI:   selection.AuthCLI,
				},
			}, nil
		},
	}})
	m.width = 100
	m.height = 30
	m.setup.cliHarness = &claudeHarness
	m.setup.providers = []SetupProviderOption{{ID: "anthropic", Name: "Anthropic", DefaultModel: "claude-sonnet-4.5"}}
	m.setup.stage = setupStageReady

	updated, _ := m.completeSetup()
	next := updated.(model)

	if gotSelection.AuthCLI != "claude" {
		t.Fatalf("SetupSelection.AuthCLI = %q, want claude", gotSelection.AuthCLI)
	}
	if gotSelection.APIKey != "" {
		t.Fatalf("SetupSelection.APIKey = %q, want empty for a CLI-authed profile", gotSelection.APIKey)
	}
	if next.providerProfile.AuthCLI != "claude" {
		t.Fatalf("committed profile AuthCLI = %q, want claude", next.providerProfile.AuthCLI)
	}
}
