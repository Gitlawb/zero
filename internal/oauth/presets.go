package oauth

import "strings"

// providerPreset is a baked-in default OAuth configuration for a well-known
// provider. Every field is overridable per provider via ZERO_OAUTH_<NAME>_*
// env vars (env wins). Only providers whose OAuth flow is verified to yield a
// credential usable for model calls are listed here.
type providerPreset struct {
	ClientID                    string
	ClientSecret                string
	AuthorizationEndpoint       string
	TokenEndpoint               string
	DeviceAuthorizationEndpoint string
	IssuerURL                   string
	Scopes                      []string
	Flow                        Flow
}

// builtinOAuthPresets maps a provider name to its default OAuth config.
//
// These presets are OFF by default and only consulted when the operator opts in
// with ZERO_OAUTH_ALLOW_PRESETS (see presetsAllowed). A preset carries a
// third-party OAuth client identity, and the engine keeps such identities out of
// the default credential path (see the package doc) — opting in is an explicit
// acknowledgement that the binary's preset client_id will be used when no
// ZERO_OAUTH_<NAME>_* override is set.
//
// xAI (Grok): the client_id is a PUBLIC client (no secret) used by several Grok
// CLIs; its access token is accepted directly as a bearer on api.x.ai/v1 (an
// OpenAI-compatible endpoint), so no header/identity spoofing is involved.
// CAVEATS: it is NOT formally documented by xAI as a public developer API and may
// change without notice (override via ZERO_OAUTH_XAI_*), and using it requires a
// SuperGrok / X Premium+ subscription. Pay-as-you-go users should use a console
// API key instead.
var builtinOAuthPresets = map[string]providerPreset{
	"xai": {
		ClientID:                    "b1a00492-073a-47ea-816f-4c329264a828",
		AuthorizationEndpoint:       "https://auth.x.ai/oauth2/authorize",
		TokenEndpoint:               "https://auth.x.ai/oauth2/token",
		DeviceAuthorizationEndpoint: "https://auth.x.ai/oauth2/device/code",
		IssuerURL:                   "https://auth.x.ai",
		Scopes:                      []string{"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access"},
		Flow:                        FlowLoopback,
	},
}

// lookupOAuthPreset returns the baked-in preset for a provider name (if any).
func lookupOAuthPreset(name string) (providerPreset, bool) {
	preset, ok := builtinOAuthPresets[strings.ToLower(strings.TrimSpace(name))]
	return preset, ok
}

// presetsAllowed reports whether baked-in OAuth presets may supply defaults. They
// are OFF unless the operator opts in with ZERO_OAUTH_ALLOW_PRESETS set to a
// truthy value, keeping any third-party OAuth client identity out of the default
// credential path (a preset client_id is only ever used after explicit opt-in).
func presetsAllowed(env map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(envValue(env, "ZERO_OAUTH_ALLOW_PRESETS"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// scopesOrPreset returns the env scopes (space-separated) when set, else the
// preset's scopes.
func scopesOrPreset(envScopes string, preset []string) []string {
	if fields := strings.Fields(envScopes); len(fields) > 0 {
		return fields
	}
	// Copy so a caller appending to cfg.Scopes can't mutate the shared preset slice.
	return append([]string(nil), preset...)
}
