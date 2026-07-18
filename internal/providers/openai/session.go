package openai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/providers/providerio"
	"github.com/Gitlawb/zero/internal/trace"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// prewarmTimeout bounds the best-effort prewarm probe. The probe runs in the
// background, so this cap only limits how long the goroutine may linger — it
// can never delay the first turn.
const prewarmTimeout = 3 * time.Second

// NewTurnSessionProvider wraps an already-constructed *Provider in the
// optimized OpenAI turn session: a best-effort connection prewarm at run start
// plus request-prefix stability telemetry. Stream is Provider.StreamCompletion
// verbatim, so runtime request behavior is identical to the default adapter —
// the session adds observation and pool priming, never a different request.
func NewTurnSessionProvider(provider *Provider, caps zeroruntime.ProviderCapabilities) zeroruntime.TurnSessionProvider {
	return &turnSessionProvider{provider: provider, caps: caps}
}

type turnSessionProvider struct {
	provider *Provider
	caps     zeroruntime.ProviderCapabilities
}

func (p *turnSessionProvider) OpenTurnSession(context.Context) (zeroruntime.TurnSession, error) {
	return &turnSession{provider: p.provider}, nil
}

func (p *turnSessionProvider) Capabilities() zeroruntime.ProviderCapabilities {
	return p.caps
}

// turnSession is one run's optimized OpenAI session. The agent loop serializes
// all provider I/O through its session shim, so fields need no mutex. The
// fingerprint counters exist so a future stateful-reuse session (Responses API)
// inherits a proven prefix-stability detector; on chat completions there is no
// server-side response state to invalidate, so drift is telemetry only.
type turnSession struct {
	provider        *Provider
	lastFingerprint string
	prewarmOnce     sync.Once
	// prewarmDone closes when the background probe settles; tests wait on it.
	prewarmDone chan struct{}
}

// Prewarm launches one bounded, unauthenticated HEAD probe to the provider's
// base URL in the background so the TCP+TLS handshake lands in the shared
// connection pool while the loop assembles the first prompt. One attempt, no
// retries, no bearer token — the goal is the handshake, not a 2xx (a 401/404/
// 405 response still primes the pool). Always returns nil: prewarm is advisory
// by contract and never required for correctness. On macOS the shared transport
// disables keep-alives (degraded pooled connections are indistinguishable from
// backend slowness there), so the probe is a documented functional no-op. The
// pool's 30s idle timeout bounds the warm window; a slower start simply falls
// back to a cold dial, never worse than today.
func (s *turnSession) Prewarm(ctx context.Context) error {
	s.prewarmOnce.Do(func() {
		done := make(chan struct{})
		s.prewarmDone = done
		recorder := trace.FromContext(ctx)
		provider := s.provider
		go func() {
			defer close(done)
			probeCtx, cancel := context.WithTimeout(ctx, prewarmTimeout)
			defer cancel()
			span := recorder.Span(trace.SpanProviderPrewarm)
			defer span.End()
			recorder.Counter(trace.CounterPrewarmAttempts, 1)
			response, err := providerio.SendWithRetry(probeCtx, provider.httpClient,
				http.MethodHead, provider.baseURL, nil,
				func(request *http.Request) {
					if provider.userAgent != "" {
						request.Header.Set("User-Agent", provider.userAgent)
					}
				}, 1)
			if err == nil {
				_ = response.Body.Close()
			}
		}()
	})
	return nil
}

func (s *turnSession) Stream(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	s.observePrefix(ctx, request)
	return s.provider.StreamCompletion(ctx, request)
}

func (s *turnSession) Compact(context.Context, zeroruntime.CompletionRequest) ([]zeroruntime.Message, error) {
	return nil, zeroruntime.ErrCompactionUnsupported
}

// Close is an idempotent no-op: the shared transport pool owns the connections.
func (s *turnSession) Close() error { return nil }

// observePrefix tracks whether the request-prefix parameters stayed stable
// between this session's streams. The first stream seeds the fingerprint and
// counts as neither stable nor drift.
func (s *turnSession) observePrefix(ctx context.Context, request zeroruntime.CompletionRequest) {
	fingerprint := s.computeFingerprint(request)
	if s.lastFingerprint == "" {
		s.lastFingerprint = fingerprint
		return
	}
	recorder := trace.FromContext(ctx)
	if fingerprint == s.lastFingerprint {
		recorder.Counter(trace.CounterPrefixStable, 1)
		return
	}
	recorder.Counter(trace.CounterPrefixDrift, 1)
	s.lastFingerprint = fingerprint
}

// computeFingerprint digests the request-prefix parameters: the session's
// model and max-tokens (fixed at construction — CompletionRequest carries no
// model field), the normalized reasoning effort as it would appear on the
// wire, and a canonical digest of the advertised tools. PromptCacheKey is
// deliberately excluded: it is the per-session cache-routing key, so hashing
// it would make identical prefixes across sessions look distinct and defeat
// reuse detection. Messages are excluded because they grow every turn — the
// fingerprint covers prefix parameters, not conversation content.
func (s *turnSession) computeFingerprint(request zeroruntime.CompletionRequest) string {
	var builder strings.Builder
	builder.WriteString(s.provider.model)
	builder.WriteByte('|')
	fmt.Fprintf(&builder, "%d", s.provider.maxTokens)
	builder.WriteByte('|')
	builder.WriteString(openAIReasoningEffort(request.ReasoningEffort))
	builder.WriteByte('|')
	builder.WriteString(canonicalToolsDigest(request.Tools))
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

// canonicalToolsDigest renders the tool set order-insensitively: name-sorted,
// each tool as name + description + its JSON-marshaled parameters
// (encoding/json sorts map keys, so the render is stable). Descriptions are
// model-visible request bytes, so a description-only change counts as prefix
// drift. A schema that fails to marshal is recorded under a stable sentinel —
// this digest feeds telemetry counters, so cruder handling of that
// pathological case is acceptable.
func canonicalToolsDigest(tools []zeroruntime.ToolDefinition) string {
	rendered := make([]string, 0, len(tools))
	for _, tool := range tools {
		schema, err := json.Marshal(tool.Parameters)
		if err != nil {
			rendered = append(rendered, tool.Name+"\n"+tool.Description+"\n__non_json:"+tool.Name)
			continue
		}
		rendered = append(rendered, tool.Name+"\n"+tool.Description+"\n"+string(schema))
	}
	sort.Strings(rendered)
	sum := sha256.Sum256([]byte(strings.Join(rendered, "\x00")))
	return hex.EncodeToString(sum[:])
}
