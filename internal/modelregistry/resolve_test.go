package modelregistry

import "testing"

func mkEntry(id, alias string) ModelEntry {
	return ModelEntry{
		ID: id, DisplayName: id, APIModel: id, Provider: ProviderAnthropic,
		ContextLimits: ContextLimits{ContextWindow: 200000, MaxOutputTokens: 64000},
		Capabilities:  ModelCapabilities{ModelCapabilityChat},
		Status:        ModelStatusActive, Aliases: []string{alias},
		Cost: ModelCost{
			Currency: "USD", Unit: "per_1m_tokens",
			InputPerMillion: 1, OutputPerMillion: 2,
			Source: "test", SourceLastVerified: "2026-06-06",
		},
	}
}

func resolveTestRegistry(t *testing.T) Registry {
	t.Helper()
	sonnet := mkEntry("claude-sonnet-4-5", "sonnet-4.5")
	sonnet.MatchPatterns = []string{`(?i)sonnet[^a-z0-9]*4[.\s]?5`}
	sonnet.ReasoningEfforts = []ReasoningEffort{ReasoningEffortNone, ReasoningEffortLow, ReasoningEffortHigh}
	sonnet.DefaultReasoningEffort = ReasoningEffortLow

	old := mkEntry("claude-sonnet-4-0", "sonnet-4.0")
	old.Status = ModelStatusDeprecated
	old.Deprecation = &DeprecationRule{FallbackID: "claude-sonnet-4-5", WarningMsg: "sonnet-4-0 retired; use 4.5"}

	reg, err := NewRegistry([]ModelEntry{sonnet, old})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestResolveRegexAlias(t *testing.T) {
	reg := resolveTestRegistry(t)
	for _, in := range []string{"claude-sonnet-4-5", "sonnet-4.5", "Sonnet 4.5", "sonnet4.5"} {
		m, ok := reg.Resolve(in)
		if !ok || m.ID != "claude-sonnet-4-5" {
			t.Errorf("Resolve(%q) = %q,%v; want claude-sonnet-4-5", in, m.ID, ok)
		}
	}
	if _, ok := reg.Resolve("totally-unknown"); ok {
		t.Error("unknown input should not resolve")
	}
}

func TestResolveWithFallbackRedirectsDeprecated(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, notice, ok := reg.ResolveWithFallback("claude-sonnet-4-0")
	if !ok || m.ID != "claude-sonnet-4-5" {
		t.Fatalf("expected redirect to 4.5, got %q,%v", m.ID, ok)
	}
	if notice == "" {
		t.Error("expected a deprecation notice")
	}
}

func TestResolveWithFallbackActiveNoNotice(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, notice, ok := reg.ResolveWithFallback("Sonnet 4.5")
	if !ok || m.ID != "claude-sonnet-4-5" || notice != "" {
		t.Fatalf("active model should resolve cleanly, got %q notice=%q", m.ID, notice)
	}
}

func TestEffectiveReasoningEffort(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, _ := reg.Get("claude-sonnet-4-5")
	if got := EffectiveReasoningEffort(m, ReasoningEffortHigh); got != ReasoningEffortHigh {
		t.Errorf("supported effort = %q; want high", got)
	}
	if got := EffectiveReasoningEffort(m, ReasoningEffortXHigh); got != ReasoningEffortLow {
		t.Errorf("unsupported effort should fall back to default low, got %q", got)
	}
	if got := EffectiveReasoningEffort(m, ""); got != ReasoningEffortLow {
		t.Errorf("empty effort should use default low, got %q", got)
	}
}
