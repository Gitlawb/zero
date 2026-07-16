package oauth

import (
	"os"
	"strings"
)

// envWithPresetsAllowed returns an env map that opts into the baked-in presets
// (as if ZERO_OAUTH_ALLOW_PRESETS=1 were exported) so an interactive wizard/CLI
// login for a well-known public client (e.g. xAI) can use the preset without the
// operator setting the flag themselves. It copies base — or snapshots the process
// environment when base is nil — because envValue treats a non-nil map as hermetic
// (a missing key does NOT fall back to os.Getenv), so a partial map would silently
// drop the operator's ZERO_OAUTH_<NAME>_* overrides. The flag is then forced on.
func envWithPresetsAllowed(base map[string]string) map[string]string {
	env := make(map[string]string, len(base)+1)
	if base == nil {
		for _, kv := range os.Environ() {
			if eq := strings.IndexByte(kv, '='); eq > 0 {
				env[kv[:eq]] = kv[eq+1:]
			}
		}
	} else {
		for k, v := range base {
			env[k] = v
		}
	}
	env["ZERO_OAUTH_ALLOW_PRESETS"] = "1"
	return env
}

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
	// Hugging Face uses its public OAuth/OIDC server at huggingface.co/oauth/*.
	// HF lets you create a "public" OAuth app (no client secret) and gives a
	// client_id per registration. Unlike xAI there is no globally-shipped
	// client_id we can bake in, so the preset ships endpoints + scopes + issuer
	// pre-filled; the operator supplies ZERO_OAUTH_HUGGINGFACE_CLIENT_ID from
	// the app they create at https://huggingface.co/settings/applications/new.
	// Device-code is the simpler headless path; the loopback flow also works.
	"huggingface": {
		AuthorizationEndpoint:       "https://huggingface.co/oauth/authorize",
		TokenEndpoint:               "https://huggingface.co/oauth/token",
		DeviceAuthorizationEndpoint: "https://huggingface.co/oauth/device",
		IssuerURL:                   "https://huggingface.co",
		Scopes:                      []string{"openid", "profile", "email", "inference-api"},
		Flow:                        FlowDevice,
	},
	// ChatGPT (Codex) uses the same OAuth client identity the `codex` CLI ships
	// publicly. The token works against `chatgpt.com/backend-api/codex/responses`
	// (NOT `api.openai.com`) for ChatGPT Plus/Pro/Business/Enterprise subscribers
	// and carries the `chatgpt-account-id` claim that the Codex backend requires
	// as a header on every request. The flow is loopback (browser required);
	// there is no public device-code path.
	"chatgpt": {
		ClientID:              "app_EMoamEEZ73f0CkXaXp7hrann",
		AuthorizationEndpoint: "https://auth.openai.com/oauth/authorize",
		TokenEndpoint:         "https://auth.openai.com/oauth/token",
		IssuerURL:             "https://auth.openai.com",
		Scopes:                []string{"openid", "profile", "email", "offline_access", "api.connectors.read", "api.connectors.invoke"},
		Flow:                  FlowLoopback,
	},
	// GitHub Copilot uses the same public OAuth client identity the Copilot
	// editor plugins ship (client_id Iv1.b507a08c87ecfe98). The device-code
	// grant against github.com yields a durable GitHub user token (scope
	// read:user, no refresh token); that token is NOT sent to the model
	// endpoint directly. The runtime exchanges it at
	// api.github.com/copilot_internal/v2/token for a short-lived Copilot token
	// (~30 min) which is the bearer for api.githubcopilot.com — see
	// internal/provideroauth/copilot.go. Device flow is the only path
	// (the plugin client has no loopback redirect registration).
	//
	// CAVEATS: this is an UNDOCUMENTED, reverse-engineered use of GitHub's
	// Copilot API. It is not a supported developer API, may change or break
	// without notice, and bulk/automated use can trigger GitHub's abuse
	// detection and suspend Copilot access. Requires an active Copilot
	// subscription (individual, business, or enterprise).
	"copilot": {
		ClientID:                    "Iv1.b507a08c87ecfe98",
		DeviceAuthorizationEndpoint: "https://github.com/login/device/code",
		TokenEndpoint:               "https://github.com/login/oauth/access_token",
		Scopes:                      []string{"read:user"},
		Flow:                        FlowDevice,
	},
}

// lookupOAuthPreset returns the baked-in preset for a provider name (if any).
func lookupOAuthPreset(name string) (providerPreset, bool) {
	preset, ok := builtinOAuthPresets[strings.ToLower(strings.TrimSpace(name))]
	return preset, ok
}

// PresetPrefersDeviceFlow reports whether the baked-in preset for a provider
// defaults to the RFC 8628 device-code flow — i.e. device code is its natural (or
// only) login path. A UI consults this to show the device-code screen (with the
// user_code) instead of the loopback/browser screen for such providers, notably
// GitHub Copilot whose OAuth client has no loopback redirect registered at all
// (its preset carries no AuthorizationEndpoint). Presets are read regardless of
// the ZERO_OAUTH_ALLOW_PRESETS opt-in because this is a UI capability probe, not
// use of the preset's client identity to obtain a credential.
func PresetPrefersDeviceFlow(name string) bool {
	preset, ok := lookupOAuthPreset(name)
	if !ok {
		return false
	}
	return preset.Flow == FlowDevice
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
