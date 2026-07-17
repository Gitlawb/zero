package zeroruntime

import (
	"context"
	"errors"
)

// ProviderCapabilities is a flat, provider-agnostic projection of a resolved
// model's static capabilities. It is deliberately plain data — no modelregistry
// or reasoning types — because modelregistry imports zeroruntime, so a typed
// reference here would form an import cycle. The providers factory populates it
// from the model-registry entry it already resolves; a zero value means the
// capabilities are unknown, which every consumer must treat as "assume nothing".
type ProviderCapabilities struct {
	// Model is the resolved API model id (informational; may be empty).
	Model string
	// ContextWindow is the model's max input context in tokens; 0 = unknown.
	ContextWindow int
	// MaxOutputTokens is the model's max output tokens per turn; 0 = unknown.
	MaxOutputTokens int
	// SupportsVision mirrors the registry's vision capability.
	SupportsVision bool
	// SupportsReasoning mirrors the registry's reasoning capability.
	SupportsReasoning bool
	// SupportsPromptCache mirrors the registry's prompt-cache capability.
	SupportsPromptCache bool
	// ReasoningEfforts lists the accepted effort tiers, weakest to strongest.
	// nil/empty means the model exposes no effort control.
	ReasoningEfforts []string
	// NativeCompaction reports server-side compaction support. Always false for
	// the default adapter; a future native-compaction session sets it, and the
	// loop branches on it to call TurnSession.Compact instead of the local
	// summarizer.
	NativeCompaction bool
}

// ErrCompactionUnsupported is returned by TurnSession.Compact when the provider
// has no native server-side compaction (every default adapter). Callers treat
// it as "use the local summarizer path", not as a run failure.
var ErrCompactionUnsupported = errors.New("zeroruntime: native compaction unsupported")

// TurnSession is one provider conversation for the lifetime of a single agent
// run: many turns, compaction summaries, and any mid-run model swaps each open
// a fresh session. It owns whatever per-run provider state an optimized
// implementation keeps warm (a pooled connection, a cached-prefix handle). The
// default adapter holds nothing and every method degrades to today's one-shot
// request path.
type TurnSession interface {
	// Prewarm optionally primes provider-side state before the first Stream.
	// It must be safe to skip entirely: the default adapter no-ops and returns
	// nil. A non-nil error is advisory — callers proceed without prewarming
	// (best-effort, never fatal).
	Prewarm(ctx context.Context) error

	// Stream issues one completion request. The signature is identical to
	// Provider.StreamCompletion so the default adapter forwards verbatim and
	// callers' stream handling stays byte-for-byte unchanged.
	Stream(ctx context.Context, request CompletionRequest) (<-chan StreamEvent, error)

	// Compact asks the provider to compact the conversation server-side and
	// returns the replacement messages. The default adapter returns
	// ErrCompactionUnsupported; callers then keep their local summarizer path.
	Compact(ctx context.Context, request CompletionRequest) ([]Message, error)

	// Close releases per-run provider state. It must be idempotent and safe to
	// call after a mid-run swap as well as at run teardown. Default: no-op.
	Close() error
}

// TurnSessionProvider opens a TurnSession for one run and exposes the resolved
// model's static capabilities. The providers factory builds the default
// implementation by wrapping an existing Provider.
type TurnSessionProvider interface {
	// OpenTurnSession starts a session for one run. Everything a session needs
	// per request (messages, tools, reasoning effort, prompt-cache key) already
	// arrives on Stream, so nothing beyond ctx is passed here — keeping the
	// seam narrow. The default adapter never errors; a real implementation may
	// fail a handshake, which callers surface as a clean run-start error.
	OpenTurnSession(ctx context.Context) (TurnSession, error)

	// Capabilities returns the resolved model's static capability projection.
	Capabilities() ProviderCapabilities
}

// NewProviderTurnSessionProvider wraps an existing Provider as a default
// TurnSessionProvider. caps may be the zero value (unknown) for callers that
// only need streaming behavior; the providers factory supplies a populated
// projection.
func NewProviderTurnSessionProvider(provider Provider, caps ProviderCapabilities) TurnSessionProvider {
	return providerTurnSessionProvider{provider: provider, caps: caps}
}

// providerTurnSessionProvider is the default TurnSessionProvider: it wraps a
// Provider so every current provider plugs into the session seam unchanged.
type providerTurnSessionProvider struct {
	provider Provider
	caps     ProviderCapabilities
}

func (d providerTurnSessionProvider) OpenTurnSession(context.Context) (TurnSession, error) {
	return providerTurnSession{provider: d.provider}, nil
}

func (d providerTurnSessionProvider) Capabilities() ProviderCapabilities {
	return d.caps
}

// providerTurnSession forwards Stream to the wrapped Provider; Prewarm and
// Close are no-ops and Compact is unsupported. The value receiver keeps it
// stateless, so double-Close and post-Close Stream are inherently safe.
type providerTurnSession struct {
	provider Provider
}

func (s providerTurnSession) Prewarm(context.Context) error { return nil }

func (s providerTurnSession) Stream(ctx context.Context, request CompletionRequest) (<-chan StreamEvent, error) {
	return s.provider.StreamCompletion(ctx, request)
}

func (s providerTurnSession) Compact(context.Context, CompletionRequest) ([]Message, error) {
	return nil, ErrCompactionUnsupported
}

func (s providerTurnSession) Close() error { return nil }
