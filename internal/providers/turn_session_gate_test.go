package providers

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func openaiEligibleProfile() config.ProviderProfile {
	return config.ProviderProfile{
		Name:         "official-openai",
		ProviderKind: config.ProviderKindOpenAI,
		BaseURL:      "https://provider.example/v1",
		APIKey:       "sk-gate-test",
		Model:        "pr8-unregistered-model",
	}
}

func buildGateProvider(t *testing.T, profile config.ProviderProfile) zeroruntime.Provider {
	t.Helper()
	provider, err := New(profile, Options{UserAgent: "zero-gate-test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return provider
}

type fakeGateProvider struct{}

func (fakeGateProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	return nil, nil
}

func TestOptimizedTurnSessionsDefaultOff(t *testing.T) {
	// No env set: the gate must be off even for a fully eligible profile.
	profile := openaiEligibleProfile()
	tsp, ok := OptimizedTurnSessions(profile, buildGateProvider(t, profile), Options{})
	if ok || tsp != nil {
		t.Fatal("optimized turn sessions must be disabled by default")
	}
}

func TestOptimizedTurnSessionsFalseyValues(t *testing.T) {
	profile := openaiEligibleProfile()
	provider := buildGateProvider(t, profile)
	for _, value := range []string{"0", "false", "FALSE", "False", " ", ""} {
		t.Setenv(openaiTurnSessionEnv, value)
		if _, ok := OptimizedTurnSessions(profile, provider, Options{}); ok {
			t.Fatalf("gate enabled for falsey value %q", value)
		}
	}
	for _, value := range []string{"1", "true", "on"} {
		t.Setenv(openaiTurnSessionEnv, value)
		if _, ok := OptimizedTurnSessions(profile, provider, Options{}); !ok {
			t.Fatalf("gate disabled for truthy value %q", value)
		}
	}
}

func TestOptimizedTurnSessionsOpenAIEligible(t *testing.T) {
	t.Setenv(openaiTurnSessionEnv, "1")
	profile := openaiEligibleProfile()
	tsp, ok := OptimizedTurnSessions(profile, buildGateProvider(t, profile), Options{})
	if !ok || tsp == nil {
		t.Fatal("expected the optimized session for an official-OpenAI profile with the gate on")
	}
	session, err := tsp.OpenTurnSession(context.Background())
	if err != nil {
		t.Fatalf("OpenTurnSession: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if caps := tsp.Capabilities(); caps.Model != "pr8-unregistered-model" {
		t.Fatalf("Capabilities().Model = %q, want the resolved api model", caps.Model)
	}
}

func TestOptimizedTurnSessionsRejectsCompatible(t *testing.T) {
	t.Setenv(openaiTurnSessionEnv, "1")
	profile := openaiEligibleProfile()
	profile.ProviderKind = config.ProviderKindOpenAICompatible
	if _, ok := OptimizedTurnSessions(profile, buildGateProvider(t, profile), Options{}); ok {
		t.Fatal("openai-compatible gateways must not get the optimized session")
	}
}

func TestOptimizedTurnSessionsRejectsCodexCatalog(t *testing.T) {
	t.Setenv(openaiTurnSessionEnv, "1")
	profile := openaiEligibleProfile()
	profile.CatalogID = "chatgpt"
	// Build the provider from the base profile: the point under test is the
	// catalog check, not Codex construction (which needs OAuth wiring).
	if _, ok := OptimizedTurnSessions(profile, buildGateProvider(t, openaiEligibleProfile()), Options{}); ok {
		t.Fatal("the ChatGPT Codex catalog must not get the optimized session")
	}
}

func TestDefaultTurnSessionsPreservesResolvedCapabilities(t *testing.T) {
	profile := openaiEligibleProfile()
	tsp := DefaultTurnSessions(profile, buildGateProvider(t, profile), Options{})
	if tsp == nil {
		t.Fatal("DefaultTurnSessions returned nil")
	}
	caps := tsp.Capabilities()
	if caps.Model != "pr8-unregistered-model" {
		t.Fatalf("Capabilities().Model = %q, want the resolved api model", caps.Model)
	}
	session, err := tsp.OpenTurnSession(context.Background())
	if err != nil {
		t.Fatalf("OpenTurnSession: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOptimizedTurnSessionsRejectsForeignProviderValue(t *testing.T) {
	t.Setenv(openaiTurnSessionEnv, "1")
	if _, ok := OptimizedTurnSessions(openaiEligibleProfile(), fakeGateProvider{}, Options{}); ok {
		t.Fatal("a non-*openai.Provider value must fall back to the default path")
	}
	if _, ok := OptimizedTurnSessions(openaiEligibleProfile(), nil, Options{}); ok {
		t.Fatal("a nil provider must fall back to the default path")
	}
}
