package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// fakeTurnSession wraps an inner provider for streaming while counting the
// session lifecycle, so tests can observe exactly how the loop drives it.
type fakeTurnSession struct {
	inner    Provider
	prewarms int
	streams  int
	closes   int
	closeErr error
}

func (s *fakeTurnSession) Prewarm(context.Context) error { s.prewarms++; return nil }

func (s *fakeTurnSession) Stream(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	s.streams++
	return s.inner.StreamCompletion(ctx, request)
}

func (s *fakeTurnSession) Compact(context.Context, zeroruntime.CompletionRequest) ([]zeroruntime.Message, error) {
	return nil, zeroruntime.ErrCompactionUnsupported
}

func (s *fakeTurnSession) Close() error { s.closes++; return s.closeErr }

// fakeTurnSessionProvider opens a scripted session (or fails to).
type fakeTurnSessionProvider struct {
	session *fakeTurnSession
	openErr error
	opens   int
}

func (p *fakeTurnSessionProvider) OpenTurnSession(context.Context) (zeroruntime.TurnSession, error) {
	p.opens++
	if p.openErr != nil {
		return nil, p.openErr
	}
	return p.session, nil
}

func (p *fakeTurnSessionProvider) Capabilities() zeroruntime.ProviderCapabilities {
	return zeroruntime.ProviderCapabilities{}
}

func singleAnswerTurns(answer string) [][]zeroruntime.StreamEvent {
	return [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: answer},
		{Type: zeroruntime.StreamEventDone},
	}}
}

// TestRunOpenTurnSessionFailureSurfaces verifies a failed session open is a
// clean run-start error: no panic, no stream ever issued, nothing to close.
func TestRunOpenTurnSessionFailureSurfaces(t *testing.T) {
	positional := &mockProvider{turns: singleAnswerTurns("never reached")}
	tsp := &fakeTurnSessionProvider{openErr: errors.New("handshake refused")}

	_, err := Run(context.Background(), "go", positional, Options{
		MaxTurns:            2,
		TurnSessionProvider: tsp,
	})
	if err == nil {
		t.Fatal("expected an error from a failed session open")
	}
	if !strings.Contains(err.Error(), "open turn session") || !strings.Contains(err.Error(), "handshake refused") {
		t.Fatalf("expected a wrapped open-turn-session error, got %v", err)
	}
	if len(positional.requests) != 0 {
		t.Fatalf("expected no provider request after a failed open, got %d", len(positional.requests))
	}
	if tsp.opens != 1 {
		t.Fatalf("expected exactly one open attempt, got %d", tsp.opens)
	}
}

// TestRunCloseFailureIsSafe verifies a Close error at teardown is swallowed:
// the run still returns its normal result.
func TestRunCloseFailureIsSafe(t *testing.T) {
	inner := &mockProvider{turns: singleAnswerTurns("done")}
	session := &fakeTurnSession{inner: inner, closeErr: errors.New("close blew up")}
	tsp := &fakeTurnSessionProvider{session: session}

	result, err := Run(context.Background(), "go", inner, Options{
		MaxTurns:            2,
		TurnSessionProvider: tsp,
	})
	if err != nil {
		t.Fatalf("expected Close failure to be swallowed, got %v", err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected normal result despite Close error, got %q", result.FinalAnswer)
	}
	if session.closes != 1 {
		t.Fatalf("expected exactly one Close at teardown, got %d", session.closes)
	}
}

// TestRunStreamsThroughTurnSession verifies that when a TurnSessionProvider is
// wired, every model request of the run flows through the session (and the
// session is prewarmed once and closed once).
func TestRunStreamsThroughTurnSession(t *testing.T) {
	inner := &mockProvider{turns: singleAnswerTurns("via session")}
	session := &fakeTurnSession{inner: inner}
	tsp := &fakeTurnSessionProvider{session: session}

	// The positional provider is a DIFFERENT mock: it must stay untouched,
	// proving the session (not the positional provider) carried the run.
	positional := &mockProvider{turns: singleAnswerTurns("wrong path")}

	result, err := Run(context.Background(), "go", positional, Options{
		MaxTurns:            2,
		TurnSessionProvider: tsp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "via session" {
		t.Fatalf("expected the session-backed answer, got %q", result.FinalAnswer)
	}
	if len(positional.requests) != 0 {
		t.Fatalf("expected the positional provider to be bypassed, got %d requests", len(positional.requests))
	}
	if session.streams == 0 || session.streams != len(inner.requests) {
		t.Fatalf("expected every request to flow through the session: streams=%d inner=%d", session.streams, len(inner.requests))
	}
	if session.prewarms != 1 {
		t.Fatalf("expected exactly one prewarm, got %d", session.prewarms)
	}
	if session.closes != 1 {
		t.Fatalf("expected exactly one close, got %d", session.closes)
	}
}

// TestRunDefaultTurnSessionMatchesRawProvider verifies the default (nil
// TurnSessionProvider) path is behavior-identical to passing an explicit
// default adapter over the same provider: same result, same request count.
func TestRunDefaultTurnSessionMatchesRawProvider(t *testing.T) {
	run := func(explicit bool) (Result, *mockProvider) {
		provider := &mockProvider{turns: escalateThenAnswerTurns("identical")}
		registry := tools.NewRegistry()
		registry.Register(escalatingTool{target: ""})
		options := Options{Registry: registry, MaxTurns: 4}
		if explicit {
			options.TurnSessionProvider = zeroruntime.NewProviderTurnSessionProvider(provider, zeroruntime.ProviderCapabilities{})
		}
		result, err := Run(context.Background(), "go", provider, options)
		if err != nil {
			t.Fatal(err)
		}
		return result, provider
	}

	defaultResult, defaultProvider := run(false)
	explicitResult, explicitProvider := run(true)

	if defaultResult.FinalAnswer != explicitResult.FinalAnswer {
		t.Fatalf("final answers diverged: default=%q explicit=%q", defaultResult.FinalAnswer, explicitResult.FinalAnswer)
	}
	if defaultResult.Turns != explicitResult.Turns {
		t.Fatalf("turn counts diverged: default=%d explicit=%d", defaultResult.Turns, explicitResult.Turns)
	}
	if len(defaultProvider.requests) != len(explicitProvider.requests) {
		t.Fatalf("request counts diverged: default=%d explicit=%d", len(defaultProvider.requests), len(explicitProvider.requests))
	}
}

// TestRunModelSwitchClosesSessionAndContinues verifies a mid-run model switch
// closes the active session exactly once and streams the rest of the run on
// the new provider.
func TestRunModelSwitchClosesSessionAndContinues(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(escalatingTool{target: "claude-opus-4.1"})

	firstInner := &mockProvider{turns: escalateThenAnswerTurns("unused")}
	firstSession := &fakeTurnSession{inner: firstInner}
	tsp := &fakeTurnSessionProvider{session: firstSession}

	secondProvider := &mockProvider{turns: singleAnswerTurns("answered after switch")}

	result, err := Run(context.Background(), "go", firstInner, Options{
		Registry:            registry,
		Model:               "claude-sonnet-4.5",
		MaxTurns:            4,
		TurnSessionProvider: tsp,
		ModelSwitcher: func(_ context.Context, _ string) (Provider, error) {
			return secondProvider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "answered after switch" {
		t.Fatalf("expected the post-switch answer, got %q", result.FinalAnswer)
	}
	// The original session handled exactly the escalation turn, then was closed
	// once by the swap (the teardown defer closes the NEW session, not this one).
	if firstSession.streams != 1 {
		t.Fatalf("expected the original session to stream exactly the escalation turn, got %d", firstSession.streams)
	}
	if firstSession.closes != 1 {
		t.Fatalf("expected the swap to close the original session exactly once, got %d", firstSession.closes)
	}
	if len(secondProvider.requests) != 1 {
		t.Fatalf("expected the post-switch turn on the new provider, got %d requests", len(secondProvider.requests))
	}
}
