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
}
