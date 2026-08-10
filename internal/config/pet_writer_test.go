package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetPetPreservesUnrelatedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := FileConfig{
		ActiveProvider: "test",
		Providers:      []ProviderProfile{{Name: "test", Model: "example"}},
		Preferences:    PreferencesConfig{Theme: "dracula"},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	updated, err := SetPet(path, "boba")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Preferences.Pet != "boba" || updated.Preferences.Theme != "dracula" || updated.ActiveProvider != "test" || len(updated.Providers) != 1 {
		t.Fatalf("SetPet lost config fields: %#v", updated)
	}
	reloadedData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded FileConfig
	if err := json.Unmarshal(reloadedData, &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.Preferences.Pet != "boba" || reloaded.Preferences.Theme != "dracula" || reloaded.ActiveProvider != "test" || len(reloaded.Providers) != 1 {
		t.Fatalf("persisted config lost fields: %#v", reloaded)
	}
}

func TestSetPetRejectsInvalidInputs(t *testing.T) {
	if _, err := SetPet("  ", "boba"); err == nil {
		t.Fatal("SetPet accepted a blank config path")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"preferences":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPet(path, "boba"); err == nil || !strings.Contains(err.Error(), "invalid config JSON") {
		t.Fatalf("SetPet malformed JSON error = %v", err)
	}
}

func TestSetPetCreatesNestedConfigAndClearsEmptyPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "zero", "config.json")
	if _, err := SetPet(path, " boba "); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if got := string(root["preferences"]); !strings.Contains(got, `"pet":"boba"`) && !strings.Contains(got, `"pet": "boba"`) {
		t.Fatalf("persisted preferences = %s", got)
	}
	root["future"] = json.RawMessage(`{"enabled":true}`)
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPet(path, " "); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root = nil
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["preferences"]; ok {
		t.Fatalf("empty preferences were retained: %s", data)
	}
	if !strings.Contains(string(root["future"]), `"enabled": true`) && !strings.Contains(string(root["future"]), `"enabled":true`) {
		t.Fatalf("unrelated future member was lost: %s", data)
	}
}

func TestSetPetPreservesUnknownJSONMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{
  "activeProvider": "test",
  "futureTopLevel": {"enabled": true},
  "preferences": {
    "theme": "dracula",
    "futurePreference": {"mode": "extended"}
  }
}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPet(path, "boba"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	var futureTopLevel struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(root["futureTopLevel"], &futureTopLevel); err != nil {
		t.Fatal(err)
	}
	if !futureTopLevel.Enabled {
		t.Fatalf("unknown top-level member = %s", root["futureTopLevel"])
	}
	var preferences map[string]json.RawMessage
	if err := json.Unmarshal(root["preferences"], &preferences); err != nil {
		t.Fatal(err)
	}
	var futurePreference struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(preferences["futurePreference"], &futurePreference); err != nil {
		t.Fatal(err)
	}
	if futurePreference.Mode != "extended" {
		t.Fatalf("unknown preference member = %s", preferences["futurePreference"])
	}
	if got := string(preferences["pet"]); got != `"boba"` {
		t.Fatalf("pet preference = %s, want boba", got)
	}
}
