package oauth

import (
	"errors"
	"testing"
)

// isolateKimiDeviceIDStorage redirects os.UserConfigDir so kimiidentity never
// writes kimi-device-id under the real user config root. DeviceID is path-keyed,
// so setting these env vars is enough (no separate cache reset).
func isolateKimiDeviceIDStorage(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("HOME", root)
}

func TestResolveConfigFromEnv(t *testing.T) {
	r := NewRegistry()
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID":     "my-client",
		"ZERO_OAUTH_DEMO_CLIENT_SECRET": "shh",
		"ZERO_OAUTH_DEMO_SCOPES":        "read write",
		"ZERO_OAUTH_DEMO_AUTHORIZE_URL": "https://auth.example.com/authorize",
		"ZERO_OAUTH_DEMO_TOKEN_URL":     "https://auth.example.com/token",
	}
	cfg, flow, err := r.ResolveConfig("demo", env)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.ClientID != "my-client" || cfg.ClientSecret != "shh" {
		t.Fatalf("client creds not applied: %+v", cfg)
	}
	if len(cfg.Scopes) != 2 || cfg.Scopes[0] != "read" {
		t.Fatalf("scopes = %v", cfg.Scopes)
	}
	if cfg.TokenEndpoint != "https://auth.example.com/token" {
		t.Fatalf("token endpoint = %q", cfg.TokenEndpoint)
	}
	if flow != FlowLoopback {
		t.Fatalf("flow = %q, want loopback default", flow)
	}
}

func TestResolveConfigDeviceFlow(t *testing.T) {
	r := NewRegistry()
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID":  "c",
		"ZERO_OAUTH_DEMO_TOKEN_URL":  "https://auth.example.com/token",
		"ZERO_OAUTH_DEMO_DEVICE_URL": "https://auth.example.com/device",
		"ZERO_OAUTH_DEMO_FLOW":       "device",
	}
	_, flow, err := r.ResolveConfig("demo", env)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if flow != FlowDevice {
		t.Fatalf("flow = %q, want device", flow)
	}
}

func TestResolveConfigRequiresClientID(t *testing.T) {
	r := NewRegistry()
	if _, _, err := r.ResolveConfig("demo", map[string]string{}); err == nil {
		t.Fatal("missing client id must error")
	}
}

func TestResolveConfigRequiresEndpointsOrIssuer(t *testing.T) {
	r := NewRegistry()
	// client id but no token endpoint and no issuer => error.
	_, _, err := r.ResolveConfig("demo", map[string]string{"ZERO_OAUTH_DEMO_CLIENT_ID": "c"})
	if err == nil {
		t.Fatal("missing endpoints/issuer must error")
	}
}

func TestResolveConfigRejectsInsecureEndpoint(t *testing.T) {
	r := NewRegistry()
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID":     "c",
		"ZERO_OAUTH_DEMO_AUTHORIZE_URL": "https://auth.example.com/authorize",
		"ZERO_OAUTH_DEMO_TOKEN_URL":     "http://insecure.example/token", // non-https, non-loopback
	}
	_, _, err := r.ResolveConfig("demo", env)
	if !errors.Is(err, ErrInsecureTokenEndpoint) {
		t.Fatalf("err = %v, want ErrInsecureTokenEndpoint", err)
	}
}

func TestResolveConfigInvalidName(t *testing.T) {
	r := NewRegistry()
	for _, bad := range []string{"", "has space", "../escape", "a/b"} {
		if _, _, err := r.ResolveConfig(bad, nil); err == nil {
			t.Errorf("ResolveConfig(%q) should reject invalid name", bad)
		}
	}
}

func TestEnvKey(t *testing.T) {
	if got := envKey("my-svc", "CLIENT_ID"); got != "ZERO_OAUTH_MY_SVC_CLIENT_ID" {
		t.Fatalf("envKey = %q", got)
	}
	if got := envKey("two.part", "SCOPES"); got != "ZERO_OAUTH_TWO_PART_SCOPES" {
		t.Fatalf("envKey = %q", got)
	}
	if got := envKey("kimi-code", "TOKEN_URL"); got != "ZERO_OAUTH_KIMI_CODE_TOKEN_URL" {
		t.Fatalf("envKey(kimi-code) = %q", got)
	}
}

func TestResolveConfigKimiCodeStripsExtraHeadersOnEndpointOverride(t *testing.T) {
	isolateKimiDeviceIDStorage(t)
	r := NewRegistry()
	// Canonical host (no override): ExtraHeaders has X-Msh-Device-Id
	cfgCanonical, _, err := r.ResolveConfig("kimi-code", map[string]string{
		"ZERO_OAUTH_ALLOW_PRESETS": "1",
	})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if len(cfgCanonical.ExtraHeaders) == 0 || cfgCanonical.ExtraHeaders["X-Msh-Device-Id"] == "" {
		t.Fatalf("expected X-Msh-Device-Id on canonical host, got: %v", cfgCanonical.ExtraHeaders)
	}

	// Overridden to non-canonical host: ExtraHeaders must be empty
	cfgOverride, _, err := r.ResolveConfig("kimi-code", map[string]string{
		"ZERO_OAUTH_ALLOW_PRESETS":       "1",
		"ZERO_OAUTH_KIMI_CODE_TOKEN_URL": "https://my-custom-proxy.example.com/oauth/token",
	})
	if err != nil {
		t.Fatalf("ResolveConfig with override: %v", err)
	}
	if len(cfgOverride.ExtraHeaders) != 0 {
		t.Fatalf("expected empty ExtraHeaders on non-canonical host override, got: %v", cfgOverride.ExtraHeaders)
	}
}

func TestIsCanonicalKimiHostAllowlist(t *testing.T) {
	approved := []string{
		"https://auth.kimi.com/api/oauth/token",
		"https://AUTH.KIMI.COM/path",
		"https://api.kimi.com/coding/v1",
		"https://api.kimi.com/",
	}
	for _, ep := range approved {
		if !isCanonicalKimiHost(ep) {
			t.Fatalf("isCanonicalKimiHost(%q) = false, want true", ep)
		}
	}
	rejected := []string{
		"https://evil.kimi.com/oauth",
		"https://kimi.com/oauth",
		"https://www.kimi.com/",
		"https://auth.moonshot.cn/oauth",
		"https://api.moonshot.cn/v1",
		"https://attacker.example.com/",
		"https://auth.kimi.com.evil.example/",
		// Non-HTTPS schemes must never receive the device identity even on an
		// approved host (a proxy override could otherwise downgrade to cleartext).
		"http://auth.kimi.com/oauth",
		"http://api.kimi.com/v1",
		"ftp://AUTH.KIMI.COM/oauth",
		"//auth.kimi.com/oauth",
		"not a url",
		"",
	}
	for _, ep := range rejected {
		if isCanonicalKimiHost(ep) {
			t.Fatalf("isCanonicalKimiHost(%q) = true, want false", ep)
		}
	}
}

// Overriding only to another approved Kimi host must keep identity headers.
func TestResolveConfigKimiCodeKeepsExtraHeadersOnApprovedOverride(t *testing.T) {
	isolateKimiDeviceIDStorage(t)
	r := NewRegistry()
	cfg, _, err := r.ResolveConfig("kimi-code", map[string]string{
		"ZERO_OAUTH_ALLOW_PRESETS":       "1",
		"ZERO_OAUTH_KIMI_CODE_TOKEN_URL": "https://api.kimi.com/oauth/token",
	})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.ExtraHeaders["X-Msh-Device-Id"] == "" {
		t.Fatalf("expected ExtraHeaders on approved host override, got: %v", cfg.ExtraHeaders)
	}
}

// A kimi.com subdomain that is not on the allowlist must strip identity headers.
func TestResolveConfigKimiCodeStripsExtraHeadersOnKimiSubdomain(t *testing.T) {
	isolateKimiDeviceIDStorage(t)
	r := NewRegistry()
	cfg, _, err := r.ResolveConfig("kimi-code", map[string]string{
		"ZERO_OAUTH_ALLOW_PRESETS":        "1",
		"ZERO_OAUTH_KIMI_CODE_TOKEN_URL":  "https://evil.kimi.com/oauth/token",
		"ZERO_OAUTH_KIMI_CODE_DEVICE_URL": "https://evil.kimi.com/oauth/device",
	})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if len(cfg.ExtraHeaders) != 0 {
		t.Fatalf("expected empty ExtraHeaders on unapproved kimi subdomain, got: %v", cfg.ExtraHeaders)
	}
}

func TestResolveConfigKimiIssuerOnlyClearsPresetEndpoints(t *testing.T) {
	isolateKimiDeviceIDStorage(t)
	r := NewRegistry()

	canonical, _, err := r.ResolveConfig("kimi-code", map[string]string{
		"ZERO_OAUTH_ALLOW_PRESETS":        "1",
		"ZERO_OAUTH_KIMI_CODE_ISSUER_URL": "https://auth.kimi.com",
	})
	if err != nil {
		t.Fatalf("ResolveConfig canonical issuer: %v", err)
	}
	if canonical.IssuerURL != "https://auth.kimi.com" {
		t.Fatalf("IssuerURL = %q", canonical.IssuerURL)
	}
	if canonical.TokenEndpoint != "" || canonical.DeviceAuthorizationEndpoint != "" {
		t.Fatalf("issuer-only override must not keep preset endpoints: token=%q device=%q", canonical.TokenEndpoint, canonical.DeviceAuthorizationEndpoint)
	}
	if canonical.ExtraHeaders["X-Msh-Device-Id"] == "" {
		t.Fatalf("canonical issuer should keep identity headers, got %#v", canonical.ExtraHeaders)
	}

	foreign, _, err := r.ResolveConfig("kimi-code", map[string]string{
		"ZERO_OAUTH_ALLOW_PRESETS":        "1",
		"ZERO_OAUTH_KIMI_CODE_ISSUER_URL": "https://idp.example/oauth",
	})
	if err != nil {
		t.Fatalf("ResolveConfig foreign issuer: %v", err)
	}
	if foreign.TokenEndpoint != "" || foreign.DeviceAuthorizationEndpoint != "" {
		t.Fatalf("foreign issuer must not keep preset Kimi endpoints: token=%q device=%q", foreign.TokenEndpoint, foreign.DeviceAuthorizationEndpoint)
	}
	if len(foreign.ExtraHeaders) != 0 {
		t.Fatalf("foreign issuer must not send X-Msh-* headers, got %#v", foreign.ExtraHeaders)
	}
}
