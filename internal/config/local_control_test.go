package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLocalControlFromUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
		"localControl": {
			"enabled": true,
			"browser": {"driver": "agent-browser"},
			"desktop": {"enabled": true},
			"terminal": {"enabled": false}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	resolved, err := Resolve(ResolveOptions{UserConfigPath: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !resolved.LocalControl.Enabled {
		t.Fatal("localControl.enabled = false, want true")
	}
	if !resolved.LocalControl.BrowserEnabled() {
		t.Fatal("BrowserEnabled = false, want true by default under localControl.enabled")
	}
	if !resolved.LocalControl.DesktopEnabled() {
		t.Fatal("DesktopEnabled = false, want explicit true")
	}
	if resolved.LocalControl.TerminalEnabled() {
		t.Fatal("TerminalEnabled = true, want explicit false")
	}
	if resolved.LocalControl.Browser.Driver != "agent-browser" {
		t.Fatalf("browser driver = %q, want agent-browser", resolved.LocalControl.Browser.Driver)
	}
}

func TestResolveLocalControlIgnoresProjectOptIn(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project.json")
	if err := os.WriteFile(projectPath, []byte(`{"localControl":{"enabled":true,"desktop":{"enabled":true}}}`), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	resolved, err := Resolve(ResolveOptions{ProjectConfigPath: projectPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.LocalControl.Enabled || resolved.LocalControl.DesktopEnabled() {
		t.Fatalf("project config enabled gated local control: %#v", resolved.LocalControl)
	}
	if !resolved.LocalControl.BrowserEnabled() || !resolved.LocalControl.TerminalEnabled() {
		t.Fatalf("browser/terminal should remain available by default: %#v", resolved.LocalControl)
	}
}

func TestLocalControlDefaultsBrowserAndTerminalOn(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !resolved.LocalControl.BrowserEnabled() {
		t.Fatal("BrowserEnabled = false, want default true")
	}
	if !resolved.LocalControl.TerminalEnabled() {
		t.Fatal("TerminalEnabled = false, want default true")
	}
	if resolved.LocalControl.DesktopEnabled() {
		t.Fatal("DesktopEnabled = true, want explicit opt-in")
	}
}

func TestLocalControlExplicitGlobalDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"localControl":{"enabled":false}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	resolved, err := Resolve(ResolveOptions{UserConfigPath: path, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.LocalControl.BrowserEnabled() || resolved.LocalControl.TerminalEnabled() || resolved.LocalControl.DesktopEnabled() {
		t.Fatalf("local control not disabled globally: %#v", resolved.LocalControl)
	}
}

func TestLocalControlExplicitFalseSurvivesConfigRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
		"activeProvider": "openai",
		"providers": [{"name": "openai", "provider_kind": "openai", "model": "gpt-4.1"}],
		"localControl": {
			"enabled": true,
			"browser": {"enabled": false},
			"terminal": {"enabled": false}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := SetFavoriteModels(path, []string{"gpt-4.1"}); err != nil {
		t.Fatalf("SetFavoriteModels returned error: %v", err)
	}
	persisted := readConfigFixture(t, path)
	if !persisted.LocalControl.enabledSet || !persisted.LocalControl.Enabled {
		t.Fatalf("localControl enabled not preserved: %#v", persisted.LocalControl)
	}
	if !persisted.LocalControl.Browser.enabledSet || persisted.LocalControl.Browser.Enabled {
		t.Fatalf("browser explicit false not preserved: %#v", persisted.LocalControl.Browser)
	}
	if !persisted.LocalControl.Terminal.enabledSet || persisted.LocalControl.Terminal.Enabled {
		t.Fatalf("terminal explicit false not preserved: %#v", persisted.LocalControl.Terminal)
	}
}

func TestEmptyLocalControlIsOmittedFromConfigJSON(t *testing.T) {
	data, err := json.Marshal(FileConfig{})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if strings.Contains(string(data), "localControl") {
		t.Fatalf("empty localControl should be omitted, got %s", string(data))
	}
}
