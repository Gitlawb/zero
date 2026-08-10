package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
