package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agentcli"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// fakeAgentCLIDeps builds an agentcli.Deps that reports `present` binaries on
// PATH, a not-found keychain (never leave this nil in a claude-related test —
// the real keychain leaks in on dev Macs), and ReadFile serving `credFiles`
// (path -> contents) for anything else not found.
func fakeAgentCLIDeps(present map[string]bool, credFiles map[string]string) agentcli.Deps {
	return agentcli.Deps{
		LookPath: func(name string) (string, error) {
			if present[name] {
				return "/usr/local/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Keychain: func(string) ([]byte, error) { return nil, errors.New("keychain: not found") },
		ReadFile: func(path string) ([]byte, error) {
			for suffix, contents := range credFiles {
				if strings.HasSuffix(path, suffix) {
					return []byte(contents), nil
				}
			}
			return nil, os.ErrNotExist
		},
	}
}

// TestProviderWizardCLIMethodOptionsFromDetections exercises the whole
// detect -> build-rows path with injected agentcli.Deps: claude is installed
// and logged in (selectable "Use Claude Code login" row), codex is installed
// but logged out (selectable, loggedIn=false), gemini is installed but has no
// reusable provider credentials (informational/disabled row).
func TestProviderWizardCLIMethodOptionsFromDetections(t *testing.T) {
	deps := fakeAgentCLIDeps(
		map[string]bool{"claude": true, "codex": true, "gemini": true},
		map[string]string{
			".claude/.credentials.json": `{"claudeAiOauth":{"accessToken":"tok","refreshToken":"r","expiresAt":9999999999999}}`,
			// codex's auth.json intentionally absent -> logged out.
			// gemini's oauth_creds.json intentionally absent -> irrelevant, it has no CatalogID anyway.
		},
	)
	detections := agentcli.Detect(deps)

	options := providerWizardCLIMethodOptions(detections)

	var claudeRow, codexRow, geminiRow *providerWizardMethodOption
	for index := range options {
		switch options[index].harness.ID {
		case "claude":
			claudeRow = &options[index]
		case "codex":
			codexRow = &options[index]
		case "gemini":
			geminiRow = &options[index]
		}
	}
	if claudeRow == nil || codexRow == nil || geminiRow == nil {
		t.Fatalf("expected claude, codex, and gemini rows, got %#v", options)
	}
	if claudeRow.kind != providerWizardMethodCLI || claudeRow.disabled || !claudeRow.loggedIn {
		t.Fatalf("claude row = %#v, want selectable + logged in", *claudeRow)
	}
	if !strings.Contains(claudeRow.label, "Claude Code") {
		t.Fatalf("claude row label = %q, want it to name Claude Code", claudeRow.label)
	}
	if codexRow.disabled || codexRow.loggedIn {
		t.Fatalf("codex row = %#v, want selectable + logged OUT", *codexRow)
	}
	if !geminiRow.disabled {
		t.Fatalf("gemini row = %#v, want disabled (no reusable provider credentials)", *geminiRow)
	}
	if geminiRow.loggedIn {
		t.Fatalf("gemini row = %#v, want loggedIn false (no oauth_creds.json in this fixture)", *geminiRow)
	}
	if !strings.Contains(geminiRow.subtitle, "not logged in") {
		t.Fatalf("gemini row subtitle = %q, want it to report login state even though it is disabled", geminiRow.subtitle)
	}
}

// TestProviderWizardCLIMethodOptionsReportsLoginStateForNoCatalogHarness locks
// in that a disabled (no-CatalogID) row still reflects the real probed login
// state instead of a generic message — a detected-and-logged-in sub-agent-only
// harness like Gemini CLI must say so, not just "no reusable API credentials".
func TestProviderWizardCLIMethodOptionsReportsLoginStateForNoCatalogHarness(t *testing.T) {
	deps := fakeAgentCLIDeps(
		map[string]bool{"gemini": true},
		map[string]string{".gemini/oauth_creds.json": `{"access_token":"tok"}`},
	)
	detections := agentcli.Detect(deps)

	options := providerWizardCLIMethodOptions(detections)

	if len(options) != 1 {
		t.Fatalf("options = %#v, want exactly one gemini row", options)
	}
	gemini := options[0]
	if !gemini.disabled {
		t.Fatalf("gemini row = %#v, want disabled (no reusable provider credentials)", gemini)
	}
	if !gemini.loggedIn {
		t.Fatalf("gemini row = %#v, want loggedIn true (oauth_creds.json present)", gemini)
	}
	if !strings.Contains(gemini.subtitle, "logged in") || strings.Contains(gemini.subtitle, "not logged in") {
		t.Fatalf("gemini row subtitle = %q, want it to say logged in", gemini.subtitle)
	}
}

// TestProviderWizardMethodOptionsKeepsAPIKeyLast locks in the ordering
// invariant several existing tests rely on (selectedMethod = len(options)-1
// selects "Paste an API key / browse providers"): CLI rows must be inserted
// between OAuth and the key option, never after it.
func TestProviderWizardMethodOptionsKeepsAPIKeyLast(t *testing.T) {
	deps := fakeAgentCLIDeps(map[string]bool{"claude": true}, map[string]string{
		".claude/.credentials.json": `{"claudeAiOauth":{"accessToken":"tok","refreshToken":"r"}}`,
	})
	options := providerWizardMethodOptions(agentcli.Detect(deps))
	if len(options) < 2 {
		t.Fatalf("expected at least [oauth-or-cli..., key], got %d options", len(options))
	}
	last := options[len(options)-1]
	if last.kind != providerWizardMethodKey {
		t.Fatalf("last option = %#v, want the API-key method", last)
	}
}

// TestProviderWizardAdvanceIntoCLIMethodSkipsProviderStep drives the wizard's
// key-handling exactly as a user would: land on Method, select a logged-in
// CLI row, press Enter. It must skip the provider chooser entirely and land on
// Model selection with cliHarness recorded.
func TestProviderWizardAdvanceIntoCLIMethodSkipsProviderStep(t *testing.T) {
	claudeHarness, ok := agentcli.Lookup("claude")
	if !ok {
		t.Fatal("test assumption broken: claude missing from the agentcli catalog")
	}
	detections := []agentcli.Detection{
		{Harness: claudeHarness, Path: "/usr/local/bin/claude", Login: agentcli.LoggedIn},
	}
	m := newModel(context.Background(), Options{
		DetectAgentCLIs: func(agentcli.Deps) []agentcli.Detection { return detections },
	})
	m.providerWizard = m.newProviderWizard()
	options := m.providerWizard.methodOptions()
	index := -1
	for i, option := range options {
		if option.kind == providerWizardMethodCLI && option.harness.ID == "claude" {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("expected a claude CLI row in %#v", options)
	}
	m.providerWizard.selectedMethod = index

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.providerWizard == nil {
		t.Fatal("expected the wizard to stay open")
	}
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("step = %v, want Model (CLI method skips the provider chooser)", next.providerWizard.step)
	}
	if next.providerWizard.cliHarness == nil || next.providerWizard.cliHarness.ID != "claude" {
		t.Fatalf("cliHarness = %#v, want claude", next.providerWizard.cliHarness)
	}
	if len(next.providerWizard.providers) != 1 || next.providerWizard.providers[0].ID != "anthropic" {
		t.Fatalf("providers = %#v, want exactly the anthropic descriptor", next.providerWizard.providers)
	}

	// Retreating from Model must go straight back to Method (not Provider,
	// which CLI mode never visits) and clear cliHarness.
	back, _ := next.Update(testKey(tea.KeyLeft))
	nextBack := back.(model)
	if nextBack.providerWizard.step != providerWizardStepMethod {
		t.Fatalf("retreat step = %v, want Method", nextBack.providerWizard.step)
	}
	if nextBack.providerWizard.cliHarness != nil {
		t.Fatal("expected cliHarness to be cleared after retreating to Method")
	}
}

// TestProviderWizardAdvanceIntoLoggedOutCLIShowsHint covers "choosing CLI when
// logged out shows the harness's login hint rather than failing later": the
// wizard must stay on the Method step and surface an actionable error instead
// of silently building a doomed provider.
func TestProviderWizardAdvanceIntoLoggedOutCLIShowsHint(t *testing.T) {
	codexHarness, ok := agentcli.Lookup("codex")
	if !ok {
		t.Fatal("test assumption broken: codex missing from the agentcli catalog")
	}
	detections := []agentcli.Detection{
		{Harness: codexHarness, Path: "/usr/local/bin/codex", Login: agentcli.LoggedOut},
	}
	m := newModel(context.Background(), Options{
		DetectAgentCLIs: func(agentcli.Deps) []agentcli.Detection { return detections },
	})
	m.providerWizard = m.newProviderWizard()
	options := m.providerWizard.methodOptions()
	index := -1
	for i, option := range options {
		if option.kind == providerWizardMethodCLI && option.harness.ID == "codex" {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("expected a codex CLI row in %#v", options)
	}
	m.providerWizard.selectedMethod = index

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.providerWizard == nil || next.providerWizard.step != providerWizardStepMethod {
		t.Fatalf("expected to stay on the Method step, got %#v", next.providerWizard)
	}
	if !strings.Contains(next.providerWizard.err, "codex login") {
		t.Fatalf("err = %q, want it to mention the `codex login` hint", next.providerWizard.err)
	}
}

// TestProviderWizardRetriesLoginAfterRejection covers the fix for stale login
// state: agentCLIDetections is captured once at wizard construction, so a
// user who logs in AFTER opening the wizard (exactly what the "not logged in"
// error tells them to do) must be able to retry in the same wizard session —
// not have to close and reopen it — because advance() reprobes on rejection.
func TestProviderWizardRetriesLoginAfterRejection(t *testing.T) {
	codexHarness, ok := agentcli.Lookup("codex")
	if !ok {
		t.Fatal("test assumption broken: codex missing from the agentcli catalog")
	}
	loggedIn := false // flips to true between the two detect() calls, like a real `codex login`
	calls := 0
	m := newModel(context.Background(), Options{
		DetectAgentCLIs: func(agentcli.Deps) []agentcli.Detection {
			calls++
			state := agentcli.LoggedOut
			if loggedIn {
				state = agentcli.LoggedIn
			}
			return []agentcli.Detection{{Harness: codexHarness, Path: "/usr/local/bin/codex", Login: state}}
		},
	})
	m.providerWizard = m.newProviderWizard()
	options := m.providerWizard.methodOptions()
	index := -1
	for i, option := range options {
		if option.kind == providerWizardMethodCLI && option.harness.ID == "codex" {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("expected a codex CLI row in %#v", options)
	}
	m.providerWizard.selectedMethod = index

	// First attempt: still logged out (matches TestProviderWizardAdvanceIntoLoggedOutCLIShowsHint).
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if next.providerWizard == nil || next.providerWizard.step != providerWizardStepMethod {
		t.Fatalf("expected to stay on the Method step after first attempt, got %#v", next.providerWizard)
	}
	if !strings.Contains(next.providerWizard.err, "codex login") {
		t.Fatalf("err = %q, want it to mention the `codex login` hint", next.providerWizard.err)
	}
	callsBeforeRetry := calls
	if callsBeforeRetry < 2 {
		t.Fatalf("calls = %d, want at least 2 (construction plus advance()'s reprobe-before-reject)", callsBeforeRetry)
	}

	// User runs `codex login` in another terminal; retry in the SAME wizard.
	loggedIn = true
	retried, _ := next.Update(testKey(tea.KeyEnter))
	after := retried.(model)
	if after.providerWizard == nil {
		t.Fatal("expected the wizard to stay open")
	}
	if after.providerWizard.err != "" {
		t.Fatalf("err = %q, want cleared after a successful retry", after.providerWizard.err)
	}
	if after.providerWizard.step != providerWizardStepModel {
		t.Fatalf("step = %v, want Model (retry should succeed and skip the provider chooser)", after.providerWizard.step)
	}
	if after.providerWizard.cliHarness == nil || after.providerWizard.cliHarness.ID != "codex" {
		t.Fatalf("cliHarness = %#v, want codex", after.providerWizard.cliHarness)
	}
	if calls <= callsBeforeRetry {
		t.Fatalf("calls = %d (was %d before retry), want a fresh reprobe on the second advance() too", calls, callsBeforeRetry)
	}
}

// TestProviderWizardCLIProfileHasNoAPIKeyOrEnv locks in the credential shape a
// CLI method selection must produce: AuthCLI set, APIKey/APIKeyEnv both empty
// — providerWizardProfile would otherwise populate APIKeyEnv for a
// RequiresAuth descriptor like Anthropic's, and providerWizardRuntimeProfile
// would then read an ambient env var, silently displacing the CLI login.
func TestProviderWizardCLIProfileHasNoAPIKeyOrEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ambient-should-be-ignored")
	claudeHarness, ok := agentcli.Lookup("claude")
	if !ok {
		t.Fatal("test assumption broken: claude missing from the agentcli catalog")
	}
	descriptor, ok := providercatalog.Get("anthropic")
	if !ok {
		t.Fatal("test assumption broken: anthropic missing from the provider catalog")
	}

	profile := providerWizardCLIProfile(claudeHarness, descriptor, "claude-cli-test-model")

	if profile.AuthCLI != "claude" {
		t.Fatalf("AuthCLI = %q, want claude", profile.AuthCLI)
	}
	if profile.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", profile.APIKey)
	}
	if profile.APIKeyEnv != "" {
		t.Fatalf("APIKeyEnv = %q, want empty (must not fall back to an ambient env var)", profile.APIKeyEnv)
	}
	if profile.BaseURL != descriptor.DefaultBaseURL {
		t.Fatalf("BaseURL = %q, want the descriptor's default %q (provider construction/reload should not depend on later default hydration)", profile.BaseURL, descriptor.DefaultBaseURL)
	}
	runtimeProfile := providerWizardRuntimeProfile(profile)
	if runtimeProfile.APIKey != "" {
		t.Fatalf("runtime APIKey = %q, want empty even with ANTHROPIC_API_KEY set in the environment", runtimeProfile.APIKey)
	}
}

// TestApplyProviderWizardCLIModePersistsAuthCLI drives applyProviderWizard for
// a CLI-mode wizard state (mirroring TestApplyProviderWizardExportsActiveProviderEnv's
// harness) and checks the committed/persisted profile carries AuthCLI and no key.
func TestApplyProviderWizardCLIModePersistsAuthCLI(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.userConfigPath = filepath.Join(t.TempDir(), "config.json")
	m.newProvider = func(config.ProviderProfile) (zeroruntime.Provider, error) {
		return &fakeProvider{}, nil
	}
	claudeHarness, ok := agentcli.Lookup("claude")
	if !ok {
		t.Fatal("test assumption broken: claude missing from the agentcli catalog")
	}
	m.providerWizard = &providerWizardState{
		step:       providerWizardStepModel,
		cliHarness: &claudeHarness,
		providers: []providercatalog.Descriptor{{
			ID:           "anthropic",
			Name:         "Anthropic",
			Transport:    providercatalog.TransportAnthropic,
			DefaultModel: "claude-sonnet-4.5",
			AuthEnvVars:  []string{"ANTHROPIC_API_KEY"},
			RequiresAuth: true,
		}},
		models: []providerWizardModel{{ID: "claude-sonnet-4.5", Description: "Claude Sonnet 4.5"}},
	}

	updated, _ := m.applyProviderWizard()
	next := updated

	if next.providerWizard != nil {
		t.Fatalf("expected the wizard to close on successful apply, got %#v", next.providerWizard)
	}
	if next.providerProfile.AuthCLI != "claude" {
		t.Fatalf("committed profile AuthCLI = %q, want claude", next.providerProfile.AuthCLI)
	}
	if next.providerProfile.APIKey != "" || next.providerProfile.APIKeyEnv != "" {
		t.Fatalf("committed profile carries a key (APIKey=%q APIKeyEnv=%q), want neither for a CLI-authed profile",
			next.providerProfile.APIKey, next.providerProfile.APIKeyEnv)
	}

	// The persisted config must round-trip AuthCLI too — this is the on-disk
	// artifact the provider factory reads on the next launch.
	raw, err := os.ReadFile(m.userConfigPath)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(raw), `"authCLI": "claude"`) {
		t.Fatalf("persisted config = %s, want it to contain \"authCLI\": \"claude\"", raw)
	}
}
