package oauth

import (
	"errors"
	"testing"
)

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
}
