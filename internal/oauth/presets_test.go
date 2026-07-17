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

// envWithPresetsAllowed lets a caller (the wizard / CLI login / runtime refresh)
// opt into the preset without exporting ZERO_OAUTH_ALLOW_PRESETS: an otherwise-inert
// xAI config now resolves from the baked-in preset.
func TestEnvWithPresetsAllowedEnablesPreset(t *testing.T) {
	r := NewRegistry()
	// Baseline: a hermetic empty map keeps the preset inert.
	if _, _, err := r.ResolveConfig("xai", map[string]string{}); err == nil {
		t.Fatal("xai should not resolve without the opt-in")
	}
	// The helper forces the opt-in so the preset resolves.
	cfg, _, err := r.ResolveConfig("xai", envWithPresetsAllowed(map[string]string{}))
	if err != nil {
		t.Fatalf("envWithPresetsAllowed should enable the xai preset: %v", err)
	}
	if cfg.ClientID != "b1a00492-073a-47ea-816f-4c329264a828" {
		t.Fatalf("client_id = %q", cfg.ClientID)
	}
}

// The helper copies the base map (never mutating it) and keeps ZERO_OAUTH_<NAME>_*
// overrides — critical because envValue treats a non-nil map as hermetic, so a
// partial map would silently drop them.
func TestEnvWithPresetsAllowedPreservesOverrides(t *testing.T) {
	base := map[string]string{"ZERO_OAUTH_XAI_CLIENT_ID": "custom-id"}
	env := envWithPresetsAllowed(base)
	if env["ZERO_OAUTH_ALLOW_PRESETS"] != "1" {
		t.Fatalf("opt-in flag not set: %v", env)
	}
	if _, mutated := base["ZERO_OAUTH_ALLOW_PRESETS"]; mutated {
		t.Fatal("envWithPresetsAllowed mutated the caller's base map")
	}
	cfg, _, err := NewRegistry().ResolveConfig("xai", env)
	if err != nil {
		t.Fatalf("ResolveConfig with override env: %v", err)
	}
	if cfg.ClientID != "custom-id" {
		t.Fatalf("override dropped through the helper: client_id = %q", cfg.ClientID)
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

// Kimi Code ships a baked-in client_id (the public kimi-code CLI identity) AND a
// device-code endpoint, so the preset resolves without env. The flow is device
// only (RFC 8628): no loopback/authorize endpoint, no issuer discovery.
func TestResolveConfigKimiPreset(t *testing.T) {
	r := NewRegistry()
	cfg, flow, err := r.ResolveConfig("kimi", map[string]string{"ZERO_OAUTH_ALLOW_PRESETS": "1"})
	if err != nil {
		t.Fatalf("ResolveConfig(kimi): %v", err)
	}
	if cfg.ClientID != "17e5f671-d194-4dfb-9706-5516cb48c098" {
		t.Fatalf("client_id = %q", cfg.ClientID)
	}
	if cfg.DeviceAuthorizationEndpoint != "https://auth.kimi.com/api/oauth/device_authorization" {
		t.Fatalf("device endpoint = %q", cfg.DeviceAuthorizationEndpoint)
	}
	if cfg.TokenEndpoint != "https://auth.kimi.com/api/oauth/token" {
		t.Fatalf("token = %q", cfg.TokenEndpoint)
	}
	if cfg.AuthorizationEndpoint != "" {
		t.Fatalf("authorize endpoint = %q, want empty (Kimi has no loopback flow)", cfg.AuthorizationEndpoint)
	}
	if flow != FlowDevice {
		t.Fatalf("flow = %q, want device (RFC 8628 only)", flow)
	}
	if len(cfg.Scopes) == 0 {
		t.Fatal("preset scopes should be populated")
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
	// The preset must request the base OIDC scopes AND the api.connectors scopes
	// the ChatGPT authorize endpoint requires (without them it rejects the flow
	// with authorize_hydra_invalid_request).
	allowedScopes := map[string]bool{
		"openid": true, "profile": true, "email": true, "offline_access": true,
		"api.connectors.read": true, "api.connectors.invoke": true,
	}
	for _, s := range cfg.Scopes {
		if !allowedScopes[s] {
			t.Fatalf("unexpected scope %q in %v", s, cfg.Scopes)
		}
	}
	for _, required := range []string{"api.connectors.read", "api.connectors.invoke"} {
		found := false
		for _, s := range cfg.Scopes {
			if s == required {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing required scope %q in %v", required, cfg.Scopes)
		}
	}
	// No device endpoint — the ChatGPT flow is loopback-only.
	if cfg.DeviceAuthorizationEndpoint != "" {
		t.Fatalf("device endpoint = %q, want empty (Codex has no device flow)", cfg.DeviceAuthorizationEndpoint)
	}
}
