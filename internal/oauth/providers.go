package oauth

import (
	"fmt"
	"regexp"
	"strings"
)

// Flow selects how a provider delivers the authorization result.
type Flow string

const (
	// FlowLoopback uses a 127.0.0.1 callback server (browser required).
	FlowLoopback Flow = "loopback"
	// FlowDevice uses the RFC 8628 device-code flow (headless/SSH).
	FlowDevice Flow = "device"
)

// providerNamePattern bounds a provider name to a safe identifier that is also a
// valid store-key segment.
var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateProviderName reports whether name is a safe provider identifier.
func ValidateProviderName(name string) error {
	if !providerNamePattern.MatchString(name) {
		return fmt.Errorf("oauth: invalid provider name %q", name)
	}
	return nil
}

// Registry resolves a provider's Config from env/config. By default every provider
// is defined entirely by the operator via ZERO_OAUTH_<NAME>_* variables, so no
// third-party OAuth client identity is used. A small set of built-in presets
// exists for convenience but stays inert unless the operator opts in with
// ZERO_OAUTH_ALLOW_PRESETS (see presetsAllowed); env always overrides a preset.
type Registry struct{}

// NewRegistry returns the (stateless) env-driven registry.
func NewRegistry() *Registry { return &Registry{} }

// envKey builds the ZERO_OAUTH_<NAME>_<suffix> variable name for a provider.
func envKey(name, suffix string) string {
	up := strings.ToUpper(name)
	up = strings.NewReplacer("-", "_", ".", "_").Replace(up)
	return "ZERO_OAUTH_" + up + "_" + suffix
}

// ResolveConfig builds the oauth.Config and default Flow for a provider from its
// env/config:
//
//	ZERO_OAUTH_<NAME>_CLIENT_ID       (required)
//	ZERO_OAUTH_<NAME>_CLIENT_SECRET   (optional)
//	ZERO_OAUTH_<NAME>_AUTHORIZE_URL   (loopback flow; or discovered via issuer)
//	ZERO_OAUTH_<NAME>_TOKEN_URL       (or discovered via issuer)
//	ZERO_OAUTH_<NAME>_DEVICE_URL      (device flow; or discovered via issuer)
//	ZERO_OAUTH_<NAME>_ISSUER_URL      (RFC 8414 / OIDC discovery base)
//	ZERO_OAUTH_<NAME>_SCOPES          (space-separated)
//	ZERO_OAUTH_<NAME>_FLOW            ("loopback" [default] | "device")
//
// Pinned credential-bearing endpoints must be https (loopback exempt).
func (r *Registry) ResolveConfig(name string, env map[string]string) (Config, Flow, error) {
	if err := ValidateProviderName(name); err != nil {
		return Config{}, "", err
	}
	// A baked-in preset (if any) supplies defaults, but ONLY when the operator opts
	// in with ZERO_OAUTH_ALLOW_PRESETS — otherwise no third-party client identity is
	// used and every field must come from a ZERO_OAUTH_<NAME>_* env var (env wins).
	var preset providerPreset
	if presetsAllowed(env) {
		preset, _ = lookupOAuthPreset(name)
	}
	cfg := Config{
		ClientID:                    firstNonEmpty(strings.TrimSpace(envValue(env, envKey(name, "CLIENT_ID"))), preset.ClientID),
		ClientSecret:                firstNonEmpty(strings.TrimSpace(envValue(env, envKey(name, "CLIENT_SECRET"))), preset.ClientSecret),
		AuthorizationEndpoint:       firstNonEmpty(strings.TrimSpace(envValue(env, envKey(name, "AUTHORIZE_URL"))), preset.AuthorizationEndpoint),
		TokenEndpoint:               firstNonEmpty(strings.TrimSpace(envValue(env, envKey(name, "TOKEN_URL"))), preset.TokenEndpoint),
		DeviceAuthorizationEndpoint: firstNonEmpty(strings.TrimSpace(envValue(env, envKey(name, "DEVICE_URL"))), preset.DeviceAuthorizationEndpoint),
		IssuerURL:                   firstNonEmpty(strings.TrimSpace(envValue(env, envKey(name, "ISSUER_URL"))), preset.IssuerURL),
		Scopes:                      scopesOrPreset(envValue(env, envKey(name, "SCOPES")), preset.Scopes),
	}
	if cfg.ClientID == "" {
		hint := ""
		if _, ok := lookupOAuthPreset(name); ok {
			hint = " (or set ZERO_OAUTH_ALLOW_PRESETS=1 to use the built-in preset)"
		}
		return Config{}, "", fmt.Errorf("oauth: provider %q is not configured; set %s (and its endpoints or an issuer)%s", name, envKey(name, "CLIENT_ID"), hint)
	}
	var flow Flow
	switch strings.ToLower(strings.TrimSpace(envValue(env, envKey(name, "FLOW")))) {
	case "":
		flow = preset.Flow
		if flow == "" {
			flow = FlowLoopback
		}
	case string(FlowLoopback):
		flow = FlowLoopback
	case string(FlowDevice):
		flow = FlowDevice
	default:
		return Config{}, "", fmt.Errorf("oauth: provider %q has invalid %s (want loopback or device)", name, envKey(name, "FLOW"))
	}
	// A token endpoint (for exchange/refresh) must be reachable directly or via
	// discovery. The per-flow authorize/device endpoints are checked at login
	// time (after discovery), so a refresh-only config needs only a token URL.
	if cfg.IssuerURL == "" && cfg.TokenEndpoint == "" {
		return Config{}, "", fmt.Errorf("oauth: provider %q needs %s or %s", name, envKey(name, "TOKEN_URL"), envKey(name, "ISSUER_URL"))
	}
	for _, ep := range []string{cfg.TokenEndpoint, cfg.AuthorizationEndpoint, cfg.DeviceAuthorizationEndpoint, cfg.IssuerURL} {
		if ep == "" {
			continue
		}
		if err := validateTokenEndpoint(ep); err != nil {
			return Config{}, "", err
		}
	}
	return cfg, flow, nil
}
