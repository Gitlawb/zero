package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// managerTestModel builds a model with two saved providers, a seeded config
// file, and a fake provider factory, then opens /provider on the manager list.
func managerTestModel(t *testing.T) model {
	t.Helper()
	// Isolate every credential surface the manager touches: XDG redirects the
	// default config dir (oauth token store, default credential store location)
	// and the file backend keeps the OS keychain out of tests entirely.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", filepath.Join(home, "oauth-tokens.json"))
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	configPath := filepath.Join(t.TempDir(), "config.json")
	seed := config.FileConfig{
		ActiveProvider: "opengateway",
		Providers: []config.ProviderProfile{
			{Name: "opengateway", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "https://gateway.example.com/v1", APIKey: "sk-gw", Model: "mimo-v2.5-pro", Description: "Main gateway"},
			{Name: "backup", ProviderKind: config.ProviderKindOpenAICompatible, BaseURL: "https://backup.example.com/v1", APIKey: "sk-backup", Model: "backup-model"},
		},
	}
	data, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatalf("encode seed config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("write seed config: %v", err)
	}
	m := newModel(context.Background(), Options{
		ProviderName:    "opengateway",
		ModelName:       "mimo-v2.5-pro",
		Provider:        &fakeProvider{},
		ProviderProfile: seed.Providers[0],
		SavedProviders:  seed.Providers,
		UserConfigPath:  configPath,
		NewProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return &fakeProvider{}, nil
		},
	})
	m.width = 120
	m.height = 40
	next, _ := m.openProviderManager()
	return next
}

func managerKey(t *testing.T, m model, msg tea.KeyMsg) model {
	t.Helper()
	next, _ := m.handleProviderWizardKey(msg)
	return next
}

func TestOpenProviderManagerListsSavedProviders(t *testing.T) {
	m := managerTestModel(t)
	if m.providerWizard == nil || m.providerWizard.step != providerWizardStepManage {
		t.Fatalf("expected the manager list to open, got %+v", m.providerWizard)
	}
	if len(m.providerWizard.manageRows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(m.providerWizard.manageRows))
	}
	rendered := m.providerWizard.render(m.width)
	for _, want := range []string{"opengateway", "backup", "active", "Enter activate"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("manager render missing %q:\n%s", want, rendered)
		}
	}
}

func TestOpenProviderManagerFallsBackToWizardWhenEmpty(t *testing.T) {
	m := newModel(context.Background(), Options{})
	next, _ := m.openProviderManager()
	if next.providerWizard == nil || next.providerWizard.step != providerWizardStepMethod {
		t.Fatalf("no saved providers should open the add wizard, got %+v", next.providerWizard)
	}
}

func TestProviderManagerEnterActivatesSelection(t *testing.T) {
	m := managerTestModel(t)
	m = managerKey(t, m, testKey(tea.KeyDown)) // move to "backup"
	next, _ := m.handleProviderWizardKey(testKey(tea.KeyEnter))
	if next.providerWizard != nil {
		t.Fatalf("manager should close after a successful switch")
	}
	if next.providerName != "backup" || next.modelName != "backup-model" {
		t.Fatalf("switch did not commit: provider=%q model=%q", next.providerName, next.modelName)
	}
	persisted := readManagerConfig(t, next.userConfigPath)
	if persisted.ActiveProvider != "backup" {
		t.Fatalf("activeProvider not persisted, got %q", persisted.ActiveProvider)
	}
}

func TestProviderManagerActivateBlockedWhilePending(t *testing.T) {
	m := managerTestModel(t)
	m.pending = true
	next, _ := m.handleProviderWizardKey(testKey(tea.KeyEnter))
	if next.providerWizard == nil || next.providerWizard.manageStatus == "" {
		t.Fatalf("a pending run must keep the manager open with a status, got %+v", next.providerWizard)
	}
	if next.providerName != "opengateway" {
		t.Fatalf("provider must not switch while pending, got %q", next.providerName)
	}
}

func TestProviderManagerDeleteConfirmsAndRemoves(t *testing.T) {
	m := managerTestModel(t)
	m = managerKey(t, m, testKey(tea.KeyDown)) // select "backup"
	m = managerKey(t, m, testKeyText("d"))
	if !m.providerWizard.manageDeleting {
		t.Fatalf("d must arm the inline delete confirm")
	}
	// Esc cancels the confirm without closing the manager.
	m = managerKey(t, m, testKey(tea.KeyEsc))
	if m.providerWizard == nil || m.providerWizard.manageDeleting {
		t.Fatalf("Esc must cancel the confirm, keeping the manager open")
	}
	if got := readManagerConfig(t, m.userConfigPath); len(got.Providers) != 2 {
		t.Fatalf("cancelled delete must not touch config, got %d providers", len(got.Providers))
	}

	m = managerKey(t, m, testKeyText("d"))
	next, _ := m.handleProviderWizardKey(testKeyText("y"))
	if next.providerWizard == nil {
		t.Fatalf("manager should stay open while providers remain")
	}
	if len(next.savedProviders) != 1 || next.savedProviders[0].Name != "opengateway" {
		t.Fatalf("savedProviders not updated: %+v", next.savedProviders)
	}
	persisted := readManagerConfig(t, next.userConfigPath)
	if len(persisted.Providers) != 1 || persisted.Providers[0].Name != "opengateway" {
		t.Fatalf("config not updated: %+v", persisted.Providers)
	}
	if persisted.ActiveProvider != "opengateway" {
		t.Fatalf("active must be untouched when a non-active profile is deleted, got %q", persisted.ActiveProvider)
	}
	if !strings.Contains(next.providerWizard.manageStatus, "Deleted backup") {
		t.Fatalf("expected delete status, got %q", next.providerWizard.manageStatus)
	}
}

func TestProviderManagerEditModelPersists(t *testing.T) {
	m := managerTestModel(t)
	m = managerKey(t, m, testKeyText("e"))
	if m.providerWizard.step != providerWizardStepEditMenu {
		t.Fatalf("e must open the field editor, got step %v", m.providerWizard.step)
	}
	// Field order: Name, Endpoint, Model, API key, Description, Save.
	m = managerKey(t, m, testKey(tea.KeyDown))
	m = managerKey(t, m, testKey(tea.KeyDown))
	m = managerKey(t, m, testKey(tea.KeyEnter)) // edit Model
	if m.providerWizard.step != providerWizardStepEditValue || m.providerWizard.editField != providerEditFieldModel {
		t.Fatalf("expected model value editor, got step %v field %v", m.providerWizard.step, m.providerWizard.editField)
	}
	m.providerWizard.editBuffer = "mimo-v3"
	m = managerKey(t, m, testKey(tea.KeyEnter)) // commit field
	if m.providerWizard.step != providerWizardStepEditMenu {
		t.Fatalf("commit should return to the field menu")
	}
	// Move to Save (cursor still on Model=index 2; Save is index 5).
	m = managerKey(t, m, testKey(tea.KeyDown))
	m = managerKey(t, m, testKey(tea.KeyDown))
	m = managerKey(t, m, testKey(tea.KeyDown))
	next, _ := m.handleProviderWizardKey(testKey(tea.KeyEnter))
	if next.providerWizard == nil || next.providerWizard.step != providerWizardStepManage {
		t.Fatalf("save should land back on the manager list")
	}
	persisted := readManagerConfig(t, next.userConfigPath)
	if persisted.Providers[0].Model != "mimo-v3" {
		t.Fatalf("edited model not persisted: %+v", persisted.Providers[0])
	}
	if persisted.Providers[0].APIKey != "sk-gw" {
		t.Fatalf("unrelated credential must survive the edit: %+v", persisted.Providers[0])
	}
}

func TestProviderManagerRenameFollowsLiveSession(t *testing.T) {
	m := managerTestModel(t)
	m = managerKey(t, m, testKeyText("e"))
	m = managerKey(t, m, testKey(tea.KeyEnter)) // edit Name (cursor 0)
	m.providerWizard.editBuffer = "gateway-main"
	m = managerKey(t, m, testKey(tea.KeyEnter))
	// Save is 5 rows below Name.
	for range 5 {
		m = managerKey(t, m, testKey(tea.KeyDown))
	}
	next, _ := m.handleProviderWizardKey(testKey(tea.KeyEnter))
	if next.providerWizard == nil || next.providerWizard.step != providerWizardStepManage {
		t.Fatalf("save should return to the list, got %+v", next.providerWizard)
	}
	if next.providerName != "gateway-main" {
		t.Fatalf("live session name must follow the rename, got %q", next.providerName)
	}
	persisted := readManagerConfig(t, next.userConfigPath)
	if persisted.ActiveProvider != "gateway-main" {
		t.Fatalf("activeProvider must follow the rename, got %q", persisted.ActiveProvider)
	}
	names := []string{}
	for _, profile := range persisted.Providers {
		names = append(names, profile.Name)
	}
	if len(persisted.Providers) != 2 || persisted.Providers[0].Name != "gateway-main" {
		t.Fatalf("rename not persisted, providers: %v", names)
	}
}

func TestProviderManagerEscWalksBackThenCloses(t *testing.T) {
	m := managerTestModel(t)
	m = managerKey(t, m, testKeyText("e"))
	m = managerKey(t, m, testKey(tea.KeyEnter)) // into Name editor
	m = managerKey(t, m, testKey(tea.KeyEsc))
	if m.providerWizard.step != providerWizardStepEditMenu {
		t.Fatalf("Esc from a field editor must return to the field menu")
	}
	m = managerKey(t, m, testKey(tea.KeyEsc))
	if m.providerWizard.step != providerWizardStepManage {
		t.Fatalf("Esc from the field menu must return to the list")
	}
	m = managerKey(t, m, testKey(tea.KeyEsc))
	if m.providerWizard != nil {
		t.Fatalf("Esc from the list must close the manager")
	}
}

func TestProviderManagerAddReturnsToListOnEsc(t *testing.T) {
	m := managerTestModel(t)
	m = managerKey(t, m, testKeyText("a"))
	if m.providerWizard.step != providerWizardStepMethod {
		t.Fatalf("a must open the add wizard's method step")
	}
	m = managerKey(t, m, testKey(tea.KeyEsc))
	if m.providerWizard == nil || m.providerWizard.step != providerWizardStepManage {
		t.Fatalf("Esc from a manager-opened add flow must return to the list")
	}
}

func TestProviderManagerCredsMsgFillsRowsAndIgnoresStale(t *testing.T) {
	m := managerTestModel(t)
	gen := m.providerWizard.manageCredGen
	next, _ := m.applyProviderManagerCreds(providerManagerCredsMsg{gen: gen, creds: map[string]string{
		"opengateway": "key set",
		"backup":      "no credential",
	}})
	if next.providerWizard.manageRows[0].cred != "key set" || next.providerWizard.manageRows[1].cred != "no credential" {
		t.Fatalf("creds not applied: %+v", next.providerWizard.manageRows)
	}
	// A stale generation must not clobber fresh rows.
	next, _ = next.applyProviderManagerCreds(providerManagerCredsMsg{gen: gen - 1, creds: map[string]string{
		"opengateway": "stale",
	}})
	if next.providerWizard.manageRows[0].cred != "key set" {
		t.Fatalf("stale creds applied: %+v", next.providerWizard.manageRows[0])
	}
}

func readManagerConfig(t *testing.T, path string) config.FileConfig {
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
