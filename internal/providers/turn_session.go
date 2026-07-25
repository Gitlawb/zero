package providers

import (
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providers/openai"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// openaiTurnSessionEnv gates the optimized OpenAI turn session (prewarm +
// prefix telemetry). Default OFF: unset, "0", or "false" (any case) leave the
// agent loop's default adapter path untouched. Set to any other non-empty
// value (e.g. "1") to enable — the same boolean idiom as ZERO_FORMAT_ON_WRITE.
const openaiTurnSessionEnv = "ZERO_OPENAI_TURN_SESSION"

func openaiTurnSessionEnabled() bool {
	value := strings.TrimSpace(os.Getenv(openaiTurnSessionEnv))
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

// OptimizedTurnSessions returns the gated optimized TurnSessionProvider for an
// already-built provider, or (nil, false) when the gate is off or the profile
// is ineligible. Callers leave agent Options.TurnSessionProvider nil on false,
// so the loop's default wrap keeps today's behavior byte-identical.
//
// Eligibility is deliberately narrow: the resolved provider kind must be
// official OpenAI (NOT openai-compatible gateways — the constructor merges the
// two, so this branches on the resolved kind explicitly), not the ChatGPT
// Codex catalog, and the provider value must be the concrete *openai.Provider
// (a fake, a Codex provider, or nil falls back to the default path safely).
// No base-URL check on top of the kind: prewarm and fingerprint telemetry are
// harmless against any host, and kind==openai mirrors the existing
// official-OpenAI precedent used for prompt_cache_key.
func OptimizedTurnSessions(profile config.ProviderProfile, provider zeroruntime.Provider, options Options) (zeroruntime.TurnSessionProvider, bool) {
	if !openaiTurnSessionEnabled() {
		return nil, false
	}
	resolved, err := resolveProfile(profile, options)
	if err != nil || resolved.providerKind != config.ProviderKindOpenAI || isCodexCatalog(profile, resolved) {
		return nil, false
	}
	concrete, ok := provider.(*openai.Provider)
	if !ok {
		return nil, false
	}
	caps, err := resolveCapabilities(profile, options)
	if err != nil {
		// resolveProfile above already succeeded, so this is effectively
		// unreachable — but never ship an optimized session with unknown
		// capabilities; the default path is the safe answer.
		return nil, false
	}
	return openai.NewTurnSessionProvider(concrete, caps), true
}

// DefaultTurnSessions wraps an already-built provider in the DEFAULT (no-op)
// turn-session adapter with the profile's resolved capability projection. It is
// the non-optimized sibling of OptimizedTurnSessions for callers that still
// want capabilities populated — e.g. the mid-run model-switch fallback when the
// switched model is not eligible for the optimized session. Capability
// resolution failure degrades to an unknown (zero) projection rather than
// failing the wrap: the default session has no behavior that depends on
// capabilities, so a swap must never be blocked by a projection error.
func DefaultTurnSessions(profile config.ProviderProfile, provider zeroruntime.Provider, options Options) zeroruntime.TurnSessionProvider {
	caps, err := resolveCapabilities(profile, options)
	if err != nil {
		caps = zeroruntime.ProviderCapabilities{}
	}
	return zeroruntime.NewProviderTurnSessionProvider(provider, caps)
}
