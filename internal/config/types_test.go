package config

import (
	"encoding/json"
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
