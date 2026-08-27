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

func TestSessionsConfigJSONRoundTrip(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{"sessions":{"retentionDays":30,"maxCount":100}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if cfg.Sessions.RetentionDays != 30 || cfg.Sessions.MaxCount != 100 {
		t.Fatalf("Sessions = %#v, want retentionDays=30 maxCount=100", cfg.Sessions)
	}

	var snake FileConfig
	if err := json.Unmarshal([]byte(`{"sessions":{"retention_days":14,"max_count":20}}`), &snake); err != nil {
		t.Fatalf("Unmarshal(snake) error = %v", err)
	}
	if snake.Sessions.RetentionDays != 14 || snake.Sessions.MaxCount != 20 {
		t.Fatalf("Sessions snake = %#v, want 14/20", snake.Sessions)
	}

	emptyEncoded, err := json.Marshal(SessionsConfig{})
	if err != nil {
		t.Fatalf("Marshal(empty) error = %v", err)
	}
	if string(emptyEncoded) != `{}` {
		t.Fatalf("Marshal(empty) = %s, want {}", emptyEncoded)
	}
	if (SessionsConfig{}).Enabled() {
		t.Fatalf("zero SessionsConfig must be disabled so existing users are not pruned")
	}
}

func TestSessionsConfigPresentOnOverridesAndResolved(t *testing.T) {
	overrides := Overrides{Sessions: SessionsConfig{RetentionDays: 7}}
	resolved := ResolvedConfig{Sessions: SessionsConfig{MaxCount: 12}}
	if overrides.Sessions.RetentionDays != 7 {
		t.Fatalf("Overrides.Sessions.RetentionDays = %d, want 7", overrides.Sessions.RetentionDays)
	}
	if resolved.Sessions.MaxCount != 12 {
		t.Fatalf("ResolvedConfig.Sessions.MaxCount = %d, want 12", resolved.Sessions.MaxCount)
	}
}
