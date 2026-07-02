package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolsConfigJSONRoundTrip(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{"tools":{"deferThreshold":25}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if cfg.Tools.DeferThreshold != 25 {
		t.Fatalf("Tools.DeferThreshold = %d, want 25", cfg.Tools.DeferThreshold)
	}

	encoded, err := json.Marshal(ToolsConfig{DeferThreshold: 7})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(encoded) != `{"deferThreshold":7}` {
		t.Fatalf("Marshal() = %s, want {\"deferThreshold\":7}", encoded)
	}

	// omitempty: a zero value must not emit the field.
	emptyEncoded, err := json.Marshal(ToolsConfig{})
	if err != nil {
		t.Fatalf("Marshal(empty) error = %v", err)
	}
	if string(emptyEncoded) != `{}` {
		t.Fatalf("Marshal(empty) = %s, want {}", emptyEncoded)
	}
}

func TestDiscoveredModelJSONRoundTrip(t *testing.T) {
	models := []DiscoveredModel{
		{ID: "glm-5.1"},
		{ID: "glm-5.2", APIModel: "glm-5.2-api"},
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded []DiscoveredModel
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("len = %d, want 2", len(decoded))
	}
	if decoded[0].ID != "glm-5.1" || decoded[0].APIModel != "" {
		t.Fatalf("decoded[0] = %#v, want {ID: glm-5.1}", decoded[0])
	}
	if decoded[1].ID != "glm-5.2" || decoded[1].APIModel != "glm-5.2-api" {
		t.Fatalf("decoded[1] = %#v, want {ID: glm-5.2, APIModel: glm-5.2-api}", decoded[1])
	}
}

func TestProviderProfileModelsRoundTripsThroughUnmarshalJSON(t *testing.T) {
	// Verify that the custom UnmarshalJSON for ProviderProfile handles "models".
	input := `{"name":"test","models":[{"id":"glm-5.1"},{"id":"glm-5.2","apiModel":"glm-5.2-api"}]}`
	var profile ProviderProfile
	if err := json.Unmarshal([]byte(input), &profile); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(profile.Models) != 2 {
		t.Fatalf("len(Models) = %d, want 2", len(profile.Models))
	}
	if profile.Models[0].ID != "glm-5.1" || profile.Models[0].APIModel != "" {
		t.Fatalf("Models[0] = %#v", profile.Models[0])
	}
	if profile.Models[1].ID != "glm-5.2" || profile.Models[1].APIModel != "glm-5.2-api" {
		t.Fatalf("Models[1] = %#v", profile.Models[1])
	}

	// Marshal back and verify the "models" key is present in the JSON.
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"models"`) {
		t.Fatalf("Marshal() output does not contain 'models' key:\n%s", encoded)
	}

	// An empty Models list must not emit the field (omitempty).
	var empty ProviderProfile
	emptyEncoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal(empty) error = %v", err)
	}
	if strings.Contains(string(emptyEncoded), `"models"`) {
		t.Fatalf("Marshal(empty) should omit models field:\n%s", emptyEncoded)
	}
}

func TestToolsConfigPresentOnOverridesAndResolved(t *testing.T) {
	// Compile-time guard that Overrides and ResolvedConfig carry the field too.
	overrides := Overrides{Tools: ToolsConfig{DeferThreshold: 3}}
	resolved := ResolvedConfig{Tools: ToolsConfig{DeferThreshold: 4}}
	if overrides.Tools.DeferThreshold != 3 {
		t.Fatalf("Overrides.Tools.DeferThreshold = %d, want 3", overrides.Tools.DeferThreshold)
	}
	if resolved.Tools.DeferThreshold != 4 {
		t.Fatalf("ResolvedConfig.Tools.DeferThreshold = %d, want 4", resolved.Tools.DeferThreshold)
	}
}
