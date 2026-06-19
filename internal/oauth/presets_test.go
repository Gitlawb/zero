package oauth

import (
	"strings"
	"testing"
)

func TestScopesOrPresetReturnsACopy(t *testing.T) {
	preset := []string{"openid", "profile"}
	got := scopesOrPreset("", preset)
	if len(got) != 2 || got[0] != "openid" {
		t.Fatalf("got %v, want the preset scopes", got)
	}
	// Mutating the result must not bleed into the shared preset slice.
	got[0] = "MUTATED"
	if preset[0] != "openid" {
		t.Fatalf("scopesOrPreset aliased the shared preset slice: %v", preset)
	}
}

func TestResolveConfigUsesXAIPreset(t *testing.T) {
	r := NewRegistry()
	// Presets are opt-in; with ZERO_OAUTH_ALLOW_PRESETS set and no other env vars
	// the preset supplies everything.
	cfg, flow, err := r.ResolveConfig("xai", map[string]string{"ZERO_OAUTH_ALLOW_PRESETS": "1"})
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
		"ZERO_OAUTH_ALLOW_PRESETS": "1",
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

// Without the opt-in flag the xAI preset must stay inert: no third-party client
// identity is baked into the default credential path, and the error points the
// user at the opt-in.
func TestResolveConfigPresetInertWithoutOptIn(t *testing.T) {
	r := NewRegistry()
	_, _, err := r.ResolveConfig("xai", map[string]string{})
	if err == nil {
		t.Fatal("xai must not resolve from the preset unless ZERO_OAUTH_ALLOW_PRESETS is set")
	}
	if !strings.Contains(err.Error(), "ZERO_OAUTH_ALLOW_PRESETS") {
		t.Fatalf("error should point at the opt-in, got: %v", err)
	}
}

// Hugging Face ships endpoints + scopes + issuer pre-filled but no global
// client_id (HF requires a one-time app registration). With no env vars the
// preset stays inert — the error points the user at the env var to set — even
// though endpoints, issuer, and scopes are pre-filled.
func TestResolveConfigHuggingFaceRequiresClientID(t *testing.T) {
	r := NewRegistry()
	_, _, err := r.ResolveConfig("huggingface", map[string]string{"ZERO_OAUTH_ALLOW_PRESETS": "1"})
	if err == nil {
		t.Fatal("huggingface must not resolve from the preset alone (no baked-in client_id)")
	}
	if !strings.Contains(err.Error(), "ZERO_OAUTH_HUGGINGFACE_CLIENT_ID") {
		t.Fatalf("error should point at the env var, got: %v", err)
	}
}

// With the env-supplied client_id, the preset resolves fully (endpoints +
// scopes + issuer + flow come from the preset; client_id from env).
func TestResolveConfigHuggingFaceWithEnvClientID(t *testing.T) {
	r := NewRegistry()
	env := map[string]string{
		"ZERO_OAUTH_ALLOW_PRESETS":         "1",
		"ZERO_OAUTH_HUGGINGFACE_CLIENT_ID": "test-client-id-value",
		"ZERO_OAUTH_HUGGINGFACE_SCOPES":    "openid inference-api",
	}
	cfg, flow, err := r.ResolveConfig("huggingface", env)
	if err != nil {
		t.Fatalf("ResolveConfig(huggingface, env): %v", err)
	}
	if cfg.ClientID != "test-client-id-value" {
		t.Fatalf("client_id = %q", cfg.ClientID)
	}
	if cfg.AuthorizationEndpoint != "https://huggingface.co/oauth/authorize" {
		t.Fatalf("authorize endpoint = %q", cfg.AuthorizationEndpoint)
	}
	if cfg.DeviceAuthorizationEndpoint != "https://huggingface.co/oauth/device" {
		t.Fatalf("device endpoint = %q", cfg.DeviceAuthorizationEndpoint)
	}
	if flow != FlowDevice {
		t.Fatalf("flow = %q, want device (headless-first)", flow)
	}
	if len(cfg.Scopes) != 2 || cfg.Scopes[0] != "openid" || cfg.Scopes[1] != "inference-api" {
		t.Fatalf("env should override scopes, got %v", cfg.Scopes)
	}
}

// ChatGPT (Codex) ships a baked-in client_id (the public Codex CLI identity),
// so the preset resolves without env. The flow is loopback because the Codex
// backend requires a browser; there is no device-code path.
func TestResolveConfigChatGPTPreset(t *testing.T) {
	r := NewRegistry()
	cfg, flow, err := r.ResolveConfig("chatgpt", map[string]string{"ZERO_OAUTH_ALLOW_PRESETS": "1"})
	if err != nil {
		t.Fatalf("ResolveConfig(chatgpt): %v", err)
	}
	if cfg.ClientID != "app_EMoamEEZ73f0CkXaXp7hrann" {
		t.Fatalf("client_id = %q", cfg.ClientID)
	}
	if cfg.AuthorizationEndpoint != "https://auth.openai.com/oauth/authorize" {
		t.Fatalf("authorize = %q", cfg.AuthorizationEndpoint)
	}
	if cfg.TokenEndpoint != "https://auth.openai.com/oauth/token" {
		t.Fatalf("token = %q", cfg.TokenEndpoint)
	}
	if flow != FlowLoopback {
		t.Fatalf("flow = %q, want loopback (Codex requires a browser)", flow)
	}
	wantScopes := map[string]bool{"openid": true, "profile": true, "email": true, "offline_access": true}
	for _, s := range cfg.Scopes {
		if !wantScopes[s] {
			t.Fatalf("unexpected scope %q in %v", s, cfg.Scopes)
		}
	}
	// No device endpoint — the ChatGPT flow is loopback-only.
	if cfg.DeviceAuthorizationEndpoint != "" {
		t.Fatalf("device endpoint = %q, want empty (Codex has no device flow)", cfg.DeviceAuthorizationEndpoint)
	}
}
