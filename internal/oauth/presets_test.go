package oauth

import "testing"

func TestResolveConfigUsesXAIPreset(t *testing.T) {
	r := NewRegistry()
	// Non-nil empty env => no env vars (and no os.Getenv fallback), so the preset
	// supplies everything.
	cfg, flow, err := r.ResolveConfig("xai", map[string]string{})
	if err != nil {
		t.Fatalf("ResolveConfig(xai): %v", err)
	}
	if cfg.ClientID != "b1a00492-073a-47ea-816f-4c329264a828" {
		t.Fatalf("client_id = %q", cfg.ClientID)
	}
	if cfg.AuthorizationEndpoint != "https://auth.x.ai/oauth2/authorize" {
		t.Fatalf("authorize = %q", cfg.AuthorizationEndpoint)
	}
	if cfg.TokenEndpoint != "https://auth.x.ai/oauth2/token" {
		t.Fatalf("token = %q", cfg.TokenEndpoint)
	}
	if cfg.DeviceAuthorizationEndpoint != "https://auth.x.ai/oauth2/device/code" {
		t.Fatalf("device = %q", cfg.DeviceAuthorizationEndpoint)
	}
	if flow != FlowLoopback {
		t.Fatalf("flow = %q, want loopback", flow)
	}
	if len(cfg.Scopes) == 0 {
		t.Fatal("preset scopes should be populated")
	}
}

func TestResolveConfigEnvOverridesPreset(t *testing.T) {
	r := NewRegistry()
	env := map[string]string{
		"ZERO_OAUTH_XAI_CLIENT_ID": "custom-id",
		"ZERO_OAUTH_XAI_SCOPES":    "alpha beta",
		"ZERO_OAUTH_XAI_FLOW":      "device",
	}
	cfg, flow, err := r.ResolveConfig("xai", env)
	if err != nil {
		t.Fatalf("ResolveConfig(xai, env): %v", err)
	}
	if cfg.ClientID != "custom-id" {
		t.Fatalf("env should override client_id, got %q", cfg.ClientID)
	}
	if len(cfg.Scopes) != 2 || cfg.Scopes[0] != "alpha" {
		t.Fatalf("env should override scopes, got %v", cfg.Scopes)
	}
	if flow != FlowDevice {
		t.Fatalf("env should override flow, got %q", flow)
	}
	// A field not overridden still comes from the preset.
	if cfg.TokenEndpoint != "https://auth.x.ai/oauth2/token" {
		t.Fatalf("non-overridden token endpoint = %q", cfg.TokenEndpoint)
	}
}

func TestResolveConfigNoPresetStillRequiresEnv(t *testing.T) {
	r := NewRegistry()
	if _, _, err := r.ResolveConfig("acme-no-preset", map[string]string{}); err == nil {
		t.Fatal("a provider with neither preset nor env config should error")
	}
}
