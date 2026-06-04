package config

import "testing"

func TestDefaultContractGapsCoverProviderAndPolicyFields(t *testing.T) {
	gaps := DefaultContractGaps()
	fields := make(map[string]ContractGap)
	for _, gap := range gaps {
		fields[gap.Field] = gap
	}

	for _, field := range []string{
		"env.OPENAI_API_KEY",
		"env.ZERO_PROVIDER",
		"providers[].timeout",
		"providers[].retryPolicy",
		"providers[].maxOutputTokens",
		"providers[].reasoningEffort",
		"permissions.mode",
		"sandbox.policy",
	} {
		gap, ok := fields[field]
		if !ok {
			t.Fatalf("missing config contract gap for %s", field)
		}
		if gap.Owner != ContractOwnerRuntime {
			t.Fatalf("gap %s owner = %q, want %q", field, gap.Owner, ContractOwnerRuntime)
		}
		if gap.Milestone == "" {
			t.Fatalf("gap %s should include milestone", field)
		}
		if gap.Reason == "" {
			t.Fatalf("gap %s should include reason", field)
		}
	}
}

func TestFindContractGapsByMilestone(t *testing.T) {
	gaps := FindContractGapsByMilestone(DefaultContractGaps(), "M0")
	if len(gaps) == 0 {
		t.Fatal("expected M0 gaps")
	}
	for _, gap := range gaps {
		if gap.Milestone != "M0" {
			t.Fatalf("got milestone %q, want M0", gap.Milestone)
		}
	}
}
